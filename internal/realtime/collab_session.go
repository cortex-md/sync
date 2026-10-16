package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/time/rate"

	"github.com/cortexnotes/cortex-sync/internal/platform/bus"

	"github.com/coder/websocket"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func (h *CollabHandler) serve(ctx context.Context, cancel context.CancelCauseFunc, s *collabSession) (websocket.StatusCode, string) {
	msgCh := make(chan CollabMessage, collabMsgChannelBuffer)
	if _, err := h.broker.Join(s.vaultID, s.filePath, s.clientID, msgCh); err != nil {
		if errors.Is(err, domain.ErrCollabRoomFull) {
			return websocket.StatusPolicyViolation, "room full"
		}
		return websocket.StatusInternalError, "failed to join"
	}

	h.register(s, cancel)
	defer h.unregister(s.clientID)
	defer h.leave(s)

	if err := h.presence.Join(ctx, &domain.CollabPresence{
		ClientID: s.clientID,
		VaultID:  s.vaultID,
		FilePath: s.filePath,
		UserID:   s.identity.userUUID(),
		CanWrite: s.canWrite,
	}); err != nil {
		return websocket.StatusInternalError, "failed to register presence"
	}

	peers, err := h.refreshRemotePeers(ctx, s.vaultID, s.filePath)
	if err != nil {
		return websocket.StatusInternalError, "failed to load presence"
	}
	if len(peers) == 1 {
		h.publishVaultEvent(ctx, s.vaultID, bus.VaultEventCollabActive, s.filePath)
	}

	if err := h.sendInitialState(ctx, s, peers); err != nil {
		return websocket.StatusInternalError, "failed to load document"
	}
	h.relay(ctx, s, collabMessage{Type: msgTypePeerJoined, Peer: &s.identity})

	go h.readLoop(ctx, cancel, s)
	go writeLoop(ctx, cancel, s.conn, msgCh, s.binary)

	pingTicker := time.NewTicker(collabPingInterval)
	flushTicker := time.NewTicker(h.flushInterval)
	defer pingTicker.Stop()
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return closeStatusFor(context.Cause(ctx))
		case <-h.lifecycle.Done():
			cancel(errCollabShutdown)
		case <-pingTicker.C:
			if h.guard != nil && h.guard(ctx, s.vaultID, s.identity.userUUID()) != nil {
				cancel(errCollabEntitlement)
				continue
			}
			if err := writeCollabMessage(ctx, s.conn, collabMessage{Type: msgTypePing}); err != nil {
				cancel(err)
				continue
			}
			if err := h.presence.Touch(ctx, []string{s.clientID}); err != nil {
				log.Warn().Err(err).Msg("collab: failed to refresh presence")
			}
			if _, err := h.refreshRemotePeers(ctx, s.vaultID, s.filePath); err != nil {
				log.Warn().Err(err).Msg("collab: failed to refresh remote peers")
			}
		case <-flushTicker.C:
			if err := h.flushToDB(ctx, s.vaultID, s.filePath); err != nil {
				log.Warn().Err(err).Str("vault_id", s.vaultID.String()).Str("file_path", s.filePath).Msg("collab: failed to flush updates")
			}
		}
	}
}

func (h *CollabHandler) leave(s *collabSession) {
	ctx := context.Background()
	h.broker.Leave(s.vaultID, s.filePath, s.clientID)
	if err := h.flushToDB(ctx, s.vaultID, s.filePath); err != nil {
		log.Warn().Err(err).Str("vault_id", s.vaultID.String()).Str("file_path", s.filePath).Msg("collab: failed to flush updates on leave")
	}
	if err := h.presence.Leave(ctx, s.clientID); err != nil {
		log.Warn().Err(err).Msg("collab: failed to remove presence")
	}
	h.relay(ctx, s, collabMessage{Type: msgTypePeerLeft, ClientID: s.clientID})

	remaining, err := h.presence.ListRoom(ctx, s.vaultID, s.filePath)
	if err == nil && len(remaining) == 0 {
		h.publishVaultEvent(ctx, s.vaultID, bus.VaultEventCollabInactive, s.filePath)
	}
	if !h.broker.HasRoom(s.vaultID, s.filePath) {
		h.mu.Lock()
		delete(h.remote, roomKey(s.vaultID, s.filePath))
		h.mu.Unlock()
	}
}

