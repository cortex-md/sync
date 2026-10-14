package vault

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type MemberHandler struct {
	members *MemberService
}

func NewMemberHandler(members *MemberService) *MemberHandler {
	return &MemberHandler{members: members}
}

type MemberResponse struct {
	VaultID     string `json:"vault_id"`
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	HasKey      bool   `json:"has_key"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joined_at"`
}

func ToMemberResponse(m MemberInfo) MemberResponse {
	publicKey := ""
	if len(m.PublicKey) > 0 {
		publicKey = base64.StdEncoding.EncodeToString(m.PublicKey)
	}
	return MemberResponse{
		PublicKey:   publicKey,
		VaultID:     m.VaultID.String(),
		UserID:      m.UserID.String(),
		Email:       m.Email,
		DisplayName: m.DisplayName,
		AvatarURL:   m.AvatarURL,
		HasKey:      m.HasKey,
		Role:        string(m.Role),
		JoinedAt:    m.JoinedAt,
	}
}

func (h *MemberHandler) List(w http.ResponseWriter, r *http.Request) {
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

	members, err := h.members.List(r.Context(), claims.UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	resp := make([]MemberResponse, 0, len(members))
	for _, m := range members {
		resp = append(resp, ToMemberResponse(m))
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

func (h *MemberHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
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

	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req updateMemberRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role := domain.VaultRole(req.Role)
	if role != domain.VaultRoleAdmin && role != domain.VaultRoleEditor && role != domain.VaultRoleViewer {
		httpx.WriteError(w, http.StatusBadRequest, "invalid role: must be admin, editor, or viewer")
		return
	}

	err = h.members.UpdateRole(r.Context(), UpdateMemberRoleInput{
		ActorID: claims.UserID,
		VaultID: vaultID,
		UserID:  userID,
		Role:    role,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *MemberHandler) Remove(w http.ResponseWriter, r *http.Request) {
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

	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	err = h.members.Remove(r.Context(), RemoveMemberInput{
		ActorID: claims.UserID,
		VaultID: vaultID,
		UserID:  userID,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
