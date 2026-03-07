package handler

import (
	"encoding/json"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type DeviceHandler struct {
	devices *usecase.DeviceUsecase
}

func NewDeviceHandler(devices *usecase.DeviceUsecase) *DeviceHandler {
	return &DeviceHandler{devices: devices}
}

type deviceResponse struct {
	ID              string `json:"id"`
	DeviceName      string `json:"device_name"`
	DeviceType      string `json:"device_type"`
	LastSeenAt      string `json:"last_seen_at"`
	CreatedAt       string `json:"created_at"`
	Revoked         bool   `json:"revoked"`
	IsCurrent       bool   `json:"is_current,omitempty"`
	LastSyncEventID int64  `json:"last_sync_event_id"`
}

func toDeviceResponse(d usecase.DeviceInfo) deviceResponse {
	return deviceResponse{
		ID:              d.ID.String(),
		DeviceName:      d.DeviceName,
		DeviceType:      d.DeviceType,
		LastSeenAt:      d.LastSeenAt,
		CreatedAt:       d.CreatedAt,
		Revoked:         d.Revoked,
		IsCurrent:       d.IsCurrent,
		LastSyncEventID: d.LastSyncEventID,
	}
}

func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID := GetDeviceID(r.Context())

	devices, err := h.devices.List(r.Context(), claims.UserID, deviceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "failed to list devices")
		return
	}

	resp := make([]deviceResponse, 0, len(devices))
	for _, d := range devices {
		resp = append(resp, toDeviceResponse(d))
	}

	WriteJSON(w, http.StatusOK, resp)
}

func (h *DeviceHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	device, err := h.devices.Get(r.Context(), claims.UserID, deviceID)
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, toDeviceResponse(*device))
}

func (h *DeviceHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	currentDeviceID := GetDeviceID(r.Context())

	err = h.devices.Revoke(r.Context(), usecase.RevokeDeviceInput{
		UserID:          claims.UserID,
		DeviceID:        deviceID,
		CurrentDeviceID: currentDeviceID,
	})
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type updateDeviceRequest struct {
	DeviceName string `json:"device_name"`
}

func (h *DeviceHandler) Update(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	var req updateDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	device, err := h.devices.Update(r.Context(), usecase.UpdateDeviceInput{
		UserID:     claims.UserID,
		DeviceID:   deviceID,
		DeviceName: req.DeviceName,
	})
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, toDeviceResponse(*device))
}

type updateSyncCursorRequest struct {
	LastSyncEventID int64 `json:"last_sync_event_id"`
}

func (h *DeviceHandler) UpdateSyncCursor(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	deviceID, err := uuid.Parse(chi.URLParam(r, "deviceID"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	var req updateSyncCursorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err = h.devices.UpdateSyncCursor(r.Context(), usecase.UpdateSyncCursorInput{
		UserID:          claims.UserID,
		DeviceID:        deviceID,
		LastSyncEventID: req.LastSyncEventID,
	})
	if err != nil {
		handleDeviceError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleDeviceError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrNotFound:
		WriteError(w, http.StatusNotFound, "device not found")
	case domain.ErrInvalidInput:
		WriteError(w, http.StatusBadRequest, err.Error())
	case domain.ErrDeviceRevoked:
		WriteErrorWithCode(w, http.StatusForbidden, "device has been revoked", "device_revoked")
	default:
		WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
