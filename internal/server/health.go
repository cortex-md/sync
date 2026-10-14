package server

import (
	"context"
	"encoding/json"
	"net/http"
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
}

func NewReadinessHandler(db *pgxpool.Pool, storage BlobStorage, lifecycle *lifecycle.Lifecycle) *ReadinessHandler {
	return &ReadinessHandler{db: db, storage: storage, lifecycle: lifecycle}
}

func (h *ReadinessHandler) Check(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.lifecycle.Draining() {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "draining"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "database_unavailable"})
			return
		}
	}

	if h.storage != nil {
		if err := h.storage.Check(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "storage_unavailable"})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, HealthResponse{Status: "ready"})
}
