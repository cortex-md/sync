package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/adapter/middleware"
	"github.com/stretchr/testify/assert"
)

func TestBodyLimitLimitsJSONRequests(t *testing.T) {
	handler := middleware.BodyLimit(4, 10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := r.Body.Read(make([]byte, 8))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestBodyLimitUsesUploadLimitForOctetStream(t *testing.T) {
	handler := middleware.BodyLimit(4, 10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := r.Body.Read(make([]byte, 8))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345678"))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
