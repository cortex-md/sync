package realtime

import (
	"context"
	"encoding/json"

	"github.com/cortexnotes/cortex-sync/internal/platform/bus"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func (h *CollabHandler) relay(ctx context.Context, s *collabSession, msg collabMessage) {
	if msg.Type != msgTypePeerJoined && msg.Type != msgTypePeerLeft {
		msg.From = &s.identity
	}
	out := newOutbound(msg, s.clientID)
	h.broker.BroadcastFrame(s.vaultID, s.filePath, s.clientID, out)

	if !h.hasRemotePeers(s.vaultID, s.filePath) {
		return
	}
	_, frame := out.encode(false)
	if err := h.bus.Publish(ctx, bus.Signal{
		Kind:     bus.KindCollab,
		VaultID:  s.vaultID,
		FilePath: s.filePath,
		ClientID: s.clientID,
		Event:    msg.Type,
		Data:     frame,
	}); err != nil {
		log.Warn().Err(err).Msg("collab: failed to relay message")
	}
}

func (h *CollabHandler) onRemoteMessage(signal bus.Signal) {
	if signal.Origin == h.bus.InstanceID() || !h.broker.HasRoom(signal.VaultID, signal.FilePath) {
		return
	}

	key := roomKey(signal.VaultID, signal.FilePath)
	switch signal.Event {
	case msgTypePeerJoined:
		h.mu.Lock()
		if h.remote[key] == nil {
			h.remote[key] = make(map[string]struct{})
		}
		h.remote[key][signal.ClientID] = struct{}{}
		h.mu.Unlock()
	case msgTypePeerLeft:
		h.mu.Lock()
		delete(h.remote[key], signal.ClientID)
		h.mu.Unlock()
	}

	var msg collabMessage
	if err := json.Unmarshal(signal.Data, &msg); err != nil {
		return
	}
	h.broker.BroadcastFrame(signal.VaultID, signal.FilePath, signal.ClientID, newOutbound(msg, signal.ClientID))
}

func (h *CollabHandler) refreshRemotePeers(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.CollabPresence, error) {
	presence, err := h.presence.ListRoom(ctx, vaultID, filePath)
	if err != nil {
		return nil, err
	}

	local := make(map[string]struct{})
	for _, id := range h.broker.PeerIDs(vaultID, filePath) {
		local[id] = struct{}{}
	}
	remote := make(map[string]struct{})
	for _, p := range presence {
		if _, isLocal := local[p.ClientID]; !isLocal {
			remote[p.ClientID] = struct{}{}
		}
	}

	h.mu.Lock()
	h.remote[roomKey(vaultID, filePath)] = remote
	h.mu.Unlock()
	return presence, nil
}

func (h *CollabHandler) hasRemotePeers(vaultID uuid.UUID, filePath string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.remote[roomKey(vaultID, filePath)]) > 0
}

func (h *CollabHandler) register(s *collabSession, cancel context.CancelCauseFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[s.clientID] = &collabConn{vaultID: s.vaultID, userID: s.identity.userUUID(), filePath: s.filePath, canWrite: s.canWrite, cancel: cancel}
}

func (h *CollabHandler) unregister(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, clientID)
}

func (h *CollabHandler) Kick(vaultID uuid.UUID, userID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		if c.vaultID == vaultID && c.userID == userID {
			c.cancel(errCollabAccessChanged)
		}
	}
}

func (h *CollabHandler) Recheck(ctx context.Context, vaultID uuid.UUID) {
	h.mu.Lock()
	conns := make([]*collabConn, 0)
	for _, c := range h.conns {
		if c.vaultID == vaultID {
			conns = append(conns, c)
		}
	}
	h.mu.Unlock()

	for _, c := range conns {
		access, err := h.authz.Access(ctx, vaultID, c.userID, domain.PermFileRead)
		if err != nil || !access.Scope.CanRead(c.filePath) || access.Scope.CanWrite(c.filePath) != c.canWrite {
			c.cancel(errCollabAccessChanged)
		}
	}
}

func (h *CollabHandler) publishVaultEvent(ctx context.Context, vaultID uuid.UUID, event string, filePath string) {
	data, _ := json.Marshal(map[string]string{"file_path": filePath})
	if err := h.bus.Publish(context.WithoutCancel(ctx), bus.Signal{
		Kind:    bus.KindSSE,
		VaultID: vaultID,
		Event:   event,
		Data:    data,
	}); err != nil {
		log.Warn().Err(err).Str("event", event).Msg("collab: failed to publish vault event")
	}
}
