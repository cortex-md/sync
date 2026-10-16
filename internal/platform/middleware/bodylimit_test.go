package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/platform/middleware"
	"github.com/stretchr/testify/assert"
)

func readAllHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func post(handler http.Handler, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestBodyLimitAppliesDefaultLimit(t *testing.T) {
	handler := middleware.BodyLimit(4)(readAllHandler())
	assert.Equal(t, http.StatusRequestEntityTooLarge, post(handler, `{"a":1}`))
	assert.Equal(t, http.StatusOK, post(handler, `{}`))
}

func TestAllowBodyRaisesLimitForARoute(t *testing.T) {
	handler := middleware.BodyLimit(4)(middleware.AllowBody(10)(readAllHandler()))
	assert.Equal(t, http.StatusOK, post(handler, "12345678"))
	assert.Equal(t, http.StatusRequestEntityTooLarge, post(handler, "12345678901"))
}

func TestRequireContentLengthRejectsChunkedBodies(t *testing.T) {
	handler := middleware.RequireContentLength(10)(readAllHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abc"))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusLengthRequired, rec.Code)

	assert.Equal(t, http.StatusRequestEntityTooLarge, post(handler, "12345678901"))
	assert.Equal(t, http.StatusOK, post(handler, "123"))
}
