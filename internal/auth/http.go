package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/validate"
	"github.com/google/uuid"
)

type Handler struct {
	auth *Service
}

func NewHandler(auth *Service) *Handler {
	return &Handler{auth: auth}
}

type registerRequest struct {
	Email                     string  `json:"email"`
	Password                  string  `json:"password"`
	DisplayName               string  `json:"display_name"`
	Locale                    string  `json:"locale"`
	KDFSalt                   *string `json:"kdf_salt"`
	PublicKey                 *string `json:"public_key"`
	EncryptedPrivateKey       *string `json:"encrypted_private_key"`
	RecoveryWrappedPrivateKey *string `json:"recovery_wrapped_private_key"`
}

type registerResponse struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validate.Email(req.Email) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if !validate.MinLength(req.Password, 8) {
		httpx.WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if !validate.NonEmpty(req.DisplayName) {
		httpx.WriteError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	input := RegisterInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Locale:      req.Locale,
	}
	var decodeErr error
	for _, field := range []struct {
		src *string
		dst *[]byte
	}{
		{req.KDFSalt, &input.KDFSalt},
		{req.PublicKey, &input.PublicKey},
		{req.EncryptedPrivateKey, &input.EncryptedPrivateKey},
		{req.RecoveryWrappedPrivateKey, &input.RecoveryWrappedPrivateKey},
	} {
		if *field.dst, decodeErr = httpx.DecodeOptionalBase64(field.src); decodeErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, "key fields must be base64 encoded")
			return
		}
	}

	output, err := h.auth.Register(r.Context(), input)
	if err != nil {
		handleAuthError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, registerResponse{
		UserID:      output.User.ID.String(),
		Email:       output.User.Email,
		DisplayName: output.User.DisplayName,
	})
}

type loginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	DeviceType string `json:"device_type"`
}

type loginResponse struct {
	AccessToken         string  `json:"access_token"`
	RefreshToken        string  `json:"refresh_token"`
	UserID              string  `json:"user_id"`
	Email               string  `json:"email"`
	DisplayName         string  `json:"display_name"`
	AvatarURL           string  `json:"avatar_url,omitempty"`
	EmailVerified       bool    `json:"email_verified"`
	Locale              string  `json:"locale"`
	KDFVersion          int     `json:"kdf_version"`
	PublicKey           *string `json:"public_key"`
	EncryptedPrivateKey *string `json:"encrypted_private_key"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validate.Email(req.Email) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if !validate.NonEmpty(req.Password) {
		httpx.WriteError(w, http.StatusBadRequest, "password is required")
		return
	}
	if !validate.NonEmpty(req.DeviceName) {
		httpx.WriteError(w, http.StatusBadRequest, "device_name is required")
		return
	}
	if !validate.NonEmpty(req.DeviceType) {
		httpx.WriteError(w, http.StatusBadRequest, "device_type is required")
		return
	}

	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid device_id")
		return
	}

	output, err := h.auth.Login(r.Context(), LoginInput{
		Email:      req.Email,
		Password:   req.Password,
		DeviceID:   deviceID,
		DeviceName: req.DeviceName,
		DeviceType: req.DeviceType,
	})
	if err != nil {
		handleAuthError(w, err)
		return
	}

	resp := loginResponse{
		AccessToken:   output.AccessToken,
		RefreshToken:  output.RefreshToken,
		UserID:        output.User.ID.String(),
		Email:         output.User.Email,
		DisplayName:   output.User.DisplayName,
		AvatarURL:     output.User.AvatarURL(),
		EmailVerified: output.User.EmailVerifiedAt != nil,
		Locale:        output.User.Locale,
		KDFVersion:    output.User.KDFVersion,
	}
	if output.User.KDFVersion == domain.KDFVersionArgon2 {
		resp.PublicKey = httpx.EncodeBase64(output.User.PublicKey)
		resp.EncryptedPrivateKey = httpx.EncodeBase64(output.User.EncryptedPrivateKey)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

type preloginRequest struct {
	Email string `json:"email"`
}

type kdfParams struct {
	Algorithm   string `json:"algorithm"`
	MemoryKiB   int    `json:"memory_kib"`
	Iterations  int    `json:"iterations"`
	Parallelism int    `json:"parallelism"`
}

type preloginResponse struct {
	KDFVersion int       `json:"kdf_version"`
	KDFSalt    *string   `json:"kdf_salt,omitempty"`
	KDF        kdfParams `json:"kdf"`
}

func (h *Handler) Prelogin(w http.ResponseWriter, r *http.Request) {
	var req preloginRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !validate.Email(req.Email) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	output, err := h.auth.Prelogin(r.Context(), req.Email)
	if err != nil {
		handleAuthError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, preloginResponse{
		KDFVersion: output.KDFVersion,
		KDFSalt:    httpx.EncodeBase64(output.KDFSalt),
		KDF: kdfParams{
			Algorithm:   domain.KDFAlgorithm,
			MemoryKiB:   domain.KDFMemoryKiB,
			Iterations:  domain.KDFIterations,
			Parallelism: domain.KDFParallelism,
		},
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validate.NonEmpty(req.RefreshToken) {
		httpx.WriteError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	output, err := h.auth.Refresh(r.Context(), RefreshInput{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		handleAuthError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, refreshResponse{
		AccessToken:  output.AccessToken,
		RefreshToken: output.RefreshToken,
	})
}

type logoutRequest struct {
	AllDevices bool `json:"all_devices"`
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	var req logoutRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	err := h.auth.Logout(r.Context(), LogoutInput{
		UserID:     claims.UserID,
		DeviceID:   deviceID,
		AllDevices: req.AllDevices,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "logout failed")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		httpx.WriteErrorWithCode(w, http.StatusConflict, "user already exists", "user_exists")
	case errors.Is(err, domain.ErrRegistrationClosed):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "registration is closed", "registration_closed")
	case errors.Is(err, domain.ErrInvalidCredentials):
		httpx.WriteError(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, domain.ErrDeviceRevoked):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "device has been revoked", "device_revoked")
	case errors.Is(err, domain.ErrDeviceConflict):
		httpx.WriteErrorWithCode(w, http.StatusConflict, "device id belongs to another account, generate a new device id", "device_conflict")
	case errors.Is(err, domain.ErrTokenExpired):
		httpx.WriteErrorWithCode(w, http.StatusUnauthorized, "token expired", "token_expired")
	case errors.Is(err, domain.ErrTokenRevoked):
		httpx.WriteErrorWithCode(w, http.StatusUnauthorized, "token revoked", "token_revoked")
	case errors.Is(err, domain.ErrTokenReuseDetected):
		httpx.WriteErrorWithCode(w, http.StatusUnauthorized, "token reuse detected, all sessions revoked", "token_reuse")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
