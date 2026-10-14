package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu                sync.Mutex
	entries           map[string]*ipEntry
	r                 rate.Limit
	burst             int
	trustProxyHeaders bool
}

type RateLimiterOption func(*RateLimiter)

func WithTrustedProxyHeaders(enabled bool) RateLimiterOption {
	return func(rl *RateLimiter) {
		rl.trustProxyHeaders = enabled
	}
}

func NewRateLimiter(r rate.Limit, burst int, options ...RateLimiterOption) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*ipEntry),
		r:       r,
		burst:   burst,
	}
	for _, option := range options {
		option(rl)
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.entries[ip]
	if !exists {
		entry = &ipEntry{limiter: rate.NewLimiter(rl.r, rl.burst)}
		rl.entries[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		threshold := time.Now().Add(-10 * time.Minute)
		for ip, entry := range rl.entries {
			if entry.lastSeen.Before(threshold) {
				delete(rl.entries, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) clientIP(r *http.Request) string {
	if rl.trustProxyHeaders {
		if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
			firstIP := strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
			if net.ParseIP(firstIP) != nil {
				return firstIP
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
			return realIP
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := rl.clientIP(r)

		if !rl.getLimiter(ip).Allow() {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
