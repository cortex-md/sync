package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/adapter/handler"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deviceTestHarness struct {
	router   *chi.Mux
	tokenGen port.TokenGenerator
}

func newDeviceTestHarness() *deviceTestHarness {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	deviceUC := usecase.NewDeviceUsecase(deviceRepo, refreshTokenRepo)

	authHandler := handler.NewAuthHandler(authUC)
	deviceHandler := handler.NewDeviceHandler(deviceUC)

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
	})
	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Route("/devices/v1", func(r chi.Router) {
			r.Get("/", deviceHandler.List)
			r.Get("/{deviceID}", deviceHandler.Get)
			r.Delete("/{deviceID}", deviceHandler.Revoke)
			r.Patch("/{deviceID}", deviceHandler.Update)
			r.Put("/{deviceID}/sync-cursor", deviceHandler.UpdateSyncCursor)
		})
	})

	return &deviceTestHarness{router: r, tokenGen: tokenGen}
}

func registerAndLogin(t *testing.T, h *deviceTestHarness, email, password string) (string, string) {
	t.Helper()
	deviceID := uuid.New().String()

	rec := doRequestWithHeaders(&testHarness{router: h.router, tokenGen: h.tokenGen}, "POST", "/auth/v1/register", map[string]string{
		"email":        email,
		"password":     password,
		"display_name": "Test User",
	}, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(&testHarness{router: h.router, tokenGen: h.tokenGen}, "POST", "/auth/v1/login", map[string]string{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"device_name": "Primary Device",
		"device_type": "desktop",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	return resp["access_token"], deviceID
}

func loginWithDevice(t *testing.T, h *deviceTestHarness, email, password, deviceID, deviceName string) string {
	t.Helper()
	rec := doRequestWithHeaders(&testHarness{router: h.router, tokenGen: h.tokenGen}, "POST", "/auth/v1/login", map[string]string{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"device_name": deviceName,
		"device_type": "mobile",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["access_token"]
}

func authHeaders(accessToken, deviceID string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + accessToken,
		"X-Device-ID":   deviceID,
	}
}

func TestDeviceHandler_List(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	secondDeviceID := uuid.New().String()
	loginWithDevice(t, h, "test@example.com", "password123", secondDeviceID, "Second Device")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/", nil, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var devices []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&devices)
	require.NoError(t, err)
	assert.Len(t, devices, 2)

	var currentCount int
	for _, d := range devices {
		if d["is_current"] == true {
			currentCount++
		}
	}
	assert.Equal(t, 1, currentCount)
}

func TestDeviceHandler_Get(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/"+deviceID, nil, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var device map[string]any
	err := json.NewDecoder(rec.Body).Decode(&device)
	require.NoError(t, err)
	assert.Equal(t, deviceID, device["id"])
}

func TestDeviceHandler_Get_NotFound(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/"+uuid.New().String(), nil, authHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeviceHandler_Get_InvalidUUID(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/not-a-uuid", nil, authHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeviceHandler_Revoke(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	secondDeviceID := uuid.New().String()
	loginWithDevice(t, h, "test@example.com", "password123", secondDeviceID, "Second Device")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "DELETE", "/devices/v1/"+secondDeviceID, nil, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/"+secondDeviceID, nil, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code)

	var device map[string]any
	err := json.NewDecoder(rec.Body).Decode(&device)
	require.NoError(t, err)
	assert.Equal(t, true, device["revoked"])
}

func TestDeviceHandler_Revoke_Self(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "DELETE", "/devices/v1/"+deviceID, nil, authHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeviceHandler_Update(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "PATCH", "/devices/v1/"+deviceID, map[string]string{
		"device_name": "Renamed Device",
	}, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var device map[string]any
	err := json.NewDecoder(rec.Body).Decode(&device)
	require.NoError(t, err)
	assert.Equal(t, "Renamed Device", device["device_name"])
}

func TestDeviceHandler_Update_EmptyName(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "PATCH", "/devices/v1/"+deviceID, map[string]string{
		"device_name": "",
	}, authHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeviceHandler_Unauthorized(t *testing.T) {
	h := newDeviceTestHarness()

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeviceHandler_UpdateSyncCursor(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "PUT", "/devices/v1/"+deviceID+"/sync-cursor", map[string]any{
		"last_sync_event_id": 42,
	}, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(&testHarness{router: h.router}, "GET", "/devices/v1/"+deviceID, nil, authHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code)

	var device map[string]any
	err := json.NewDecoder(rec.Body).Decode(&device)
	require.NoError(t, err)
	assert.Equal(t, float64(42), device["last_sync_event_id"])
}

func TestDeviceHandler_UpdateSyncCursor_InvalidDeviceID(t *testing.T) {
	h := newDeviceTestHarness()
	accessToken, deviceID := registerAndLogin(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "PUT", "/devices/v1/not-a-uuid/sync-cursor", map[string]any{
		"last_sync_event_id": 42,
	}, authHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeviceHandler_UpdateSyncCursor_Unauthorized(t *testing.T) {
	h := newDeviceTestHarness()

	rec := doRequestWithHeaders(&testHarness{router: h.router}, "PUT", "/devices/v1/"+uuid.New().String()+"/sync-cursor", map[string]any{
		"last_sync_event_id": 42,
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
