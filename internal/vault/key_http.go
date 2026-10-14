package vault

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type KeyHandler struct {
	keys *KeyService
}

func NewKeyHandler(keys *KeyService) *KeyHandler {
	return &KeyHandler{keys: keys}
}

type vaultKeyRequest struct {
	EncryptedKey string `json:"encrypted_key"`
}

type vaultKeyResponse struct {
	VaultID      string `json:"vault_id"`
	UserID       string `json:"user_id"`
	EncryptedKey string `json:"encrypted_key"`
	UpdatedAt    string `json:"updated_at"`
}

type pendingKeyResponse struct {
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	PublicKey   string `json:"public_key,omitempty"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

func toVaultKeyResponse(info *VaultKeyInfo) vaultKeyResponse {
	return vaultKeyResponse{
		VaultID:      info.VaultID.String(),
		UserID:       info.UserID.String(),
		EncryptedKey: base64.StdEncoding.EncodeToString(info.EncryptedKey),
		UpdatedAt:    info.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *KeyHandler) GetMine(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	info, err := h.keys.Get(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toVaultKeyResponse(info))
}

func (h *KeyHandler) PutMine(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	key, ok := decodeVaultKey(w, r)
	if !ok {
		return
	}
	info, err := h.keys.Upsert(r.Context(), UpsertVaultKeyInput{
		ActorID:      httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID:      vaultID,
		EncryptedKey: key,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toVaultKeyResponse(info))
}

func (h *KeyHandler) Grant(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	key, ok := decodeVaultKey(w, r)
	if !ok {
		return
	}
	info, err := h.keys.Grant(r.Context(), GrantVaultKeyInput{
		ActorID:      httpx.ClaimsFromContext(r.Context()).UserID,
		VaultID:      vaultID,
		UserID:       userID,
		EncryptedKey: key,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toVaultKeyResponse(info))
}

func (h *KeyHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := vaultIDParam(w, r)
	if !ok {
		return
	}
	pending, err := h.keys.ListPending(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}
	resp := make([]pendingKeyResponse, 0, len(pending))
	for _, p := range pending {
		item := pendingKeyResponse{UserID: p.UserID.String(), Role: string(p.Role), DisplayName: p.DisplayName, Email: p.Email, AvatarURL: p.AvatarURL}
		if len(p.PublicKey) > 0 {
			item.PublicKey = base64.StdEncoding.EncodeToString(p.PublicKey)
		}
		resp = append(resp, item)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func vaultIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	vaultID, err := uuid.Parse(chi.URLParam(r, "vaultID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid vault id")
		return uuid.Nil, false
	}
	return vaultID, true
}

func decodeVaultKey(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	var req vaultKeyRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return nil, false
	}
	key, err := base64.StdEncoding.DecodeString(req.EncryptedKey)
	if err != nil || len(key) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "encrypted_key must be non-empty base64")
		return nil, false
	}
	return key, true
}

type publicKeyRequest struct {
	PublicKey string `json:"public_key"`
}

type publicKeyResponse struct {
	UserID    string `json:"user_id"`
	PublicKey string `json:"public_key"`
}

func (h *KeyHandler) SetPublicKey(w http.ResponseWriter, r *http.Request) {
	var req publicKeyRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	key, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "public_key must be base64 encoded")
		return
	}
	if err := h.keys.SetPublicKey(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, key); err != nil {
		handleVaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *KeyHandler) GetPublicKey(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	key, err := h.keys.GetPublicKey(r.Context(), userID)
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, publicKeyResponse{UserID: userID.String(), PublicKey: base64.StdEncoding.EncodeToString(key)})
}
