package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
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
	db      *pgxpool.Pool
	storage port.BlobStorage
}

func NewReadinessHandler(db *pgxpool.Pool, storage port.BlobStorage) *ReadinessHandler {
	return &ReadinessHandler{db: db, storage: storage}
}

func (h *ReadinessHandler) Check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "database_unavailable"})
			return
		}
	}

	if h.storage != nil {
		if err := h.storage.Check(ctx); err != nil {
			WriteJSON(w, http.StatusServiceUnavailable, HealthResponse{Status: "storage_unavailable"})
			return
		}
	}

	WriteJSON(w, http.StatusOK, HealthResponse{Status: "ready"})
}

func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details any    `json:"details,omitempty"`
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, ErrorResponse{Error: message})
}

func WriteErrorWithCode(w http.ResponseWriter, status int, message string, code string) {
	WriteJSON(w, status, ErrorResponse{Error: message, Code: code})
}
