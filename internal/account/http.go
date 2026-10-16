package account

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/pages"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	accounts *Service
}

func NewHandler(accounts *Service) *Handler {
	return &Handler{accounts: accounts}
}

type AccountResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	Locale        string `json:"locale"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	EmailVerified bool   `json:"email_verified"`
	HasPublicKey  bool   `json:"has_public_key"`
	InstanceAdmin bool   `json:"is_instance_admin"`
	CreatedAt     string `json:"created_at"`
}

func ToAccountResponse(user *domain.User) AccountResponse {
	return AccountResponse{
		ID:            user.ID.String(),
		Email:         user.Email,
		DisplayName:   user.DisplayName,
		Locale:        user.Locale,
		AvatarURL:     user.AvatarURL(),
		EmailVerified: user.EmailVerifiedAt != nil,
		HasPublicKey:  len(user.PublicKey) > 0,
		InstanceAdmin: user.IsInstanceAdmin,
		CreatedAt:     user.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, err := h.accounts.Me(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID)
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToAccountResponse(user))
}

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Locale      *string `json:"locale"`
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req updateProfileRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	user, err := h.accounts.UpdateProfile(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, UpdateProfileInput{
		DisplayName: req.DisplayName,
		Locale:      req.Locale,
	})
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToAccountResponse(user))
}

type changePasswordRequest struct {
	CurrentPassword     string  `json:"current_password"`
	NewPassword         string  `json:"new_password"`
	KDFSalt             *string `json:"kdf_salt"`
	EncryptedPrivateKey *string `json:"encrypted_private_key"`
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	salt, saltErr := httpx.DecodeOptionalBase64(req.KDFSalt)
	encrypted, encErr := httpx.DecodeOptionalBase64(req.EncryptedPrivateKey)
	if saltErr != nil || encErr != nil {
		httpx.WriteError(w, http.StatusBadRequest, "key fields must be base64 encoded")
		return
	}
	claims := httpx.ClaimsFromContext(r.Context())
	if err := h.accounts.ChangePassword(r.Context(), ChangePasswordInput{
		UserID:              claims.UserID,
		DeviceID:            claims.DeviceID,
		CurrentPassword:     req.CurrentPassword,
		NewPassword:         req.NewPassword,
		KDFSalt:             salt,
		EncryptedPrivateKey: encrypted,
	}); err != nil {
		handleAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type requestEmailChangeRequest struct {
	Password string `json:"password"`
	NewEmail string `json:"new_email"`
}

func (h *Handler) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	var req requestEmailChangeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	applied, err := h.accounts.RequestEmailChange(r.Context(), RequestEmailChangeInput{
		UserID:   httpx.ClaimsFromContext(r.Context()).UserID,
		Password: req.Password,
		NewEmail: req.NewEmail,
	})
	if err != nil {
		handleAccountError(w, err)
		return
	}
	if applied {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "changed"})
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "confirmation_sent"})
}

func (h *Handler) SendEmailVerification(w http.ResponseWriter, r *http.Request) {
	if err := h.accounts.SendEmailVerification(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID); err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "verification_sent"})
}

func (h *Handler) SetAvatar(w http.ResponseWriter, r *http.Request) {
	user, err := h.accounts.SetAvatar(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, r.Body)
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToAccountResponse(user))
}

func (h *Handler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	if err := h.accounts.DeleteAvatar(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID); err != nil {
		handleAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	file := chi.URLParam(r, "file")
	if match := r.Header.Get("If-None-Match"); match != "" && match == avatarETag(file) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	reader, err := h.accounts.OpenAvatar(r.Context(), userID, file)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		handleAccountError(w, err)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", avatarETag(file))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func avatarETag(file string) string {
	return `"` + strings.TrimSuffix(file, ".jpg") + `"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if isFormPost(r) {
		email := strings.TrimSpace(r.PostFormValue("email"))
		if err := h.accounts.ForgotPassword(r.Context(), email); err != nil {
			log.Error().Err(err).Msg("forgot password failed")
		}
		locale := pages.Locale(r)
		pages.Render(w, http.StatusAccepted, pages.Data{
			Lang:    locale,
			Icon:    "mail",
			Tone:    "ok",
			Title:   pages.Text(locale, "sent.title"),
			Message: pages.Textf(locale, "sent.message", email),
			Fine:    pages.Text(locale, "sent.fine"),
		})
		return
	}
	var req forgotPasswordRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.accounts.ForgotPassword(r.Context(), req.Email); err != nil {
		log.Error().Err(err).Msg("forgot password failed")
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "reset_sent_if_account_exists"})
}

