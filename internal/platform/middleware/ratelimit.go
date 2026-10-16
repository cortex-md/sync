package middleware

import (
	"container/list"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type limiterEntry struct {
	key     string
	limiter *rate.Limiter
}

type KeyedLimiter struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
	r       rate.Limit
	burst   int
	maxKeys int
}

func NewKeyedLimiter(r rate.Limit, burst int, maxKeys int) *KeyedLimiter {
	if r <= 0 || burst <= 0 {
		return nil
	}
	return &KeyedLimiter{entries: make(map[string]*list.Element), order: list.New(), r: r, burst: burst, maxKeys: max(maxKeys, 1)}
}

func (l *KeyedLimiter) Allow(key string) (bool, time.Duration) {
	return l.AllowN(key, 1)
}

func (l *KeyedLimiter) AllowN(key string, n int) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	if n > l.burst {
		return false, -1
	}
	now := time.Now()
	reservation := l.limiter(key).ReserveN(now, n)
	if delay := reservation.DelayFrom(now); delay > 0 {
		reservation.CancelAt(now)
		return false, delay
	}
	return true, 0
}

func (l *KeyedLimiter) Burst() int {
	if l == nil {
		return 0
	}
	return l.burst
}

func (l *KeyedLimiter) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.order.Len()
}

func (l *KeyedLimiter) limiter(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if element, ok := l.entries[key]; ok {
		l.order.MoveToFront(element)
		return element.Value.(*limiterEntry).limiter
	}
	for l.order.Len() >= l.maxKeys {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.entries, oldest.Value.(*limiterEntry).key)
	}
	entry := &limiterEntry{key: key, limiter: rate.NewLimiter(l.r, l.burst)}
	l.entries[key] = l.order.PushFront(entry)
	return entry.limiter
}

func LimitBy(limiter *KeyedLimiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	if limiter == nil {
		return passthrough
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ok, retry := limiter.Allow(key(r)); !ok {
				WriteLimited(w, http.StatusTooManyRequests, "rate_limited", "too many requests", retry)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func LimitByIP(limiter *KeyedLimiter) func(http.Handler) http.Handler {
	return LimitBy(limiter, ClientIPFrom)
}

func WriteLimited(w http.ResponseWriter, status int, code string, message string, retry time.Duration) {
	if retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}

func passthrough(next http.Handler) http.Handler {
	return next
}

func LimitBytes(limiter *KeyedLimiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	if limiter == nil {
		return passthrough
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > 0 {
				ok, retry := limiter.AllowN(key(r), int(r.ContentLength))
				if !ok && retry < 0 {
					WriteLimited(w, http.StatusRequestEntityTooLarge, "upload_budget_exceeded", "upload exceeds the hourly upload budget", 0)
					return
				}
				if !ok {
					WriteLimited(w, http.StatusTooManyRequests, "upload_budget_exceeded", "hourly upload budget exhausted", retry)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
