package handler

import (
	"encoding/json"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/adapter/validate"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
)

type AuthHandler struct {
	auth *usecase.AuthUsecase
}

func NewAuthHandler(auth *usecase.AuthUsecase) *AuthHandler {
	return &AuthHandler{auth: auth}
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type registerResponse struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validate.Email(req.Email) {
		WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if !validate.MinLength(req.Password, 8) {
		WriteError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if !validate.NonEmpty(req.DisplayName) {
		WriteError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	output, err := h.auth.Register(r.Context(), usecase.RegisterInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		handleAuthError(w, err)
		return
	}

	WriteJSON(w, http.StatusCreated, registerResponse{
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
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validate.Email(req.Email) {
		WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if !validate.NonEmpty(req.Password) {
		WriteError(w, http.StatusBadRequest, "password is required")
		return
	}
	if !validate.NonEmpty(req.DeviceName) {
		WriteError(w, http.StatusBadRequest, "device_name is required")
		return
	}
	if !validate.NonEmpty(req.DeviceType) {
		WriteError(w, http.StatusBadRequest, "device_type is required")
		return
	}

	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid device_id")
		return
	}

	output, err := h.auth.Login(r.Context(), usecase.LoginInput{
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

	WriteJSON(w, http.StatusOK, loginResponse{
		AccessToken:  output.AccessToken,
		RefreshToken: output.RefreshToken,
		UserID:       output.User.ID.String(),
		Email:        output.User.Email,
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !validate.NonEmpty(req.RefreshToken) {
		WriteError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	output, err := h.auth.Refresh(r.Context(), usecase.RefreshInput{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		handleAuthError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, refreshResponse{
		AccessToken:  output.AccessToken,
		RefreshToken: output.RefreshToken,
	})
}

type logoutRequest struct {
	AllDevices bool `json:"all_devices"`
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := GetDeviceID(r.Context())

	var req logoutRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	err := h.auth.Logout(r.Context(), usecase.LogoutInput{
		UserID:     claims.UserID,
		DeviceID:   deviceID,
		AllDevices: req.AllDevices,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "logout failed")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleAuthError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrInvalidInput:
		WriteError(w, http.StatusBadRequest, err.Error())
	case domain.ErrAlreadyExists:
		WriteErrorWithCode(w, http.StatusConflict, "user already exists", "user_exists")
	case domain.ErrInvalidCredentials:
		WriteError(w, http.StatusUnauthorized, "invalid credentials")
	case domain.ErrDeviceRevoked:
		WriteErrorWithCode(w, http.StatusForbidden, "device has been revoked", "device_revoked")
	case domain.ErrTokenExpired:
		WriteErrorWithCode(w, http.StatusUnauthorized, "token expired", "token_expired")
	case domain.ErrTokenRevoked:
		WriteErrorWithCode(w, http.StatusUnauthorized, "token revoked", "token_revoked")
	case domain.ErrTokenReuseDetected:
		WriteErrorWithCode(w, http.StatusUnauthorized, "token reuse detected, all sessions revoked", "token_reuse")
	default:
		WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
