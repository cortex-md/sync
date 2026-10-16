package realtime

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/lifecycle"

	"github.com/coder/websocket"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	collabPingInterval     = 30 * time.Second
	collabMsgChannelBuffer = 1024
	collabTicketTTL        = time.Minute
	collabMaxMessageBytes  = 2 << 20
	collabWriteTimeout     = 10 * time.Second

	collabCloseAccessChanged websocket.StatusCode = 4403
	collabCloseEntitlement   websocket.StatusCode = 4402
	collabCloseTooSlow       websocket.StatusCode = 4408
	collabCloseRateLimited   websocket.StatusCode = 4429
	collabProtocolVersion                         = 1
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
	errCollabRateLimited   = errors.New("collab client sent too many messages")
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
	Connections   ConnectionLimiter
	MessageRate   rate.Limit
	MessageBurst  int
}

type ConnectionLimiter interface {
	Acquire(key string) (func(), bool)
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
	connections   ConnectionLimiter
	messageRate   rate.Limit
	messageBurst  int

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
	Version  int          `json:"v,omitempty"`
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
	upTo     int64
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
		connections:   deps.Connections,
		messageRate:   deps.MessageRate,
		messageBurst:  deps.MessageBurst,
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

	if h.connections != nil {
		release, ok := h.connections.Acquire(userID.String())
		if !ok {
			w.Header().Set("Retry-After", "5")
			httpx.WriteErrorWithCode(w, http.StatusTooManyRequests, "too many collab connections", "rate_limited")
			return
		}
		defer release()
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

func closeStatusFor(cause error) (websocket.StatusCode, string) {
	switch {
	case errors.Is(cause, errCollabAccessChanged):
		return collabCloseAccessChanged, "access changed"
	case errors.Is(cause, errCollabTooSlow):
		return collabCloseTooSlow, "too slow, resync"
	case errors.Is(cause, errCollabRateLimited):
		return collabCloseRateLimited, "too many messages"
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
