package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/validate"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	vaults *Service
}

func NewHandler(vaults *Service) *Handler {
	return &Handler{vaults: vaults}
}

type createVaultRequest struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Icon              string `json:"icon"`
	EncryptedVaultKey string `json:"encrypted_vault_key"`
}

type VaultResponse struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	Icon                string   `json:"icon"`
	OwnerID             string   `json:"owner_id"`
	Role                string   `json:"role"`
	MemberCount         int      `json:"member_count"`
	HasKey              bool     `json:"has_key"`
	AllowedEmailDomains []string `json:"allowed_email_domains"`
	AllowGuests         bool     `json:"allow_guests"`
	AllowPublicLinks    bool     `json:"allow_public_links"`
	StorageUsedBytes    int64    `json:"storage_used_bytes"`
	StorageQuotaBytes   *int64   `json:"storage_quota_bytes"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

func ToVaultResponse(v VaultInfo) VaultResponse {
	domains := v.AllowedEmailDomains
	if domains == nil {
		domains = []string{}
	}
	return VaultResponse{
		ID:                  v.ID.String(),
		Name:                v.Name,
		Description:         v.Description,
		Icon:                v.Icon,
		OwnerID:             v.OwnerID.String(),
		Role:                string(v.Role),
		MemberCount:         v.MemberCount,
		HasKey:              v.HasKey,
		AllowedEmailDomains: domains,
		AllowGuests:         v.AllowGuests,
		AllowPublicLinks:    v.AllowPublicLinks,
		StorageUsedBytes:    v.StorageUsedBytes,
		StorageQuotaBytes:   v.StorageQuotaBytes,
		CreatedAt:           v.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:           v.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(req.EncryptedVaultKey)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid encrypted_vault_key: must be base64 encoded")
		return
	}

	if !validate.NonEmpty(req.Name) {
		httpx.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}

	var clientID uuid.UUID
	if req.ID != "" {
		if clientID, err = uuid.Parse(req.ID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
	}

	vault, err := h.vaults.Create(r.Context(), CreateVaultInput{
		ID:                clientID,
		Name:              req.Name,
		Description:       req.Description,
		Icon:              req.Icon,
		UserID:            claims.UserID,
		EncryptedVaultKey: encryptedKey,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, ToVaultResponse(*vault))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vaults, err := h.vaults.List(r.Context(), claims.UserID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list vaults")
		return
	}

	resp := make([]VaultResponse, 0, len(vaults))
	for _, v := range vaults {
		resp = append(resp, ToVaultResponse(v))
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
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

	vault, err := h.vaults.Get(r.Context(), claims.UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToVaultResponse(*vault))
}

type updateVaultRequest struct {
	Name                string          `json:"name"`
	Description         *string         `json:"description"`
	Icon                *string         `json:"icon"`
	AllowedEmailDomains *[]string       `json:"allowed_email_domains"`
	AllowGuests         *bool           `json:"allow_guests"`
	AllowPublicLinks    *bool           `json:"allow_public_links"`
	StorageQuotaBytes   json.RawMessage `json:"storage_quota_bytes"`
}

func (req updateVaultRequest) quota() (**int64, error) {
	if len(req.StorageQuotaBytes) == 0 {
		return nil, nil
	}
	var value *int64
	if err := json.Unmarshal(req.StorageQuotaBytes, &value); err != nil {
		return nil, err
	}
	if value != nil && *value < 0 {
		return nil, domain.ErrInvalidInput
	}
	return &value, nil
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
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

	var req updateVaultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	quota, err := req.quota()
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid storage_quota_bytes")
		return
	}

	vault, err := h.vaults.Update(r.Context(), UpdateVaultInput{
		UserID:              claims.UserID,
		VaultID:             vaultID,
		Name:                req.Name,
		Description:         req.Description,
		Icon:                req.Icon,
		AllowedEmailDomains: req.AllowedEmailDomains,
		AllowGuests:         req.AllowGuests,
		AllowPublicLinks:    req.AllowPublicLinks,
		StorageQuotaBytes:   quota,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToVaultResponse(*vault))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
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

	if err := h.vaults.Delete(r.Context(), claims.UserID, vaultID); err != nil {
		handleVaultError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleVaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrVaultAccessDenied):
		httpx.WriteError(w, http.StatusForbidden, "vault access denied")
	case errors.Is(err, domain.ErrInsufficientRole):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "insufficient permissions", "insufficient_role")
	case errors.Is(err, domain.ErrGroupNameTaken):
		httpx.WriteErrorWithCode(w, http.StatusConflict, "a group with this name already exists", "group_name_taken")
	case errors.Is(err, domain.ErrAlreadyExists):
		httpx.WriteError(w, http.StatusConflict, "already exists")
	case errors.Is(err, domain.ErrInviteExpired):
		httpx.WriteErrorWithCode(w, http.StatusGone, "invite has expired", "invite_expired")
	case errors.Is(err, domain.ErrFeatureDisabled):
		httpx.WriteErrorWithCode(w, http.StatusNotFound, "feature disabled on this server", "feature_disabled")
	case errors.Is(err, domain.ErrDomainNotAllowed):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "email domain is not allowed in this vault", "domain_not_allowed")
	default:
		log.Error().Err(err).Msg("vault request failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
