package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/lifecycle"

	"github.com/coder/websocket"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	collabPingInterval     = 30 * time.Second
	collabMsgChannelBuffer = 256
	collabTicketTTL        = time.Minute
	collabMaxMessageBytes  = 2 << 20
	collabWriteTimeout     = 10 * time.Second

	collabCloseAccessChanged websocket.StatusCode = 4403
	collabCloseEntitlement   websocket.StatusCode = 4402
	collabCloseTooSlow       websocket.StatusCode = 4408
)

const (
	msgTypeSyncStep1  = "sync_step1"
	msgTypeSyncStep2  = "sync_step2"
	msgTypeUpdate     = "update"
	msgTypeAwareness  = "awareness"
	msgTypeCompact    = "compact"
	msgTypePing       = "ping"
	msgTypePong       = "pong"
	msgTypeWelcome    = "welcome"
	msgTypePeerJoined = "peer_joined"
	msgTypePeerLeft   = "peer_left"
	msgTypeSynced     = "synced"
)

var (
	errCollabAccessChanged = errors.New("collab access changed")
	errCollabTooSlow       = errors.New("collab consumer too slow")
	errCollabShutdown      = errors.New("server shutting down")
	errCollabEntitlement   = errors.New("subscription required")
)

type CollabDeps struct {
	Broker        *RoomBroker
	Documents     CollabDocumentRepository
	Presence      CollabPresenceRepository
	Members       VaultMemberRepository
	Users         UserRepository
	Bus           bus.Bus
	Tickets       *TicketSigner
	Lifecycle     *lifecycle.Lifecycle
	FlushInterval time.Duration
	Authz         *authz.Authorizer
	Guard         StreamGuard
}

type CollabHandler struct {
	broker        *RoomBroker
	documents     CollabDocumentRepository
	presence      CollabPresenceRepository
	users         UserRepository
	bus           bus.Bus
	tickets       *TicketSigner
	lifecycle     *lifecycle.Lifecycle
	authz         *authz.Authorizer
	guard         StreamGuard
	flushInterval time.Duration

	mu     sync.Mutex
	conns  map[string]*collabConn
	remote map[string]map[string]struct{}
}

type collabConn struct {
	vaultID  uuid.UUID
	userID   uuid.UUID
	filePath string
	canWrite bool
	cancel   context.CancelCauseFunc
}

