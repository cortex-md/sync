package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/pages"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type InviteHandler struct {
	invites *InviteService
}

func NewInviteHandler(invites *InviteService) *InviteHandler {
	return &InviteHandler{invites: invites}
}

type GrantPayload struct {
	Path     string `json:"path"`
	CanWrite bool   `json:"can_write"`
}

type createInviteRequest struct {
	ID                string         `json:"id"`
	InviteeEmail      string         `json:"invitee_email"`
	Role              string         `json:"role"`
	Grants            []GrantPayload `json:"grants"`
	EncryptedVaultKey string         `json:"encrypted_vault_key"`
}

type InviteResponse struct {
	ID                string         `json:"id"`
	VaultID           string         `json:"vault_id"`
	VaultName         string         `json:"vault_name"`
	InviterID         string         `json:"inviter_id"`
	InviterName       string         `json:"inviter_name,omitempty"`
	InviteeEmail      string         `json:"invitee_email"`
	Grants            []GrantPayload `json:"grants,omitempty"`
	Role              string         `json:"role"`
	EncryptedVaultKey string         `json:"encrypted_vault_key,omitempty"`
	Accepted          bool           `json:"accepted"`
	InviteToken       string         `json:"invite_token,omitempty"`
	InviteURL         string         `json:"invite_url,omitempty"`
	ExpiresAt         string         `json:"expires_at"`
	CreatedAt         string         `json:"created_at"`
}

