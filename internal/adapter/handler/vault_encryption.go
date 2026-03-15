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

type VaultEncryptionHandler struct {
	encryptions *usecase.VaultEncryptionUsecase
}

func NewVaultEncryptionHandler(encryptions *usecase.VaultEncryptionUsecase) *VaultEncryptionHandler {
	return &VaultEncryptionHandler{encryptions: encryptions}
}

type vaultEncryptionResponse struct {
	HasKey       bool   `json:"has_key"`
	Salt         string `json:"salt,omitempty"`
	EncryptedVEK string `json:"encrypted_vek,omitempty"`
}

type createVaultEncryptionRequest struct {
	Salt         string `json:"salt"`
	EncryptedVEK string `json:"encrypted_vek"`
}

func (h *VaultEncryptionHandler) Get(w http.ResponseWriter, r *http.Request) {
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

	info, err := h.encryptions.Get(r.Context(), claims.UserID, vaultID)
	if err != nil {
		switch err {
		case domain.ErrVaultAccessDenied:
			WriteError(w, http.StatusForbidden, "vault access denied")
		default:
			WriteError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	resp := vaultEncryptionResponse{HasKey: info.HasKey}
	if info.HasKey {
		resp.Salt = base64.StdEncoding.EncodeToString(info.Salt)
		resp.EncryptedVEK = base64.StdEncoding.EncodeToString(info.EncryptedVEK)
	}

	WriteJSON(w, http.StatusOK, resp)
}

func (h *VaultEncryptionHandler) Create(w http.ResponseWriter, r *http.Request) {
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

	var req createVaultEncryptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	salt, err := base64.StdEncoding.DecodeString(req.Salt)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid salt: must be base64 encoded")
		return
	}

	encryptedVEK, err := base64.StdEncoding.DecodeString(req.EncryptedVEK)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid encrypted_vek: must be base64 encoded")
		return
	}

	err = h.encryptions.Create(r.Context(), usecase.CreateVaultEncryptionInput{
		ActorID:      claims.UserID,
		VaultID:      vaultID,
		Salt:         salt,
		EncryptedVEK: encryptedVEK,
	})
	if err != nil {
		switch err {
		case domain.ErrInvalidInput:
			WriteError(w, http.StatusBadRequest, "salt and encrypted_vek are required")
		case domain.ErrVaultAccessDenied:
			WriteError(w, http.StatusForbidden, "vault access denied")
		default:
			WriteError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}
