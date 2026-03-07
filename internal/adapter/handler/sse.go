package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/middleware"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type SSEHandler struct {
	broker  port.SSEBroker
	events  port.SyncEventRepository
	members port.VaultMemberRepository
	ping    time.Duration
}

func NewSSEHandler(broker port.SSEBroker, events port.SyncEventRepository, members port.VaultMemberRepository) *SSEHandler {
	return &SSEHandler{
		broker:  broker,
		events:  events,
		members: members,
		ping:    30 * time.Second,
	}
}

func NewSSEHandlerWithPing(broker port.SSEBroker, events port.SyncEventRepository, members port.VaultMemberRepository, ping time.Duration) *SSEHandler {
	return &SSEHandler{
		broker:  broker,
		events:  events,
		members: members,
		ping:    ping,
	}
}

func (h *SSEHandler) Events(w http.ResponseWriter, r *http.Request) {
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

	_, err = h.members.GetByVaultAndUser(r.Context(), vaultID, claims.UserID)
	if err != nil {
		WriteError(w, http.StatusForbidden, "vault access denied")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if lastEventID := r.Header.Get("Last-Event-ID"); lastEventID != "" {
		h.replayEvents(w, flusher, vaultID, lastEventID)
	}

	clientID := fmt.Sprintf("%s:%s:%s", claims.UserID, GetDeviceID(r.Context()), uuid.New().String())
	ch, err := h.broker.Subscribe(r.Context(), vaultID, clientID)
	if err != nil {
		return
	}
	middleware.SSEConnectionOpened()
	defer func() {
		h.broker.Unsubscribe(vaultID, clientID)
		middleware.SSEConnectionClosed()
	}()

	pingTicker := time.NewTicker(h.ping)
	defer pingTicker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeSSEEvent(w, event)
			flusher.Flush()
		case <-pingTicker.C:
			fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

func (h *SSEHandler) replayEvents(w http.ResponseWriter, flusher http.Flusher, vaultID uuid.UUID, lastEventID string) {
	sinceID, err := strconv.ParseInt(lastEventID, 10, 64)
	if err != nil {
		return
	}

	events, err := h.events.ListByVaultID(context.Background(), vaultID, sinceID, 1000)
	if err != nil {
		return
	}

	for _, e := range events {
		data := map[string]any{
			"vault_uuid": e.VaultID.String(),
			"file_path":  e.FilePath,
			"version":    e.Version,
			"actor_id":   e.ActorID.String(),
			"device_id":  e.DeviceID.String(),
		}
		for k, v := range e.Metadata {
			data[k] = v
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			continue
		}

		writeSSEEvent(w, port.SSEEvent{
			ID:        strconv.FormatInt(e.ID, 10),
			EventType: string(e.EventType),
			Data:      string(jsonData),
		})
	}
	flusher.Flush()
}

func writeSSEEvent(w http.ResponseWriter, event port.SSEEvent) {
	if event.ID != "" {
		fmt.Fprintf(w, "id: %s\n", event.ID)
	}
	if event.EventType != "" {
		fmt.Fprintf(w, "event: %s\n", event.EventType)
	}
	fmt.Fprintf(w, "data: %s\n\n", event.Data)
}
