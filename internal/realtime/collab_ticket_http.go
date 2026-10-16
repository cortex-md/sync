package realtime

import (
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

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
	if !httpx.DecodeJSON(w, r, &req) {
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