func isFormPost(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

func (h *Handler) VerifyEmailPage(w http.ResponseWriter, r *http.Request) {
	h.confirmPage(w, r, domain.UserTokenEmailVerify, "verify", "/account/v1/email/verify")
}

func (h *Handler) ConfirmEmailChangePage(w http.ResponseWriter, r *http.Request) {
	h.confirmPage(w, r, domain.UserTokenEmailChange, "change", "/account/v1/email/confirm-change")
}

func (h *Handler) confirmPage(w http.ResponseWriter, r *http.Request, kind domain.UserTokenKind, prefix string, action string) {
	token := r.URL.Query().Get("token")
	if err := h.accounts.TokenUsable(r.Context(), kind, token); err != nil {
		h.renderTokenFailure(w, r, err, "")
		return
	}
	locale := pages.Locale(r)
	pages.Render(w, http.StatusOK, pages.Data{
		Lang:    locale,
		Icon:    "mail",
		Title:   pages.Text(locale, prefix+".title"),
		Message: pages.Text(locale, prefix+".message"),
		Token:   token,
		Actions: []pages.Action{{Label: pages.Text(locale, prefix+".button"), Post: action, Style: "primary"}},
	})
}

type tokenRequest struct {
	Token                     string  `json:"token"`
	Password                  string  `json:"password"`
	KDFSalt                   *string `json:"kdf_salt"`
	PublicKey                 *string `json:"public_key"`
	EncryptedPrivateKey       *string `json:"encrypted_private_key"`
	RecoveryWrappedPrivateKey *string `json:"recovery_wrapped_private_key"`
}

func (req tokenRequest) keyMaterial() (*KeyMaterial, error) {
	if req.KDFSalt == nil && req.PublicKey == nil && req.EncryptedPrivateKey == nil && req.RecoveryWrappedPrivateKey == nil {
		return nil, nil
	}
	keys := &KeyMaterial{}
	for _, field := range []struct {
		src *string
		dst *[]byte
	}{
		{req.KDFSalt, &keys.KDFSalt},
		{req.PublicKey, &keys.PublicKey},
		{req.EncryptedPrivateKey, &keys.EncryptedPrivateKey},
		{req.RecoveryWrappedPrivateKey, &keys.RecoveryWrappedPrivateKey},
	} {
		decoded, err := httpx.DecodeOptionalBase64(field.src)
		if err != nil {
			return nil, domain.ErrInvalidInput
		}
		*field.dst = decoded
	}
	return keys, nil
}

func readTokenRequest(r *http.Request) (tokenRequest, bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req tokenRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		return req, true
	}
	if err := r.ParseForm(); err != nil {
		return tokenRequest{}, false
	}
	return tokenRequest{Token: r.PostForm.Get("token"), Password: r.PostForm.Get("password")}, false
}

func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	req, isJSON := readTokenRequest(r)
	user, err := h.accounts.VerifyEmail(r.Context(), req.Token)
	h.respondTokenAction(w, r, isJSON, err, "", func(locale string) pages.Data {
		return pages.Data{
			Icon:    "mail",
			Title:   pages.Text(locale, "verify.done.title"),
			Message: pages.Textf(locale, "verify.done", user.Email),
			Actions: []pages.Action{pages.OpenCortex(locale)},
		}
	})
}

func (h *Handler) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	req, isJSON := readTokenRequest(r)
	user, err := h.accounts.ConfirmEmailChange(r.Context(), req.Token)
	h.respondTokenAction(w, r, isJSON, err, "change.taken", func(locale string) pages.Data {
		return pages.Data{
			Icon:    "mail",
			Title:   pages.Text(locale, "change.done.title"),
			Message: pages.Textf(locale, "change.done", user.Email),
			Actions: []pages.Action{pages.OpenCortex(locale)},
		}
	})
}

