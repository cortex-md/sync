package handler_test

import (
	"encoding/base64"
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

type encryptionTestHarness struct {
	router   *chi.Mux
	tokenGen port.TokenGenerator
}

func newEncryptionTestHarness() *encryptionTestHarness {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	keyRepo := fake.NewVaultKeyRepository()
	encryptionRepo := fake.NewVaultEncryptionRepository()
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	encryptionUC := usecase.NewVaultEncryptionUsecase(encryptionRepo, memberRepo)

	authHandler := handler.NewAuthHandler(authUC)
	vaultHandler := handler.NewVaultHandler(vaultUC)
	encryptionHandler := handler.NewVaultEncryptionHandler(encryptionUC)

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
	})
	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Route("/vaults/v1", func(r chi.Router) {
			r.Post("/", vaultHandler.Create)
		})
		r.Route("/sync/v1/vaults/{vaultID}", func(r chi.Router) {
			r.Get("/encryption", encryptionHandler.Get)
			r.Post("/encryption", encryptionHandler.Create)
		})
	})

	return &encryptionTestHarness{router: r, tokenGen: tokenGen}
}

func encRegisterAndLogin(t *testing.T, h *encryptionTestHarness, email, password string) (string, string) {
	t.Helper()
	deviceID := uuid.New().String()
	th := &testHarness{router: h.router, tokenGen: h.tokenGen}

	rec := doRequestWithHeaders(th, "POST", "/auth/v1/register", map[string]string{
		"email":        email,
		"password":     password,
		"display_name": "Test User",
	}, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "POST", "/auth/v1/login", map[string]string{
		"email":       email,
		"password":    password,
		"device_id":   deviceID,
		"device_name": "Test Device",
		"device_type": "desktop",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["access_token"], deviceID
}

func encCreateVault(t *testing.T, h *encryptionTestHarness, accessToken, deviceID string) string {
	t.Helper()
	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("test-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/", map[string]string{
		"name":                "Test Vault",
		"encrypted_vault_key": encKey,
	}, map[string]string{
		"Authorization": "Bearer " + accessToken,
		"X-Device-ID":   deviceID,
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["id"].(string)
}

func TestVaultEncryptionGet_NoEncryption(t *testing.T) {
	h := newEncryptionTestHarness()
	token, deviceID := encRegisterAndLogin(t, h, "test@example.com", "password123")
	vaultID := encCreateVault(t, h, token, deviceID)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/sync/v1/vaults/"+vaultID+"/encryption", nil, map[string]string{
		"Authorization": "Bearer " + token,
		"X-Device-ID":   deviceID,
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp["has_key"].(bool))
}

func TestVaultEncryptionCreate_Success(t *testing.T) {
	h := newEncryptionTestHarness()
	token, deviceID := encRegisterAndLogin(t, h, "test@example.com", "password123")
	vaultID := encCreateVault(t, h, token, deviceID)

	th := &testHarness{router: h.router}
	salt := base64.StdEncoding.EncodeToString([]byte("test-salt-16bytes"))
	evek := base64.StdEncoding.EncodeToString([]byte("test-encrypted-vek"))

	rec := doRequestWithHeaders(th, "POST", "/sync/v1/vaults/"+vaultID+"/encryption", map[string]string{
		"salt":          salt,
		"encrypted_vek": evek,
	}, map[string]string{
		"Authorization": "Bearer " + token,
		"X-Device-ID":   deviceID,
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "GET", "/sync/v1/vaults/"+vaultID+"/encryption", nil, map[string]string{
		"Authorization": "Bearer " + token,
		"X-Device-ID":   deviceID,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp["has_key"].(bool))
	assert.Equal(t, salt, resp["salt"])
	assert.Equal(t, evek, resp["encrypted_vek"])
}

func TestVaultEncryptionCreate_InvalidBase64(t *testing.T) {
	h := newEncryptionTestHarness()
	token, deviceID := encRegisterAndLogin(t, h, "test@example.com", "password123")
	vaultID := encCreateVault(t, h, token, deviceID)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "POST", "/sync/v1/vaults/"+vaultID+"/encryption", map[string]string{
		"salt":          "not-valid-base64!!!",
		"encrypted_vek": "also-not-valid!!!",
	}, map[string]string{
		"Authorization": "Bearer " + token,
		"X-Device-ID":   deviceID,
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultEncryptionGet_NotMember(t *testing.T) {
	h := newEncryptionTestHarness()
	token1, deviceID1 := encRegisterAndLogin(t, h, "owner@example.com", "password123")
	vaultID := encCreateVault(t, h, token1, deviceID1)

	token2, deviceID2 := encRegisterAndLogin(t, h, "other@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/sync/v1/vaults/"+vaultID+"/encryption", nil, map[string]string{
		"Authorization": "Bearer " + token2,
		"X-Device-ID":   deviceID2,
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultEncryptionGet_Unauthorized(t *testing.T) {
	h := newEncryptionTestHarness()
	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/sync/v1/vaults/"+uuid.New().String()+"/encryption", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
