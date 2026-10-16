package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/lifecycle"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthResponse struct {
	Status string `json:"status"`
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

type ReadinessHandler struct {
	db        *pgxpool.Pool
	storage   BlobStorage
	lifecycle *lifecycle.Lifecycle
	ttl       time.Duration

	mu        sync.Mutex
	checkedAt time.Time
	status    string
}

func NewReadinessHandler(db *pgxpool.Pool, storage BlobStorage, lifecycle *lifecycle.Lifecycle) *ReadinessHandler {
	return &ReadinessHandler{db: db, storage: storage, lifecycle: lifecycle, ttl: time.Minute}
}

func (h *ReadinessHandler) Check(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.lifecycle.Draining() {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "draining"})
		return
	}

	status := h.dependencies(r.Context())
	if status != "ready" {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: status})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, HealthResponse{Status: status})
}

func (h *ReadinessHandler) dependencies(ctx context.Context) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == "ready" && time.Since(h.checkedAt) < h.ttl {
		return h.status
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	h.status = "ready"
	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			h.status = "database_unavailable"
		}
	}
	if h.status == "ready" && h.storage != nil {
		if err := h.storage.Check(ctx); err != nil {
			h.status = "storage_unavailable"
		}
	}
	h.checkedAt = time.Now()
	return h.status
}
