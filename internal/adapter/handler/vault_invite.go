package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type VaultInviteHandler struct {
	invites *usecase.VaultInviteUsecase
}

func NewVaultInviteHandler(invites *usecase.VaultInviteUsecase) *VaultInviteHandler {
	return &VaultInviteHandler{invites: invites}
}

type createInviteRequest struct {
	InviteeEmail      string `json:"invitee_email"`
	Role              string `json:"role"`
	EncryptedVaultKey string `json:"encrypted_vault_key"`
}

type inviteResponse struct {
	ID                string `json:"id"`
	VaultID           string `json:"vault_id"`
	VaultName         string `json:"vault_name"`
	InviterID         string `json:"inviter_id"`
	InviteeEmail      string `json:"invitee_email"`
	Role              string `json:"role"`
	EncryptedVaultKey string `json:"encrypted_vault_key,omitempty"`
	Accepted          bool   `json:"accepted"`
	ExpiresAt         string `json:"expires_at"`
	CreatedAt         string `json:"created_at"`
}

func toInviteResponse(inv usecase.InviteInfo) inviteResponse {
	resp := inviteResponse{
		ID:           inv.ID.String(),
		VaultID:      inv.VaultID.String(),
		VaultName:    inv.VaultName,
		InviterID:    inv.InviterID.String(),
		InviteeEmail: inv.InviteeEmail,
		Role:         string(inv.Role),
		Accepted:     inv.Accepted,
		ExpiresAt:    inv.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:    inv.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if len(inv.EncryptedVaultKey) > 0 {
		resp.EncryptedVaultKey = base64.StdEncoding.EncodeToString(inv.EncryptedVaultKey)
	}
	return resp
}

func (h *VaultInviteHandler) Create(w http.ResponseWriter, r *http.Request) {
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

	var req createInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role := domain.VaultRole(req.Role)
	if role != domain.VaultRoleAdmin && role != domain.VaultRoleEditor && role != domain.VaultRoleViewer {
		WriteError(w, http.StatusBadRequest, "invalid role: must be admin, editor, or viewer")
		return
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(req.EncryptedVaultKey)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid encrypted_vault_key: must be base64 encoded")
		return
	}

	invite, err := h.invites.Create(r.Context(), usecase.CreateInviteInput{
		ActorID:           claims.UserID,
		VaultID:           vaultID,
		InviteeEmail:      req.InviteeEmail,
		Role:              role,
		EncryptedVaultKey: encryptedKey,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusCreated, toInviteResponse(*invite))
}

func (h *VaultInviteHandler) ListByVault(w http.ResponseWriter, r *http.Request) {
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

	invites, err := h.invites.ListByVault(r.Context(), claims.UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	resp := make([]inviteResponse, 0, len(invites))
	for _, inv := range invites {
		resp = append(resp, toInviteResponse(inv))
	}

	WriteJSON(w, http.StatusOK, resp)
}

func (h *VaultInviteHandler) ListMyInvites(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	invites, err := h.invites.ListMyInvites(r.Context(), claims.Email)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to list invites")
		return
	}

	resp := make([]inviteResponse, 0, len(invites))
	for _, inv := range invites {
		resp = append(resp, toInviteResponse(inv))
	}

	WriteJSON(w, http.StatusOK, resp)
}

type acceptInviteRequest struct {
	InviteID string `json:"invite_id"`
}

func (h *VaultInviteHandler) Accept(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req acceptInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	inviteID, err := uuid.Parse(req.InviteID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid invite_id")
		return
	}

	vault, err := h.invites.Accept(r.Context(), usecase.AcceptInviteInput{
		UserID:   claims.UserID,
		InviteID: inviteID,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, toVaultResponse(*vault))
}

func (h *VaultInviteHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid invite id")
		return
	}

	err = h.invites.Delete(r.Context(), usecase.DeleteInviteInput{
		ActorID:  claims.UserID,
		VaultID:  vaultID,
		InviteID: inviteID,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
