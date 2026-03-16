package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/adapter/validate"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type VaultHandler struct {
	vaults *usecase.VaultUsecase
}

func NewVaultHandler(vaults *usecase.VaultUsecase) *VaultHandler {
	return &VaultHandler{vaults: vaults}
}

type createVaultRequest struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	EncryptedVaultKey string `json:"encrypted_vault_key"`
}

type vaultResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"`
	Role        string `json:"role"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toVaultResponse(v usecase.VaultInfo) vaultResponse {
	return vaultResponse{
		ID:          v.ID.String(),
		Name:        v.Name,
		Description: v.Description,
		OwnerID:     v.OwnerID.String(),
		Role:        string(v.Role),
		MemberCount: v.MemberCount,
		CreatedAt:   v.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   v.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func (h *VaultHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(req.EncryptedVaultKey)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid encrypted_vault_key: must be base64 encoded")
		return
	}

	if !validate.NonEmpty(req.Name) {
		WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	vault, err := h.vaults.Create(r.Context(), usecase.CreateVaultInput{
		Name:              req.Name,
		Description:       req.Description,
		UserID:            claims.UserID,
		EncryptedVaultKey: encryptedKey,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusCreated, toVaultResponse(*vault))
}

func (h *VaultHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaults, err := h.vaults.List(r.Context(), claims.UserID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to list vaults")
		return
	}

	resp := make([]vaultResponse, 0, len(vaults))
	for _, v := range vaults {
		resp = append(resp, toVaultResponse(v))
	}

	WriteJSON(w, http.StatusOK, resp)
}

func (h *VaultHandler) Get(w http.ResponseWriter, r *http.Request) {
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

	vault, err := h.vaults.Get(r.Context(), claims.UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, toVaultResponse(*vault))
}

type updateVaultRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

func (h *VaultHandler) Update(w http.ResponseWriter, r *http.Request) {
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

	var req updateVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	vault, err := h.vaults.Update(r.Context(), usecase.UpdateVaultInput{
		UserID:      claims.UserID,
		VaultID:     vaultID,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, toVaultResponse(*vault))
}

func (h *VaultHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	if err := h.vaults.Delete(r.Context(), claims.UserID, vaultID); err != nil {
		handleVaultError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleVaultError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrInvalidInput:
		WriteError(w, http.StatusBadRequest, err.Error())
	case domain.ErrNotFound:
		WriteError(w, http.StatusNotFound, "vault not found")
	case domain.ErrVaultAccessDenied:
		WriteError(w, http.StatusForbidden, "vault access denied")
	case domain.ErrInsufficientRole:
		WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
	case domain.ErrAlreadyExists:
		WriteError(w, http.StatusConflict, "already exists")
	case domain.ErrInviteExpired:
		WriteErrorWithCode(w, http.StatusGone, "invite has expired", "invite_expired")
	default:
		WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
