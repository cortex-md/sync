package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"nhooyr.io/websocket"
)

const (
	collabPingInterval     = 30 * time.Second
	collabMsgChannelBuffer = 64
	collabTicketTTL        = time.Minute
)

type CollabHandler struct {
	broker        port.CollabBroker
	repo          port.CollabDocumentRepository
	members       port.VaultMemberRepository
	sseBroker     port.SSEBroker
	flushInterval time.Duration
	ticketMu      sync.Mutex
	tickets       map[string]collabTicket
}

type collabTicket struct {
	UserID    uuid.UUID
	VaultID   uuid.UUID
	FilePath  string
	ExpiresAt time.Time
}

func NewCollabHandler(
	broker port.CollabBroker,
	repo port.CollabDocumentRepository,
	members port.VaultMemberRepository,
	_ port.TokenGenerator,
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
		sseBroker:     sseBroker,
		flushInterval: flushInterval,
		tickets:       make(map[string]collabTicket),
	}
}

type createCollabTicketRequest struct {
	FilePath string `json:"file_path"`
}

type createCollabTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
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

func (h *CollabHandler) CreateTicket(w http.ResponseWriter, r *http.Request) {
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

	var req createCollabTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.FilePath == "" {
		WriteError(w, http.StatusBadRequest, "file_path is required")
		return
	}

	if _, err := h.members.GetByVaultAndUser(r.Context(), vaultID, claims.UserID); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusForbidden, "not a member of this vault")
			return
		}
		WriteError(w, http.StatusInternalServerError, "failed to check vault membership")
		return
	}

	ticket, expiresAt, err := h.issueTicket(claims.UserID, vaultID, req.FilePath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to create collab ticket")
		return
	}

	WriteJSON(w, http.StatusCreated, createCollabTicketResponse{
		Ticket:    ticket,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

func (h *CollabHandler) issueTicket(userID uuid.UUID, vaultID uuid.UUID, filePath string) (string, time.Time, error) {
	h.ticketMu.Lock()
	defer h.ticketMu.Unlock()

	h.pruneExpiredTicketsLocked(time.Now())

	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", time.Time{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(randomBytes)
	expiresAt := time.Now().Add(collabTicketTTL)
	h.tickets[ticket] = collabTicket{
		UserID:    userID,
		VaultID:   vaultID,
		FilePath:  filePath,
		ExpiresAt: expiresAt,
	}
	return ticket, expiresAt, nil
}

func (h *CollabHandler) consumeTicket(ticket string, vaultID uuid.UUID, filePath string) (uuid.UUID, error) {
	h.ticketMu.Lock()
	defer h.ticketMu.Unlock()

	now := time.Now()
	h.pruneExpiredTicketsLocked(now)

	issued, ok := h.tickets[ticket]
	if !ok {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	delete(h.tickets, ticket)
	if issued.ExpiresAt.Before(now) || issued.VaultID != vaultID || issued.FilePath != filePath {
		return uuid.Nil, domain.ErrInvalidCredentials
	}
	return issued.UserID, nil
}

func (h *CollabHandler) pruneExpiredTicketsLocked(now time.Time) {
	for ticket, issued := range h.tickets {
		if !issued.ExpiresAt.After(now) {
			delete(h.tickets, ticket)
		}
	}
}

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

	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		WriteError(w, http.StatusUnauthorized, "missing ticket query parameter")
		return
	}

	userID, err := h.consumeTicket(ticket, vaultID, filePath)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, "invalid or expired ticket")
		return
	}

	member, err := h.members.GetByVaultAndUser(r.Context(), vaultID, userID)
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
		lastPeer := h.broker.PeerCount(vaultID, filePath) == 1
		if lastPeer {
			if err := h.flushToDB(context.Background(), vaultID, filePath); err != nil {
				log.Warn().Err(err).Str("vault_id", vaultID.String()).Str("file_path", filePath).Msg("failed to flush collab updates on close")
			}
		}
		h.broker.Leave(vaultID, filePath, clientID)
		if lastPeer {
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
			if err := h.flushToDB(ctx, vaultID, filePath); err != nil {
				log.Warn().Err(err).Str("vault_id", vaultID.String()).Str("file_path", filePath).Msg("failed to flush collab updates")
			}
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

func (h *CollabHandler) flushToDB(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	updates := h.broker.BeginFlush(vaultID, filePath)
	if len(updates) == 0 {
		return nil
	}
	err := h.repo.BatchStoreUpdates(ctx, vaultID, filePath, updates)
	h.broker.FinishFlush(vaultID, filePath, len(updates), err == nil)
	return err
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
			if err := h.broker.BufferUpdate(vaultID, filePath, msg.Data); err != nil {
				errCh <- err
				return
			}
			h.broker.Broadcast(vaultID, filePath, clientID, data)

		case msgTypeAwareness:
			h.broker.Broadcast(vaultID, filePath, clientID, data)

		case msgTypeCompact:
			if !canWrite || len(msg.Data) == 0 {
				continue
			}
			if err := h.flushToDB(ctx, vaultID, filePath); err != nil {
				errCh <- err
				return
			}
			if err := h.repo.CompactDocument(ctx, vaultID, filePath, msg.Data, []byte{}); err != nil {
				errCh <- err
				return
			}
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
