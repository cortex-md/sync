package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type EncryptionHandler struct {
	encryptions *EncryptionService
}

func NewEncryptionHandler(encryptions *EncryptionService) *EncryptionHandler {
	return &EncryptionHandler{encryptions: encryptions}
}

type vaultEncryptionResponse struct {
	HasKey        bool   `json:"has_key"`
	HasMemberKeys bool   `json:"has_member_keys"`
	Salt          string `json:"salt,omitempty"`
	EncryptedVEK  string `json:"encrypted_vek,omitempty"`
}

type createVaultEncryptionRequest struct {
	Salt         string `json:"salt"`
	EncryptedVEK string `json:"encrypted_vek"`
}

func (h *EncryptionHandler) Get(w http.ResponseWriter, r *http.Request) {
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

	info, err := h.encryptions.Get(r.Context(), claims.UserID, vaultID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrVaultAccessDenied):
			httpx.WriteError(w, http.StatusForbidden, "vault access denied")
		case errors.Is(err, domain.ErrInsufficientRole):
			httpx.WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
		default:
			httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	resp := vaultEncryptionResponse{HasKey: info.HasKey, HasMemberKeys: info.HasMemberKeys}
	if info.HasKey {
		resp.Salt = base64.StdEncoding.EncodeToString(info.Salt)
		resp.EncryptedVEK = base64.StdEncoding.EncodeToString(info.EncryptedVEK)
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *EncryptionHandler) Create(w http.ResponseWriter, r *http.Request) {
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

	var req createVaultEncryptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	salt, err := base64.StdEncoding.DecodeString(req.Salt)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid salt: must be base64 encoded")
		return
	}

	encryptedVEK, err := base64.StdEncoding.DecodeString(req.EncryptedVEK)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid encrypted_vek: must be base64 encoded")
		return
	}

	err = h.encryptions.Create(r.Context(), CreateVaultEncryptionInput{
		ActorID:      claims.UserID,
		VaultID:      vaultID,
		Salt:         salt,
		EncryptedVEK: encryptedVEK,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			httpx.WriteError(w, http.StatusBadRequest, "salt and encrypted_vek are required")
		case errors.Is(err, domain.ErrVaultAccessDenied):
			httpx.WriteError(w, http.StatusForbidden, "vault access denied")
		case errors.Is(err, domain.ErrInsufficientRole):
			httpx.WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
		default:
			httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}