type collabPeer struct {
	ClientID    string `json:"client_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	CanWrite    bool   `json:"can_write"`
}

type collabMessage struct {
	Type     string       `json:"type"`
	Data     []byte       `json:"data,omitempty"`
	UpTo     int64        `json:"up_to,omitempty"`
	ClientID string       `json:"client_id,omitempty"`
	From     *collabPeer  `json:"from,omitempty"`
	Peer     *collabPeer  `json:"peer,omitempty"`
	Peers    []collabPeer `json:"peers,omitempty"`
}

type collabSession struct {
	vaultID  uuid.UUID
	filePath string
	clientID string
	identity collabPeer
	canWrite bool
	binary   bool
	conn     *websocket.Conn
}

func NewCollabHandler(deps CollabDeps) *CollabHandler {
	if deps.FlushInterval <= 0 {
		deps.FlushInterval = 10 * time.Second
	}
	if deps.Lifecycle == nil {
		deps.Lifecycle = lifecycle.New()
	}
	if deps.Authz == nil {
		deps.Authz = authz.New(deps.Members)
	}
	h := &CollabHandler{
		broker:        deps.Broker,
		documents:     deps.Documents,
		presence:      deps.Presence,
		users:         deps.Users,
		bus:           deps.Bus,
		tickets:       deps.Tickets,
		lifecycle:     deps.Lifecycle,
		authz:         deps.Authz,
		flushInterval: deps.FlushInterval,
		guard:         deps.Guard,
		conns:         make(map[string]*collabConn),
		remote:        make(map[string]map[string]struct{}),
	}
	h.bus.Subscribe(bus.KindCollab, h.onRemoteMessage)
	h.bus.Subscribe(bus.KindAccessChanged, func(signal bus.Signal) {
		h.Kick(signal.VaultID, signal.UserID)
	})
	h.bus.Subscribe(bus.KindVaultAccessChanged, func(signal bus.Signal) {
		go h.Recheck(context.Background(), signal.VaultID)
	})
	return h
}

type createCollabTicketRequest struct {
	FilePath string `json:"file_path"`
}

type createCollabTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
}

func (h *CollabHandler) CreateTicket(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	var req createCollabTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.FilePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "file_path is required")
		return
	}

	if _, err := h.authz.RequirePath(r.Context(), vaultID, claims.UserID, req.FilePath, domain.PermFileRead); err != nil {
		writeCollabAccessError(w, err)
		return
	}

	ticket, expiresAt, err := h.tickets.Issue(claims.UserID, vaultID, req.FilePath)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to create collab ticket")
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, createCollabTicketResponse{
		Ticket:    ticket,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

func (h *CollabHandler) Connect(w http.ResponseWriter, r *http.Request) {
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "missing ticket query parameter")
		return
	}

	userID, err := h.tickets.Verify(ticket, vaultID, filePath)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid or expired ticket")
		return
	}

	if h.lifecycle.Draining() {
		httpx.WriteError(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}

	access, err := h.authz.Access(r.Context(), vaultID, userID, domain.PermFileRead)
	if err != nil || !access.Scope.CanRead(filePath) {
		if err == nil {
			err = domain.ErrInsufficientRole
		}
		writeCollabAccessError(w, err)
		return
	}

	user, err := h.users.GetByID(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(collabMaxMessageBytes)

	done := h.lifecycle.TrackStream()
	defer done()

	session := &collabSession{
		vaultID:  vaultID,
		filePath: filePath,
		clientID: uuid.NewString(),
		canWrite: access.Scope.CanWrite(filePath),
		binary:   r.URL.Query().Get("frames") == "binary",
		conn:     conn,
	}
	session.identity = peerFromUser(session.clientID, user, session.canWrite)

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)

	status, reason := h.serve(ctx, cancel, session)
	_ = conn.Close(status, reason)
}

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
	doc, updates, err := h.documents.LoadDocument(ctx, s.vaultID, s.filePath)
	if err != nil {
		return err
	}

	var upTo int64
	for _, u := range updates {
		upTo = max(upTo, u.ID)
	}

	peers, err := h.peersFromPresence(ctx, presence, s.clientID)
	if err != nil {
		return err
	}
	if err := writeCollabMessage(ctx, s.conn, collabMessage{
		Type:     msgTypeWelcome,
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
	for _, pending := range h.broker.PendingUpdates(s.vaultID, s.filePath) {
		if err := s.write(ctx, collabMessage{Type: msgTypeUpdate, Data: pending}); err != nil {
			return err
		}
	}
	return writeCollabMessage(ctx, s.conn, collabMessage{Type: msgTypeSynced})
}

func (h *CollabHandler) readLoop(ctx context.Context, cancel context.CancelCauseFunc, s *collabSession) {
	readCtx := context.WithoutCancel(ctx)
	for {
		typ, data, err := s.conn.Read(readCtx)
		if err != nil {
			cancel(err)
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
			if !s.canWrite || len(msg.Data) == 0 || msg.UpTo <= 0 {
				continue
			}
			if err := h.flushToDB(ctx, s.vaultID, s.filePath); err != nil {
				cancel(err)
				return
			}
			if err := h.documents.CompactDocument(ctx, s.vaultID, s.filePath, msg.Data, msg.UpTo); err != nil {
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

func (h *CollabHandler) flushToDB(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	updates := h.broker.BeginFlush(vaultID, filePath)
	if len(updates) == 0 {
		return nil
	}
	err := h.documents.BatchStoreUpdates(context.WithoutCancel(ctx), vaultID, filePath, updates)
	h.broker.FinishFlush(vaultID, filePath, len(updates), err == nil)
	return err
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

func (h *CollabHandler) peersFromPresence(ctx context.Context, presence []domain.CollabPresence, excludeClientID string) ([]collabPeer, error) {
	ids := make([]uuid.UUID, 0, len(presence))
	for _, p := range presence {
		ids = append(ids, p.UserID)
	}
	users, err := h.users.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*domain.User, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}

	peers := make([]collabPeer, 0, len(presence))
	for _, p := range presence {
		if p.ClientID == excludeClientID {
			continue
		}
		if user, ok := byID[p.UserID]; ok {
			peers = append(peers, peerFromUser(p.ClientID, user, p.CanWrite))
		}
	}
	return peers, nil
}

func (h *CollabHandler) GetPeers(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpx.WriteError(w, http.StatusBadRequest, "missing path query parameter")
		return
	}

	if _, err := h.authz.RequirePath(r.Context(), vaultID, claims.UserID, filePath, domain.PermFileRead); err != nil {
		writeCollabAccessError(w, err)
		return
	}

	presence, err := h.presence.ListRoom(r.Context(), vaultID, filePath)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}
	peers, err := h.peersFromPresence(r.Context(), presence, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}

	peerIDs := make([]string, 0, len(peers))
	for _, p := range peers {
		peerIDs = append(peerIDs, p.ClientID)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"vault_id":   vaultID.String(),
		"file_path":  filePath,
		"peer_count": len(peers),
		"peer_ids":   peerIDs,
		"peers":      peers,
	})
}

type filePresence struct {
	FilePath string       `json:"file_path"`
	Peers    []collabPeer `json:"peers"`
}

func (h *CollabHandler) GetVaultPresence(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return
	}

	access, err := h.authz.Access(r.Context(), vaultID, claims.UserID, domain.PermFileRead)
	if err != nil {
		writeCollabAccessError(w, err)
		return
	}

	all, err := h.presence.ListVault(r.Context(), vaultID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}
	presence := all[:0]
	for _, p := range all {
		if access.Scope.CanRead(p.FilePath) {
			presence = append(presence, p)
		}
	}
	peers, err := h.peersFromPresence(r.Context(), presence, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load presence")
		return
	}

	pathByClient := make(map[string]string, len(presence))
	for _, p := range presence {
		pathByClient[p.ClientID] = p.FilePath
	}
	result := []filePresence{}
	index := make(map[string]int)
	for _, peer := range peers {
		path := pathByClient[peer.ClientID]
		i, ok := index[path]
		if !ok {
			i = len(result)
			index[path] = i
			result = append(result, filePresence{FilePath: path})
		}
		result[i].Peers = append(result[i].Peers, peer)
	}
	httpx.WriteJSON(w, http.StatusOK, result)
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

func closeStatusFor(cause error) (websocket.StatusCode, string) {
	switch {
	case errors.Is(cause, errCollabAccessChanged):
		return collabCloseAccessChanged, "access changed"
	case errors.Is(cause, errCollabTooSlow):
		return collabCloseTooSlow, "too slow, resync"
	case errors.Is(cause, errCollabEntitlement):
		return collabCloseEntitlement, "subscription required"
	case errors.Is(cause, errCollabShutdown):
		return websocket.StatusGoingAway, "server shutting down"
	case errors.Is(cause, domain.ErrCollabBufferFull):
		return websocket.StatusTryAgainLater, "buffer full"
	default:
		return websocket.StatusNormalClosure, ""
	}
}

func writeCollabAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrVaultAccessDenied):
		httpx.WriteError(w, http.StatusForbidden, "not a member of this vault")
	case errors.Is(err, domain.ErrInsufficientRole):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "failed to check vault membership")
	}
}

func peerFromUser(clientID string, user *domain.User, canWrite bool) collabPeer {
	return collabPeer{
		ClientID:    clientID,
		UserID:      user.ID.String(),
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL(),
		CanWrite:    canWrite,
	}
}

func (p collabPeer) userUUID() uuid.UUID {
	id, _ := uuid.Parse(p.UserID)
	return id
}

func (s *collabSession) write(ctx context.Context, msg collabMessage) error {
	typ, data := newOutbound(msg, "").encode(s.binary)
	return s.conn.Write(ctx, typ, data)
}
