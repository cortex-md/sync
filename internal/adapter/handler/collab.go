package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"
)

const (
	collabPingInterval     = 30 * time.Second
	collabMsgChannelBuffer = 64
)

type CollabHandler struct {
	broker        port.CollabBroker
	repo          port.CollabDocumentRepository
	members       port.VaultMemberRepository
	tokens        port.TokenGenerator
	sseBroker     port.SSEBroker
	flushInterval time.Duration
}

func NewCollabHandler(
	broker port.CollabBroker,
	repo port.CollabDocumentRepository,
	members port.VaultMemberRepository,
	tokens port.TokenGenerator,
	sseBroker port.SSEBroker,
	flushInterval time.Duration,
) *CollabHandler {
	if flushInterval <= 0 {
		flushInterval = 10 * time.Second
	}
	return &CollabHandler{
		broker:        broker,
		repo:          repo,
		members:       members,
		tokens:        tokens,
		sseBroker:     sseBroker,
		flushInterval: flushInterval,
	}
}

type collabSyncMessage struct {
	Type string `json:"type"`
	Data []byte `json:"data,omitempty"`
}

const (
	msgTypeSyncStep1 = "sync_step1"
	msgTypeSyncStep2 = "sync_step2"
	msgTypeUpdate    = "update"
	msgTypeAwareness = "awareness"
	msgTypeCompact   = "compact"
	msgTypePing      = "ping"
	msgTypePong      = "pong"
)

func (h *CollabHandler) Connect(w http.ResponseWriter, r *http.Request) {
	vaultIDStr := chi.URLParam(r, "vaultID")
	vaultID, err := uuid.Parse(vaultIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		WriteError(w, http.StatusUnauthorized, "missing token query parameter")
		return
	}

	claims, err := h.tokens.ValidateAccessToken(token)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}

	member, err := h.members.GetByVaultAndUser(r.Context(), vaultID, claims.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusForbidden, "not a member of this vault")
			return
		}
		WriteError(w, http.StatusInternalServerError, "failed to check vault membership")
		return
	}

	canWrite := member.Role.CanWrite()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	clientID := uuid.New().String()
	msgCh := make(chan port.CollabMessage, collabMsgChannelBuffer)

	newCount, err := h.broker.Join(vaultID, filePath, clientID, msgCh)
	if err != nil {
		if err == domain.ErrCollabRoomFull {
			conn.Close(websocket.StatusPolicyViolation, "room full")
			return
		}
		conn.Close(websocket.StatusInternalError, "failed to join")
		return
	}
	if newCount == 1 {
		h.publishCollabSSE(r.Context(), vaultID, filePath, domain.EventCollabActive)
	}
	defer func() {
		h.broker.Leave(vaultID, filePath, clientID)
		if h.broker.PeerCount(vaultID, filePath) == 0 {
			h.flushToDB(context.Background(), vaultID, filePath)
			h.publishCollabSSE(context.Background(), vaultID, filePath, domain.EventCollabInactive)
		}
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err := h.sendExistingState(ctx, conn, vaultID, filePath); err != nil {
		return
	}

	errCh := make(chan error, 2)

	go h.readLoop(ctx, conn, vaultID, filePath, clientID, canWrite, errCh)
	go h.writeLoop(ctx, conn, msgCh, errCh)

	pingTicker := time.NewTicker(collabPingInterval)
	flushTicker := time.NewTicker(h.flushInterval)
	defer pingTicker.Stop()
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			if err := writeCollabJSON(ctx, conn, collabSyncMessage{Type: msgTypePing}); err != nil {
				return
			}
		case <-flushTicker.C:
			h.flushToDB(ctx, vaultID, filePath)
		case err := <-errCh:
			if err != nil {
				return
			}
		}
	}
}

func (h *CollabHandler) sendExistingState(ctx context.Context, conn *websocket.Conn, vaultID uuid.UUID, filePath string) error {
	doc, updates, err := h.repo.LoadDocument(ctx, vaultID, filePath)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "failed to load document")
		return err
	}

	if doc != nil && len(doc.CompactedState) > 0 {
		if err := writeCollabJSON(ctx, conn, collabSyncMessage{
			Type: msgTypeSyncStep2,
			Data: doc.CompactedState,
		}); err != nil {
			return err
		}
	}

	for _, u := range updates {
		if err := writeCollabJSON(ctx, conn, collabSyncMessage{
			Type: msgTypeUpdate,
			Data: u.Data,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *CollabHandler) flushToDB(ctx context.Context, vaultID uuid.UUID, filePath string) {
	updates := h.broker.FlushUpdates(vaultID, filePath)
	if len(updates) == 0 {
		return
	}
	h.repo.BatchStoreUpdates(ctx, vaultID, filePath, updates)
}

func (h *CollabHandler) publishCollabSSE(ctx context.Context, vaultID uuid.UUID, filePath string, eventType domain.EventType) {
	if h.sseBroker == nil {
		return
	}
	h.sseBroker.Publish(vaultID, port.SSEEvent{
		EventType: string(eventType),
		Data:      fmt.Sprintf(`{"file_path":%q}`, filePath),
	})
}

func (h *CollabHandler) readLoop(ctx context.Context, conn *websocket.Conn, vaultID uuid.UUID, filePath string, clientID string, canWrite bool, errCh chan<- error) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			errCh <- err
			return
		}

		if typ != websocket.MessageText {
			continue
		}

		var msg collabSyncMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case msgTypePong:
			continue

		case msgTypeUpdate, msgTypeSyncStep1, msgTypeSyncStep2:
			if !canWrite {
				continue
			}
			h.broker.BufferUpdate(vaultID, filePath, msg.Data)
			h.broker.Broadcast(vaultID, filePath, clientID, data)

		case msgTypeAwareness:
			h.broker.Broadcast(vaultID, filePath, clientID, data)

		case msgTypeCompact:
			if !canWrite || len(msg.Data) == 0 {
				continue
			}
			h.broker.FlushUpdates(vaultID, filePath)
			h.repo.CompactDocument(ctx, vaultID, filePath, msg.Data, []byte{})
		}
	}
}

func (h *CollabHandler) writeLoop(ctx context.Context, conn *websocket.Conn, msgCh <-chan port.CollabMessage, errCh chan<- error) {
	for {
		select {
		case <-ctx.Done():
			errCh <- ctx.Err()
			return
		case msg := <-msgCh:
			if err := conn.Write(ctx, websocket.MessageText, msg.Data); err != nil {
				errCh <- err
				return
			}
		}
	}
}

func writeCollabJSON(ctx context.Context, conn *websocket.Conn, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func (h *CollabHandler) GetPeers(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	_, err = h.members.GetByVaultAndUser(r.Context(), vaultID, claims.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusForbidden, "not a member of this vault")
			return
		}
		WriteError(w, http.StatusInternalServerError, "failed to check vault membership")
		return
	}

	count := h.broker.PeerCount(vaultID, filePath)
	peerIDs := h.broker.PeerIDs(vaultID, filePath)
	if peerIDs == nil {
		peerIDs = []string{}
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"vault_id":   vaultID.String(),
		"file_path":  filePath,
		"peer_count": count,
		"peer_ids":   peerIDs,
	})
}
