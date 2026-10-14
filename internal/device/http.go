package device

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	devices *Service
}

func NewHandler(devices *Service) *Handler {
	return &Handler{devices: devices}
}

type DeviceResponse struct {
	ID         string `json:"id"`
	DeviceName string `json:"device_name"`
	DeviceType string `json:"device_type"`
	LastSeenAt string `json:"last_seen_at"`
	CreatedAt  string `json:"created_at"`
	Revoked    bool   `json:"revoked"`
	IsCurrent  bool   `json:"is_current,omitempty"`
}

func ToDeviceResponse(d DeviceInfo) DeviceResponse {
	return DeviceResponse{
		ID:         d.ID.String(),
		DeviceName: d.DeviceName,
		DeviceType: d.DeviceType,
		LastSeenAt: d.LastSeenAt,
		CreatedAt:  d.CreatedAt,
		Revoked:    d.Revoked,
		IsCurrent:  d.IsCurrent,
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := httpx.DeviceID(r.Context())

	devices, err := h.devices.List(r.Context(), claims.UserID, deviceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list devices")
		return
	}

	resp := make([]DeviceResponse, 0, len(devices))
	for _, d := range devices {
		resp = append(resp, ToDeviceResponse(d))
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	device, err := h.devices.Get(r.Context(), claims.UserID, deviceID)
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToDeviceResponse(*device))
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	currentDeviceID := httpx.DeviceID(r.Context())

	err = h.devices.Revoke(r.Context(), RevokeDeviceInput{
		UserID:          claims.UserID,
		DeviceID:        deviceID,
		CurrentDeviceID: currentDeviceID,
	})
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type updateDeviceRequest struct {
	DeviceName string `json:"device_name"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims := httpx.ClaimsFromContext(r.Context())
	if claims == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	var req updateDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	device, err := h.devices.Update(r.Context(), UpdateDeviceInput{
		UserID:     claims.UserID,
		DeviceID:   deviceID,
		DeviceName: req.DeviceName,
	})
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToDeviceResponse(*device))
}

func handleDeviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "device not found")
	case errors.Is(err, domain.ErrInvalidInput):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrDeviceRevoked):
		httpx.WriteErrorWithCode(w, http.StatusForbidden, "device has been revoked", "device_revoked")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
