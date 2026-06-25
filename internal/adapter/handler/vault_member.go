package handler

import (
	"encoding/json"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type VaultMemberHandler struct {
	members *usecase.VaultMemberUsecase
}

func NewVaultMemberHandler(members *usecase.VaultMemberUsecase) *VaultMemberHandler {
	return &VaultMemberHandler{members: members}
}

type memberResponse struct {
	VaultID     string `json:"vault_id"`
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joined_at"`
}

func toMemberResponse(m usecase.MemberInfo) memberResponse {
	return memberResponse{
		VaultID:     m.VaultID.String(),
		UserID:      m.UserID.String(),
		Email:       m.Email,
		DisplayName: m.DisplayName,
		Role:        string(m.Role),
		JoinedAt:    m.JoinedAt,
	}
}

func (h *VaultMemberHandler) List(w http.ResponseWriter, r *http.Request) {
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

	members, err := h.members.List(r.Context(), claims.UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	resp := make([]memberResponse, 0, len(members))
	for _, m := range members {
		resp = append(resp, toMemberResponse(m))
	}

	WriteJSON(w, http.StatusOK, resp)
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

func (h *VaultMemberHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
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

	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req updateMemberRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role := domain.VaultRole(req.Role)
	if role != domain.VaultRoleAdmin && role != domain.VaultRoleEditor && role != domain.VaultRoleViewer {
		WriteError(w, http.StatusBadRequest, "invalid role: must be admin, editor, or viewer")
		return
	}

	err = h.members.UpdateRole(r.Context(), usecase.UpdateMemberRoleInput{
		ActorID: claims.UserID,
		VaultID: vaultID,
		UserID:  userID,
		Role:    role,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *VaultMemberHandler) Remove(w http.ResponseWriter, r *http.Request) {
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

	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.members.Remove(r.Context(), usecase.RemoveMemberInput{
		ActorID: claims.UserID,
		VaultID: vaultID,
		UserID:  userID,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
