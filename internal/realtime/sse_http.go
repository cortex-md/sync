package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/authz"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/lifecycle"
	"github.com/cortexnotes/cortex-sync/internal/platform/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type SSEHandler struct {
	broker    *EventBroker
	events    SyncEventRepository
	authz     *authz.Authorizer
	ping      time.Duration
	lifecycle *lifecycle.Lifecycle
	guard     StreamGuard
}

type SSEDeps struct {
	Broker    *EventBroker
	Events    SyncEventRepository
	Members   VaultMemberRepository
	Authz     *authz.Authorizer
	Lifecycle *lifecycle.Lifecycle
	Ping      time.Duration
	Guard     StreamGuard
}

func NewSSEHandler(d SSEDeps) *SSEHandler {
	if d.Ping <= 0 {
		d.Ping = 30 * time.Second
	}
	if d.Lifecycle == nil {
		d.Lifecycle = lifecycle.New()
	}
	if d.Authz == nil {
		d.Authz = authz.New(d.Members)
	}
	return &SSEHandler{
		broker:    d.Broker,
		events:    d.Events,
		authz:     d.Authz,
		ping:      d.Ping,
		guard:     d.Guard,
		lifecycle: d.Lifecycle,
	}
}

func (h *SSEHandler) Events(w http.ResponseWriter, r *http.Request) {
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
		log.Warn().
			Str("vault_id", vaultID.String()).
			Str("user_id", claims.UserID.String()).
			Err(err).
			Msg("SSE vault access check failed")
		if errors.Is(err, domain.ErrVaultAccessDenied) || errors.Is(err, domain.ErrInsufficientRole) {
			httpx.WriteError(w, http.StatusForbidden, "vault access denied")
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}
	visible := eventFilter(access.Scope, claims.UserID)

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	if h.lifecycle.Draining() {
		httpx.WriteError(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	done := h.lifecycle.TrackStream()
	defer done()

	ctx := r.Context()
	clientID := fmt.Sprintf("%s:%s:%s", claims.UserID, httpx.DeviceID(ctx), uuid.New().String())
	ch, err := h.broker.Subscribe(ctx, vaultID, clientID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	middleware.SSEConnectionOpened()
	defer func() {
		h.broker.Unsubscribe(vaultID, clientID)
		middleware.SSEConnectionClosed()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	var lastSentID int64
	if lastEventID := r.Header.Get("Last-Event-ID"); lastEventID != "" {
		if sinceID, parseErr := strconv.ParseInt(lastEventID, 10, 64); parseErr == nil {
			lastSentID = h.replayEvents(ctx, w, flusher, vaultID, sinceID, access.Scope)
		}
	}

	pingTicker := time.NewTicker(h.ping)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.lifecycle.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if id, parseErr := strconv.ParseInt(event.ID, 10, 64); parseErr == nil {
				if id <= lastSentID {
					continue
				}
				lastSentID = id
			}
			if !visible(event) {
				continue
			}
			writeSSEEvent(w, event)
			flusher.Flush()
			if event.EventType == bus.VaultEventAccessChanged {
				refreshed, err := h.authz.Access(ctx, vaultID, claims.UserID, domain.PermFileRead)
				if err != nil {
					return
				}
				visible = eventFilter(refreshed.Scope, claims.UserID)
			}
		case <-pingTicker.C:
			if h.guard != nil && h.guard(ctx, vaultID, claims.UserID) != nil {
				return
			}
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

const sseReplayPageSize = 500

func (h *SSEHandler) replayEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, vaultID uuid.UUID, sinceID int64, scope domain.Scope) int64 {
	lastID := sinceID
	for {
		events, err := h.events.ListScoped(ctx, vaultID, lastID, sseReplayPageSize, scope)
		if err != nil {
			return lastID
		}

		for i := range events {
			data, err := events[i].PayloadJSON()
			if err != nil {
				continue
			}
			writeSSEEvent(w, Event{
				ID:        strconv.FormatInt(events[i].ID, 10),
				EventType: string(events[i].EventType),
				Data:      string(data),
			})
			lastID = events[i].ID
		}
		flusher.Flush()

		if len(events) < sseReplayPageSize {
			return lastID
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, event Event) {
	if event.ID != "" {
		fmt.Fprintf(w, "id: %s\n", event.ID)
	}
	if event.EventType != "" {
		fmt.Fprintf(w, "event: %s\n", event.EventType)
	}
	fmt.Fprintf(w, "data: %s\n\n", event.Data)
}

func eventFilter(scope domain.Scope, userID uuid.UUID) func(Event) bool {
	if scope.All {
		return func(Event) bool { return true }
	}
	self := userID.String()
	member := scope.Base >= domain.AccessView
	return func(event Event) bool {
		if event.EventType == bus.VaultEventAccessChanged {
			return true
		}
		var payload struct {
			FilePath string `json:"file_path"`
			OldPath  string `json:"old_path"`
			UserID   string `json:"user_id"`
		}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			return false
		}
		if payload.FilePath != "" {
			return scope.CanRead(payload.FilePath) || (payload.OldPath != "" && scope.CanRead(payload.OldPath))
		}
		return member || payload.UserID == self
	}
}