func (h *Handler) ResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	preview, err := h.accounts.PrepareReset(r.Context(), token)
	if err != nil {
		h.renderTokenFailure(w, r, err, "/account/v1/password/forgot")
		return
	}
	locale := pages.Locale(r)
	pages.Render(w, http.StatusOK, pages.Data{
		Lang:    locale,
		Icon:    "lock",
		Title:   pages.Text(locale, "reset.title"),
		Detail:  pages.Textf(locale, "reset.for", preview.Email),
		Message: pages.Text(locale, "reset.message"),
		Actions: []pages.Action{{Label: pages.Text(locale, "reset.button"), URL: resetDeepLink(token, h.accounts.publicURL), Style: "primary"}},
		Fine:    pages.Text(locale, "reset.fine"),
	})
}

func resetDeepLink(token, server string) template.URL {
	return template.URL("cortex://reset-password?token=" + url.QueryEscape(token) + "&server=" + url.QueryEscape(server))
}

type resetPrepareRequest struct {
	Token string `json:"token"`
}

type resetPrepareResponse struct {
	PublicKey                 *string `json:"public_key"`
	RecoveryWrappedPrivateKey *string `json:"recovery_wrapped_private_key"`
}

func (h *Handler) PrepareResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPrepareRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	preview, err := h.accounts.PrepareReset(r.Context(), req.Token)
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resetPrepareResponse{
		PublicKey:                 httpx.EncodeBase64(preview.PublicKey),
		RecoveryWrappedPrivateKey: httpx.EncodeBase64(preview.RecoveryWrappedPrivateKey),
	})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	req, isJSON := readTokenRequest(r)
	keys, err := req.keyMaterial()
	if err == nil {
		err = h.accounts.ResetPassword(r.Context(), ResetPasswordInput{Token: req.Token, Password: req.Password, Keys: keys})
	}
	if errors.Is(err, domain.ErrInvalidInput) && !isJSON {
		pages.Notice(w, r, http.StatusBadRequest, "reset.invalid.title", "reset.invalid")
		return
	}
	h.respondTokenAction(w, r, isJSON, err, "", func(locale string) pages.Data {
		return pages.Data{
			Icon:    "shield",
			Tone:    "ok",
			Title:   pages.Text(locale, "reset.done.title"),
			Message: pages.Text(locale, "reset.done"),
			Actions: []pages.Action{pages.OpenCortex(locale)},
			Fine:    pages.Text(locale, "reset.done.fine"),
		}
	})
}

