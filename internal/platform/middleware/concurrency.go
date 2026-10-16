package middleware

import (
	"net/http"
	"sync"
	"time"
)

const busyRetryAfter = 5 * time.Second

type ConcurrencyLimiter struct {
	mu     sync.Mutex
	global int
	perKey int
	active int
	byKey  map[string]int
}

func NewConcurrencyLimiter(global int, perKey int) *ConcurrencyLimiter {
	if global <= 0 && perKey <= 0 {
		return nil
	}
	return &ConcurrencyLimiter{global: global, perKey: perKey, byKey: make(map[string]int)}
}

func (c *ConcurrencyLimiter) Acquire(key string) (func(), bool) {
	if c == nil {
		return func() {}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.global > 0 && c.active >= c.global {
		return nil, false
	}
	if c.perKey > 0 && c.byKey[key] >= c.perKey {
		return nil, false
	}
	c.active++
	c.byKey[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.active--
			if c.byKey[key] <= 1 {
				delete(c.byKey, key)
				return
			}
			c.byKey[key]--
		})
	}, true
}

func LimitConcurrency(limiter *ConcurrencyLimiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	if limiter == nil {
		return passthrough
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			release, ok := limiter.Acquire(key(r))
			if !ok {
				WriteLimited(w, http.StatusServiceUnavailable, "server_busy", "server is busy, retry shortly", busyRetryAfter)
				return
			}
			defer release()
			next.ServeHTTP(w, r)
		})
	}
}
