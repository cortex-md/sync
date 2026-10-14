package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	server.HealthCheck(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp server.HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
}

func TestReadinessCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.NewReadinessHandler(nil, fake.NewBlobStorage(), nil).Check(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp server.HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "ready", resp.Status)
}
