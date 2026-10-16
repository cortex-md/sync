package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/platform/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

func serveFrom(handler http.Handler, remote string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestLimitByIPBlocksOverLimitWithRetryAfter(t *testing.T) {
	limiter := middleware.NewKeyedLimiter(rate.Limit(1), 1, 100)
	handler := middleware.ClientIP(middleware.NewIPResolver("none", 0))(middleware.LimitByIP(limiter)(okHandler()))

	assert.Equal(t, http.StatusOK, serveFrom(handler, "192.168.1.1:5678", nil).Code)
	blocked := serveFrom(handler, "192.168.1.1:5678", nil)
	assert.Equal(t, http.StatusTooManyRequests, blocked.Code)
	assert.Equal(t, "1", blocked.Header().Get("Retry-After"))
	assert.Contains(t, blocked.Body.String(), `"code":"rate_limited"`)
	assert.Equal(t, http.StatusOK, serveFrom(handler, "192.168.1.2:5678", nil).Code)
}

func TestNilLimiterIsPassthrough(t *testing.T) {
	handler := middleware.LimitByIP(middleware.NewKeyedLimiter(0, 0, 100))(okHandler())
	for range 5 {
		assert.Equal(t, http.StatusOK, serveFrom(handler, "10.0.0.1:1", nil).Code)
	}
}

func TestHopsResolverIgnoresSpoofedLeftmostAddress(t *testing.T) {
	resolver := middleware.NewIPResolver("hops", 1)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
	assert.Equal(t, "203.0.113.9", resolver.Resolve(req))

	two := middleware.NewIPResolver("hops", 2)
	assert.Equal(t, "1.2.3.4", two.Resolve(req))

	req.Header.Del("X-Forwarded-For")
	assert.Equal(t, "10.0.0.5", resolver.Resolve(req))
}

func TestNoneResolverIgnoresForwardedHeaders(t *testing.T) {
	resolver := middleware.NewIPResolver("none", 0)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")
	assert.Equal(t, "10.0.0.5", resolver.Resolve(req))
}

func TestCloudflareResolverUsesConnectingIP(t *testing.T) {
	resolver := middleware.NewIPResolver("cloudflare", 0)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.68.0.1:443"
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")
	assert.Equal(t, "198.51.100.7", resolver.Resolve(req))
}

func TestKeyedLimiterEvictsLeastRecentlyUsedKeys(t *testing.T) {
	limiter := middleware.NewKeyedLimiter(rate.Limit(1), 1, 3)
	for i := range 10 {
		ok, _ := limiter.Allow("ip-" + strconv.Itoa(i))
		require.True(t, ok)
	}
	assert.Equal(t, 3, limiter.Len())
}

func TestLimitBytesChargesContentLength(t *testing.T) {
	limiter := middleware.NewKeyedLimiter(rate.Limit(1), 10, 100)
	handler := middleware.LimitBytes(limiter, func(*http.Request) string { return "user" })(okHandler())
	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	assert.Equal(t, http.StatusOK, send("12345678").Code)
	assert.Equal(t, http.StatusTooManyRequests, send("12345678").Code)
	assert.Equal(t, http.StatusRequestEntityTooLarge, send(strings.Repeat("x", 11)).Code)
}

func TestConcurrencyLimiterEnforcesGlobalAndPerKey(t *testing.T) {
	limiter := middleware.NewConcurrencyLimiter(3, 2)
	releaseA1, ok := limiter.Acquire("a")
	require.True(t, ok)
	_, ok = limiter.Acquire("a")
	require.True(t, ok)
	_, ok = limiter.Acquire("a")
	require.False(t, ok)
	_, ok = limiter.Acquire("b")
	require.True(t, ok)
	_, ok = limiter.Acquire("c")
	require.False(t, ok)
	releaseA1()
	releaseA1()
	_, ok = limiter.Acquire("c")
	require.True(t, ok)
}