func ToInviteResponse(inv InviteInfo) InviteResponse {
	resp := InviteResponse{
		ID:           inv.ID.String(),
		VaultID:      inv.VaultID.String(),
		VaultName:    inv.VaultName,
		InviterID:    inv.InviterID.String(),
		InviterName:  inv.InviterName,
		InviteeEmail: inv.InviteeEmail,
		Role:         string(inv.Role),
		Accepted:     inv.Accepted,
		InviteToken:  inv.Token,
		InviteURL:    inv.URL,
		ExpiresAt:    inv.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:    inv.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if len(inv.EncryptedVaultKey) > 0 {
		resp.EncryptedVaultKey = base64.StdEncoding.EncodeToString(inv.EncryptedVaultKey)
	}
	for _, g := range inv.Grants {
		resp.Grants = append(resp.Grants, GrantPayload{Path: g.Path, CanWrite: g.CanWrite})
	}
	return resp
}

func (h *InviteHandler) Create(w http.ResponseWriter, r *http.Request) {
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

	var req createInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role := domain.VaultRole(req.Role)
	if !role.Valid() || role == domain.VaultRoleOwner {
		httpx.WriteError(w, http.StatusBadRequest, "invalid role: must be admin, editor, viewer, or guest")
		return
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(req.EncryptedVaultKey)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid encrypted_vault_key: must be base64 encoded")
		return
	}

	var clientID uuid.UUID
	if req.ID != "" {
		if clientID, err = uuid.Parse(req.ID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
	}

	invite, err := h.invites.Create(r.Context(), CreateInviteInput{
		ID:                clientID,
		ActorID:           claims.UserID,
		VaultID:           vaultID,
		InviteeEmail:      req.InviteeEmail,
		Role:              role,
		Grants:            toGrantInputs(req.Grants),
		EncryptedVaultKey: encryptedKey,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, ToInviteResponse(*invite))
}

func (h *InviteHandler) ListByVault(w http.ResponseWriter, r *http.Request) {
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

	invites, err := h.invites.ListByVault(r.Context(), claims.UserID, vaultID)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	resp := make([]InviteResponse, 0, len(invites))
	for _, inv := range invites {
		resp = append(resp, ToInviteResponse(inv))
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *InviteHandler) ListMyInvites(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	invites, err := h.invites.ListMyInvites(r.Context(), claims.Email)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list invites")
		return
	}

	resp := make([]InviteResponse, 0, len(invites))
	for _, inv := range invites {
		resp = append(resp, ToInviteResponse(inv))
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

type acceptInviteRequest struct {
	InviteID string `json:"invite_id"`
	Token    string `json:"token"`
}

type acceptInviteResponse struct {
	VaultResponse
	EncryptedVaultKey string `json:"encrypted_vault_key,omitempty"`
}

func (h *InviteHandler) Accept(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req acceptInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	input := AcceptInviteInput{UserID: claims.UserID, Token: req.Token}
	if req.Token == "" {
		inviteID, err := uuid.Parse(req.InviteID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid invite_id")
			return
		}
		input.InviteID = inviteID
	}

	accepted, err := h.invites.Accept(r.Context(), input)
	if err != nil {
		handleVaultError(w, err)
		return
	}

	resp := acceptInviteResponse{VaultResponse: ToVaultResponse(*accepted.Vault)}
	if len(accepted.EncryptedVaultKey) > 0 {
		resp.EncryptedVaultKey = base64.StdEncoding.EncodeToString(accepted.EncryptedVaultKey)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type invitePreviewResponse struct {
	VaultName   string `json:"vault_name"`
	InviterName string `json:"inviter_name"`
	Role        string `json:"role"`
	ExpiresAt   string `json:"expires_at"`
}

func (h *InviteHandler) Preview(w http.ResponseWriter, r *http.Request) {
	preview, err := h.invites.Preview(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		handleVaultError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, invitePreviewResponse{
		VaultName:   preview.VaultName,
		InviterName: preview.InviterName,
		Role:        string(preview.Role),
		ExpiresAt:   preview.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (h *InviteHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid invite id")
		return
	}

	err = h.invites.Delete(r.Context(), DeleteInviteInput{
		ActorID:  claims.UserID,
		VaultID:  vaultID,
		InviteID: inviteID,
	})
	if err != nil {
		handleVaultError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *InviteHandler) InvitePage(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	locale := pages.Locale(r)
	preview, err := h.invites.Preview(r.Context(), token)
	if err != nil {
		renderInviteFailure(w, r, err)
		return
	}

	role := pages.Role(locale, string(preview.Role))
	pages.Render(w, http.StatusOK, pages.Data{
		Lang:  locale,
		Icon:  "users",
		Title: pages.Textf(locale, "invite.title", preview.InviterName),
		Token: token,
		Vault: &pages.Vault{
			Name:   preview.VaultName,
			Detail: pages.Textf(locale, pluralKey("invite.member", preview.MemberCount), preview.MemberCount, role),
		},
		Actions: []pages.Action{
			{Label: pages.Text(locale, "invite.accept"), URL: inviteDeepLink(token), Style: "primary", KeepHash: true},
			{Label: pages.Text(locale, "invite.decline"), Post: "/invite/" + url.PathEscape(token) + "/decline", Style: "quiet"},
		},
		Fine: pages.Textf(locale, "invite.fine", pages.Date(locale, preview.ExpiresAt)),
	})
}

func (h *InviteHandler) DeclinePage(w http.ResponseWriter, r *http.Request) {
	locale := pages.Locale(r)
	declined, err := h.invites.Decline(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		renderInviteFailure(w, r, err)
		return
	}
	pages.Render(w, http.StatusOK, pages.Data{
		Lang:    locale,
		Icon:    "x",
		Tone:    "warn",
		Title:   pages.Text(locale, "invite.declined"),
		Message: pages.Textf(locale, "invite.declined.msg", declined.InviterName),
	})
}

func renderInviteFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInviteExpired):
		pages.Notice(w, r, http.StatusGone, "invite.unusable", "invite.expired")
	case errors.Is(err, domain.ErrAlreadyExists):
		pages.Notice(w, r, http.StatusGone, "invite.unusable", "invite.used")
	case errors.Is(err, domain.ErrNotFound):
		pages.Notice(w, r, http.StatusNotFound, "invite.unusable", "invite.invalid")
	default:
		log.Error().Err(err).Msg("invite page failed")
		pages.Error(w, r)
	}
}

func pluralKey(key string, count int) string {
	if count == 1 {
		return key
	}
	return key + "s"
}

func inviteDeepLink(token string) template.URL {
	return template.URL("cortex://invite/" + url.PathEscape(token))
}

func toGrantInputs(payload []GrantPayload) []GrantInput {
	inputs := make([]GrantInput, 0, len(payload))
	for _, g := range payload {
		inputs = append(inputs, GrantInput{Path: g.Path, CanWrite: g.CanWrite})
	}
	return inputs
}
