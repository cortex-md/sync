package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/server"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stackClient struct {
	t       *testing.T
	handler http.Handler
}

func newProtectedStack(t *testing.T, env map[string]string) *stackClient {
	t.Helper()
	t.Setenv("CORTEX_USE_FAKE_REPOS", "true")
	t.Setenv("CORTEX_AUTH_ACCESS_TOKEN_SECRET", "protection-test-secret-with-32-characters")
	for key, value := range env {
		t.Setenv(key, value)
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	infra, closeInfra, err := server.NewInfra(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(closeInfra)
	return &stackClient{t: t, handler: server.New(ctx, cfg, infra).Handler()}
}

func (c *stackClient) do(method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "198.51.100.10:4000"
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

func (c *stackClient) json(method, path string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	require.NoError(c.t, json.NewEncoder(&buf).Encode(payload))
	merged := map[string]string{"Content-Type": "application/json"}
	for key, value := range headers {
		merged[key] = value
	}
	return c.do(method, path, &buf, merged)
}

func (c *stackClient) session(email string) map[string]string {
	rec := c.json(http.MethodPost, "/auth/v1/register", map[string]string{"email": email, "password": "password123", "display_name": "Test"}, nil)
	require.Equal(c.t, http.StatusCreated, rec.Code, rec.Body.String())
	deviceID := uuid.NewString()
	rec = c.json(http.MethodPost, "/auth/v1/login", map[string]string{"email": email, "password": "password123", "device_id": deviceID, "device_name": "Test", "device_type": "desktop"}, nil)
	require.Equal(c.t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(c.t, json.NewDecoder(rec.Body).Decode(&out))
	return map[string]string{"Authorization": "Bearer " + out.AccessToken, "X-Device-ID": deviceID}
}

func (c *stackClient) vault(headers map[string]string) string {
	rec := c.json(http.MethodPost, "/vaults/v1/", map[string]string{"name": "Notes"}, headers)
	require.Equal(c.t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(c.t, json.NewDecoder(rec.Body).Decode(&out))
	return out.ID
}

func TestProtectionRejectsChunkedSnapshotUploads(t *testing.T) {
	stack := newProtectedStack(t, nil)
	headers := stack.session("chunked@example.com")
	vaultID := stack.vault(headers)

	headers["Content-Type"] = "application/octet-stream"
	headers["X-File-Path"] = "a.md"
	req := httptest.NewRequest(http.MethodPost, "/sync/v1/vaults/"+vaultID+"/files", strings.NewReader("hello"))
	req.ContentLength = -1
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	stack.handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusLengthRequired, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"length_required"`)
}

func TestProtectionCapsJSONBodies(t *testing.T) {
	stack := newProtectedStack(t, map[string]string{"CORTEX_SERVER_MAX_JSON_BODY": "256"})
	headers := stack.session("json@example.com")

	rec := stack.json(http.MethodPost, "/vaults/v1/", map[string]string{"name": strings.Repeat("x", 400)}, headers)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"payload_too_large"`)
}

func TestProtectionLimitsRegistrationsPerAddress(t *testing.T) {
	stack := newProtectedStack(t, map[string]string{"CORTEX_PROTECTION_REGISTRATIONS_PER_HOUR": "2"})
	for i := range 2 {
		rec := stack.json(http.MethodPost, "/auth/v1/register", map[string]string{"email": "user" + string(rune('a'+i)) + "@example.com", "password": "password123", "display_name": "Test"}, nil)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}
	rec := stack.json(http.MethodPost, "/auth/v1/register", map[string]string{"email": "userz@example.com", "password": "password123", "display_name": "Test"}, nil)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), `"code":"rate_limited"`)
}

func TestProtectionOffDisablesPolicyLimits(t *testing.T) {
	stack := newProtectedStack(t, map[string]string{"CORTEX_PROTECTION": "off"})
	for i := range 8 {
		rec := stack.json(http.MethodPost, "/auth/v1/register", map[string]string{"email": "off" + string(rune('a'+i)) + "@example.com", "password": "password123", "display_name": "Test"}, nil)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	}
}

func TestTenancyCapsOwnedVaults(t *testing.T) {
	stack := newProtectedStack(t, map[string]string{"CORTEX_TENANCY_MODE": "multi", "CORTEX_TENANCY_MAX_VAULTS_PER_USER": "1"})
	headers := stack.session("owner@example.com")
	stack.vault(headers)

	rec := stack.json(http.MethodPost, "/vaults/v1/", map[string]string{"name": "Second"}, headers)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"limit_reached"`)
}

func TestLogoutRevokesAccessTokenImmediately(t *testing.T) {
	stack := newProtectedStack(t, nil)
	headers := stack.session("logout@example.com")
	require.Equal(t, http.StatusOK, stack.do(http.MethodGet, "/vaults/v1/", nil, headers).Code)

	rec := stack.json(http.MethodPost, "/auth/v1/logout", map[string]any{}, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = stack.do(http.MethodGet, "/vaults/v1/", nil, headers)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
