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

type vaultTestHarness struct {
	router   *chi.Mux
	tokenGen port.TokenGenerator
}

func newVaultTestHarness() *vaultTestHarness {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	vaultRepo := fake.NewVaultRepository()
	memberRepo := fake.NewVaultMemberRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	keyRepo := fake.NewVaultKeyRepository()
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	vaultUC := usecase.NewVaultUsecase(vaultRepo, memberRepo, keyRepo, inviteRepo, fake.NewTransactor())
	memberUC := usecase.NewVaultMemberUsecase(memberRepo, keyRepo, userRepo)
	inviteUC := usecase.NewVaultInviteUsecase(inviteRepo, memberRepo, keyRepo, userRepo, vaultRepo, fake.NewTransactor())

	authHandler := handler.NewAuthHandler(authUC)
	vaultHandler := handler.NewVaultHandler(vaultUC)
	memberHandler := handler.NewVaultMemberHandler(memberUC)
	inviteHandler := handler.NewVaultInviteHandler(inviteUC)

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
			r.Get("/", vaultHandler.List)
			r.Get("/invites", inviteHandler.ListMyInvites)
			r.Post("/invites/accept", inviteHandler.Accept)
			r.Route("/{vaultID}", func(r chi.Router) {
				r.Get("/", vaultHandler.Get)
				r.Patch("/", vaultHandler.Update)
				r.Delete("/", vaultHandler.Delete)
				r.Route("/members", func(r chi.Router) {
					r.Get("/", memberHandler.List)
					r.Patch("/{userID}", memberHandler.UpdateRole)
					r.Delete("/{userID}", memberHandler.Remove)
				})
				r.Route("/invites", func(r chi.Router) {
					r.Post("/", inviteHandler.Create)
					r.Get("/", inviteHandler.ListByVault)
					r.Delete("/{inviteID}", inviteHandler.Delete)
				})
			})
		})
	})

	return &vaultTestHarness{router: r, tokenGen: tokenGen}
}

func vaultRegisterAndLogin(t *testing.T, h *vaultTestHarness, email, password string) (string, string) {
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

func vaultAuthHeaders(accessToken, deviceID string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + accessToken,
		"X-Device-ID":   deviceID,
	}
}

func createVault(t *testing.T, h *vaultTestHarness, accessToken, deviceID, name string) map[string]any {
	t.Helper()
	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("test-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/", map[string]string{
		"name":                name,
		"description":         "A test vault",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var resp map[string]any
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	return resp
}

func TestVaultHandler_Create_Success(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	vault := createVault(t, h, accessToken, deviceID, "My Vault")
	assert.NotEmpty(t, vault["id"])
	assert.Equal(t, "My Vault", vault["name"])
	assert.Equal(t, "A test vault", vault["description"])
	assert.Equal(t, "owner", vault["role"])
}

func TestVaultHandler_Create_MissingName(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("test-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/", map[string]string{
		"name":                "",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultHandler_Create_MissingKey(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/", map[string]string{
		"name":                "Test Vault",
		"encrypted_vault_key": "",
	}, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultHandler_Create_InvalidBase64Key(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/", map[string]string{
		"name":                "Test Vault",
		"encrypted_vault_key": "not-valid-base64!@#$",
	}, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultHandler_List_Success(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	createVault(t, h, accessToken, deviceID, "Vault 1")
	createVault(t, h, accessToken, deviceID, "Vault 2")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/", nil, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var vaults []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&vaults)
	require.NoError(t, err)
	assert.Len(t, vaults, 2)
}

func TestVaultHandler_List_Empty(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/", nil, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var vaults []map[string]any
	err := json.NewDecoder(rec.Body).Decode(&vaults)
	require.NoError(t, err)
	assert.Len(t, vaults, 0)
}

func TestVaultHandler_Get_Success(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, accessToken, deviceID, "My Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/", nil, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var vault map[string]any
	err := json.NewDecoder(rec.Body).Decode(&vault)
	require.NoError(t, err)
	assert.Equal(t, vaultID, vault["id"])
	assert.Equal(t, "My Vault", vault["name"])
	assert.Equal(t, "owner", vault["role"])
}

func TestVaultHandler_Get_NotMember(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := vaultRegisterAndLogin(t, h, "other@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Private Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/", nil, vaultAuthHeaders(otherToken, otherDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultHandler_Get_InvalidUUID(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/not-a-uuid/", nil, vaultAuthHeaders(accessToken, deviceID))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVaultHandler_Update_Success(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, accessToken, deviceID, "Old Name")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "PATCH", "/vaults/v1/"+vaultID+"/", map[string]string{
		"name": "New Name",
	}, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var vault map[string]any
	err := json.NewDecoder(rec.Body).Decode(&vault)
	require.NoError(t, err)
	assert.Equal(t, "New Name", vault["name"])
}

func TestVaultHandler_Update_NotMember(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	otherToken, otherDevice := vaultRegisterAndLogin(t, h, "other@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "My Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "PATCH", "/vaults/v1/"+vaultID+"/", map[string]string{
		"name": "Hacked Name",
	}, vaultAuthHeaders(otherToken, otherDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultHandler_Delete_Success(t *testing.T) {
	h := newVaultTestHarness()
	accessToken, deviceID := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")

	created := createVault(t, h, accessToken, deviceID, "Doomed Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	rec := doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/", nil, vaultAuthHeaders(accessToken, deviceID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "GET", "/vaults/v1/"+vaultID+"/", nil, vaultAuthHeaders(accessToken, deviceID))
	assert.True(t, rec.Code == http.StatusNotFound || rec.Code == http.StatusForbidden, "expected 404 or 403, got %d", rec.Code)
}

func TestVaultHandler_Delete_NotOwner(t *testing.T) {
	h := newVaultTestHarness()
	ownerToken, ownerDevice := vaultRegisterAndLogin(t, h, "owner@example.com", "password123")
	editorToken, editorDevice := vaultRegisterAndLogin(t, h, "editor@example.com", "password123")

	created := createVault(t, h, ownerToken, ownerDevice, "Protected Vault")
	vaultID := created["id"].(string)

	th := &testHarness{router: h.router}
	encKey := base64.StdEncoding.EncodeToString([]byte("editor-encrypted-key"))
	rec := doRequestWithHeaders(th, "POST", "/vaults/v1/"+vaultID+"/invites/", map[string]string{
		"invitee_email":       "editor@example.com",
		"role":                "editor",
		"encrypted_vault_key": encKey,
	}, vaultAuthHeaders(ownerToken, ownerDevice))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var invite map[string]any
	err := json.NewDecoder(rec.Body).Decode(&invite)
	require.NoError(t, err)
	inviteID := invite["id"].(string)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/invites/accept", map[string]string{
		"invite_id": inviteID,
	}, vaultAuthHeaders(editorToken, editorDevice))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequestWithHeaders(th, "DELETE", "/vaults/v1/"+vaultID+"/", nil, vaultAuthHeaders(editorToken, editorDevice))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVaultHandler_Unauthorized(t *testing.T) {
	h := newVaultTestHarness()
	th := &testHarness{router: h.router}

	rec := doRequestWithHeaders(th, "GET", "/vaults/v1/", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = doRequestWithHeaders(th, "POST", "/vaults/v1/", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
