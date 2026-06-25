package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

type testHarness struct {
	router   *chi.Mux
	tokenGen port.TokenGenerator
}

func newTestHarness() *testHarness {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")
	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	authHandler := handler.NewAuthHandler(authUC)

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Post("/token/refresh", authHandler.Refresh)
	})
	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Post("/auth/v1/logout", authHandler.Logout)
	})

	return &testHarness{router: r, tokenGen: tokenGen}
}

func doRequest(h *testHarness, method, path string, body any) *httptest.ResponseRecorder {
	return doRequestWithHeaders(h, method, path, body, nil)
}

func doRequestWithHeaders(h *testHarness, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func registerUser(t *testing.T, h *testHarness, email, password string) {
	t.Helper()
	rec := doRequest(h, "POST", "/auth/v1/register", map[string]string{
		"email":        email,
		"password":     password,
		"display_name": "Test User",
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func loginUser(t *testing.T, h *testHarness, email, password string) (string, string, string) {
	t.Helper()
	deviceID := uuid.New().String()
	rec := doRequest(h, "POST", "/auth/v1/login", map[string]string{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"device_name": "Test Device",
		"device_type": "desktop",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["access_token"], resp["refresh_token"], deviceID
}

func TestAuthHandler_Register_Success(t *testing.T) {
	h := newTestHarness()
	rec := doRequest(h, "POST", "/auth/v1/register", map[string]string{
		"email":        "test@example.com",
		"password":     "password123",
		"display_name": "Test User",
	})

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "test@example.com", resp["email"])
	assert.Equal(t, "Test User", resp["display_name"])
	assert.NotEmpty(t, resp["user_id"])
}

func TestAuthHandler_Register_Duplicate(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")

	rec := doRequest(h, "POST", "/auth/v1/register", map[string]string{
		"email":        "test@example.com",
		"password":     "password456",
		"display_name": "Test User",
	})

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestAuthHandler_Register_InvalidBody(t *testing.T) {
	h := newTestHarness()
	req := httptest.NewRequest("POST", "/auth/v1/register", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthHandler_Login_Success(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")

	accessToken, refreshToken, _ := loginUser(t, h, "test@example.com", "password123")
	assert.NotEmpty(t, accessToken)
	assert.NotEmpty(t, refreshToken)
}

func TestAuthHandler_Login_WrongPassword(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")

	rec := doRequest(h, "POST", "/auth/v1/login", map[string]string{
		"email":       "test@example.com",
		"password":    "wrongpassword",
		"device_id":   uuid.New().String(),
		"device_name": "Test Device",
		"device_type": "desktop",
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHandler_Login_InvalidDeviceID(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")

	rec := doRequest(h, "POST", "/auth/v1/login", map[string]string{
		"email":     "test@example.com",
		"password":  "password123",
		"device_id": "not-a-uuid",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthHandler_Refresh_Success(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")
	_, refreshToken, _ := loginUser(t, h, "test@example.com", "password123")

	rec := doRequest(h, "POST", "/auth/v1/token/refresh", map[string]string{
		"refresh_token": refreshToken,
	})

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["access_token"])
	assert.NotEmpty(t, resp["refresh_token"])
	assert.NotEqual(t, refreshToken, resp["refresh_token"])
}

func TestAuthHandler_Refresh_ReuseDetection(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")
	_, refreshToken, _ := loginUser(t, h, "test@example.com", "password123")

	rec := doRequest(h, "POST", "/auth/v1/token/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = doRequest(h, "POST", "/auth/v1/token/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["code"], "token_reuse")
}

func TestAuthHandler_Logout_Success(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")
	accessToken, _, deviceID := loginUser(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(h, "POST", "/auth/v1/logout", map[string]bool{
		"all_devices": false,
	}, map[string]string{
		"Authorization": "Bearer " + accessToken,
		"X-Device-ID":   deviceID,
	})

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthHandler_Logout_MissingAuth(t *testing.T) {
	h := newTestHarness()

	rec := doRequest(h, "POST", "/auth/v1/logout", nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHandler_Logout_MissingDeviceID(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")
	accessToken, _, _ := loginUser(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(h, "POST", "/auth/v1/logout", nil, map[string]string{
		"Authorization": "Bearer " + accessToken,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	h := newTestHarness()

	rec := doRequestWithHeaders(h, "POST", "/auth/v1/logout", nil, map[string]string{
		"Authorization": "Bearer invalid-token",
		"X-Device-ID":   uuid.New().String(),
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_MalformedHeader(t *testing.T) {
	h := newTestHarness()

	rec := doRequestWithHeaders(h, "POST", "/auth/v1/logout", nil, map[string]string{
		"Authorization": "not-bearer-format",
		"X-Device-ID":   uuid.New().String(),
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")
	accessToken, _, deviceID := loginUser(t, h, "test@example.com", "password123")

	claims, err := h.tokenGen.ValidateAccessToken(accessToken)
	require.NoError(t, err)
	assert.Equal(t, "test@example.com", claims.Email)
	_ = context.Background()
	_ = deviceID
}

func TestDeviceMiddleware_InvalidUUID(t *testing.T) {
	h := newTestHarness()
	registerUser(t, h, "test@example.com", "password123")
	accessToken, _, _ := loginUser(t, h, "test@example.com", "password123")

	rec := doRequestWithHeaders(h, "POST", "/auth/v1/logout", nil, map[string]string{
		"Authorization": "Bearer " + accessToken,
		"X-Device-ID":   "not-a-uuid",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