func (h *CollabHandler) sendInitialState(ctx context.Context, s *collabSession, presence []domain.CollabPresence) error {
	pendingUpdates := h.broker.PendingUpdates(s.vaultID, s.filePath)
	doc, updates, err := h.documents.LoadDocument(ctx, s.vaultID, s.filePath)
	if err != nil {
		return err
	}

	var upTo int64
	for _, u := range updates {
		upTo = max(upTo, u.ID)
	}
	s.upTo = upTo

	peers, err := h.peersFromPresence(ctx, presence, s.clientID)
	if err != nil {
		return err
	}
	if err := writeCollabMessage(ctx, s.conn, collabMessage{
		Type:     msgTypeWelcome,
		Version:  collabProtocolVersion,
		ClientID: s.clientID,
		UpTo:     upTo,
		From:     &s.identity,
		Peers:    peers,
	}); err != nil {
		return err
	}

	if doc != nil && len(doc.CompactedState) > 0 {
		if err := s.write(ctx, collabMessage{Type: msgTypeSyncStep2, Data: doc.CompactedState}); err != nil {
			return err
		}
	}
	for _, u := range updates {
		if err := s.write(ctx, collabMessage{Type: msgTypeUpdate, Data: u.Data}); err != nil {
			return err
		}
	}
	for _, pending := range pendingUpdates {
		if err := s.write(ctx, collabMessage{Type: msgTypeUpdate, Data: pending}); err != nil {
			return err
		}
	}
	return writeCollabMessage(ctx, s.conn, collabMessage{Type: msgTypeSynced})
}

func (h *CollabHandler) readLoop(ctx context.Context, cancel context.CancelCauseFunc, s *collabSession) {
	readCtx := context.WithoutCancel(ctx)
	var limiter *rate.Limiter
	if h.messageRate > 0 && h.messageBurst > 0 {
		limiter = rate.NewLimiter(h.messageRate, h.messageBurst)
	}
	for {
		typ, data, err := s.conn.Read(readCtx)
		if err != nil {
			cancel(err)
			return
		}
		if limiter != nil && !limiter.Allow() {
			cancel(errCollabRateLimited)
			return
		}

		var msg collabMessage
		if typ == websocket.MessageBinary {
			decoded, ok := decodeBinaryFrame(data)
			if !ok {
				continue
			}
			msg = decoded
		} else if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case msgTypeSyncStep1, msgTypeAwareness:
			h.relay(ctx, s, collabMessage{Type: msg.Type, Data: msg.Data})

		case msgTypeUpdate, msgTypeSyncStep2:
			if !s.canWrite || len(msg.Data) == 0 {
				continue
			}
			if err := h.bufferUpdate(ctx, s, msg.Data); err != nil {
				cancel(err)
				return
			}
			h.relay(ctx, s, collabMessage{Type: msg.Type, Data: msg.Data})

		case msgTypeCompact:
			upTo := min(msg.UpTo, s.upTo)
			if !s.canWrite || len(msg.Data) == 0 || upTo <= 0 {
				continue
			}
			if err := h.flushToDB(ctx, s.vaultID, s.filePath); err != nil {
				cancel(err)
				return
			}
			if err := h.documents.CompactDocument(ctx, s.vaultID, s.filePath, msg.Data, upTo); err != nil {
				if errors.Is(err, domain.ErrConflict) {
					continue
				}
				cancel(err)
				return
			}
		}
	}
}

func (h *CollabHandler) bufferUpdate(ctx context.Context, s *collabSession, data []byte) error {
	err := h.broker.BufferUpdate(s.vaultID, s.filePath, data)
	if !errors.Is(err, domain.ErrCollabBufferFull) {
		return err
	}
	if err := h.flushToDB(ctx, s.vaultID, s.filePath); err != nil {
		return err
	}
	return h.broker.BufferUpdate(s.vaultID, s.filePath, data)
}

func (h *CollabHandler) flushToDB(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	updates := h.broker.BeginFlush(vaultID, filePath)
	if len(updates) == 0 {
		return nil
	}
	err := h.documents.BatchStoreUpdates(context.WithoutCancel(ctx), vaultID, filePath, updates)
	h.broker.FinishFlush(vaultID, filePath, len(updates), err == nil)
	return err
}

func writeLoop(ctx context.Context, cancel context.CancelCauseFunc, conn *websocket.Conn, msgCh <-chan CollabMessage, binary bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				cancel(errCollabTooSlow)
				return
			}
			typ, data := msg.Frame.encode(binary)
			writeCtx, done := context.WithTimeout(context.WithoutCancel(ctx), collabWriteTimeout)
			err := conn.Write(writeCtx, typ, data)
			done()
			if err != nil {
				cancel(err)
				return
			}
		}
	}
}

func writeCollabMessage(ctx context.Context, conn *websocket.Conn, msg collabMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func (s *collabSession) write(ctx context.Context, msg collabMessage) error {
	typ, data := newOutbound(msg, "").encode(s.binary)
	return s.conn.Write(ctx, typ, data)
}