func (h *Handler) respondTokenAction(w http.ResponseWriter, r *http.Request, isJSON bool, err error, conflictKey string, done func(locale string) pages.Data) {
	if isJSON {
		if err != nil {
			handleAccountError(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err != nil {
		h.renderTokenFailure(w, r, err, conflictKey)
		return
	}
	locale := pages.Locale(r)
	data := done(locale)
	data.Lang = locale
	if data.Tone == "" {
		data.Tone = "ok"
	}
	pages.Render(w, http.StatusOK, data)
}

func (h *Handler) renderTokenFailure(w http.ResponseWriter, r *http.Request, err error, option string) {
	locale := pages.Locale(r)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		data := pages.Data{
			Lang:    locale,
			Icon:    "clock",
			Tone:    "warn",
			Title:   pages.Text(locale, "expired.title"),
			Message: pages.Text(locale, "expired.app"),
			Actions: []pages.Action{pages.OpenCortex(locale)},
		}
		if strings.HasPrefix(option, "/") {
			data.Message = pages.Text(locale, "expired.resend")
			data.Actions = nil
			data.EmailForm = &pages.EmailForm{Action: option, Label: pages.Text(locale, "expired.email"), Button: pages.Text(locale, "expired.button")}
		}
		pages.Render(w, http.StatusGone, data)
	case errors.Is(err, domain.ErrAlreadyExists) && option != "" && !strings.HasPrefix(option, "/"):
		pages.Render(w, http.StatusConflict, pages.Data{
			Lang:    locale,
			Icon:    "x",
			Tone:    "warn",
			Title:   pages.Text(locale, "change.title"),
			Message: pages.Text(locale, option),
		})
	default:
		log.Error().Err(err).Msg("account token action failed")
		pages.Error(w, r)
	}
}

type keysResponse struct {
	KDFVersion          int     `json:"kdf_version"`
	KDFSalt             *string `json:"kdf_salt"`
	PublicKey           *string `json:"public_key"`
	EncryptedPrivateKey *string `json:"encrypted_private_key"`
	HasRecovery         bool    `json:"has_recovery"`
}

func toKeysResponse(user *domain.User) keysResponse {
	return keysResponse{
		KDFVersion:          user.KDFVersion,
		KDFSalt:             httpx.EncodeBase64(user.KDFSalt),
		PublicKey:           httpx.EncodeBase64(user.PublicKey),
		EncryptedPrivateKey: httpx.EncodeBase64(user.EncryptedPrivateKey),
		HasRecovery:         len(user.RecoveryWrappedPrivateKey) > 0,
	}
}

func (h *Handler) GetKeys(w http.ResponseWriter, r *http.Request) {
	user, err := h.accounts.Me(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID)
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toKeysResponse(user))
}

type setupKeysRequest struct {
	CurrentPassword           string  `json:"current_password"`
	Password                  string  `json:"password"`
	KDFSalt                   *string `json:"kdf_salt"`
	PublicKey                 *string `json:"public_key"`
	EncryptedPrivateKey       *string `json:"encrypted_private_key"`
	RecoveryWrappedPrivateKey *string `json:"recovery_wrapped_private_key"`
}

func (h *Handler) SetupKeys(w http.ResponseWriter, r *http.Request) {
	var req setupKeysRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	keys, err := tokenRequest{
		KDFSalt: req.KDFSalt, PublicKey: req.PublicKey,
		EncryptedPrivateKey: req.EncryptedPrivateKey, RecoveryWrappedPrivateKey: req.RecoveryWrappedPrivateKey,
	}.keyMaterial()
	if err != nil || keys == nil {
		handleAccountError(w, domain.ErrInvalidInput)
		return
	}
	user, err := h.accounts.SetupKeys(r.Context(), SetupKeysInput{
		UserID:          httpx.ClaimsFromContext(r.Context()).UserID,
		CurrentPassword: req.CurrentPassword,
		Password:        req.Password,
		Keys:            *keys,
	})
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toKeysResponse(user))
}

type recoveryRequest struct {
	RecoveryWrappedPrivateKey string `json:"recovery_wrapped_private_key"`
}

func (h *Handler) SetRecoveryKey(w http.ResponseWriter, r *http.Request) {
	var req recoveryRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	wrapped, err := httpx.DecodeBase64(req.RecoveryWrappedPrivateKey)
	if err != nil {
		handleAccountError(w, domain.ErrInvalidInput)
		return
	}
	user, err := h.accounts.SetRecoveryKey(r.Context(), httpx.ClaimsFromContext(r.Context()).UserID, wrapped)
	if err != nil {
		handleAccountError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toKeysResponse(user))
}

func handleAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrKeysNotSetUp):
		httpx.WriteErrorWithCode(w, http.StatusConflict, "account keys are not set up", "keys_not_set_up")
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteErrorWithCode(w, http.StatusBadRequest, "invalid input", "invalid_input")
	case errors.Is(err, domain.ErrInvalidCredentials):
		httpx.WriteErrorWithCode(w, http.StatusUnauthorized, "invalid credentials", "invalid_credentials")
	case errors.Is(err, domain.ErrAlreadyExists):
		httpx.WriteErrorWithCode(w, http.StatusConflict, "email already in use", "email_taken")
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteErrorWithCode(w, http.StatusNotFound, "not found", "not_found")
	case errors.Is(err, domain.ErrFileTooLarge):
		httpx.WriteErrorWithCode(w, http.StatusRequestEntityTooLarge, "file too large", "file_too_large")
	default:
		if httpx.WriteDomainError(w, err) {
			return
		}
		log.Error().Err(err).Msg("account request failed")
		httpx.WriteInternalError(w)
	}
}
