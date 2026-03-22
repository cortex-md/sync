package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
)

type subscriptionCache struct {
	entries sync.Map
	ttl     time.Duration
}

type cacheEntry struct {
	active    bool
	expiresAt time.Time
}

func SubscriptionMiddleware(subs port.SubscriptionRepository, cacheTTL time.Duration) func(http.Handler) http.Handler {
	cache := &subscriptionCache{ttl: cacheTTL}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetAuthClaims(r.Context())
			if claims == nil {
				WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			userID := claims.UserID

			if val, ok := cache.entries.Load(userID); ok {
				entry := val.(*cacheEntry)
				if time.Now().Before(entry.expiresAt) {
					if entry.active {
						next.ServeHTTP(w, r)
						return
					}
					WriteErrorWithCode(w, http.StatusPaymentRequired, "subscription required", "subscription_required")
					return
				}
			}

			sub, err := subs.GetByUserID(r.Context(), userID)
			if err != nil {
				if err == domain.ErrNotFound {
					cache.entries.Store(userID, &cacheEntry{active: false, expiresAt: time.Now().Add(cache.ttl)})
					WriteErrorWithCode(w, http.StatusPaymentRequired, "subscription required", "subscription_required")
					return
				}
				WriteError(w, http.StatusInternalServerError, "internal server error")
				return
			}

			active := sub.IsActive()
			cache.entries.Store(userID, &cacheEntry{active: active, expiresAt: time.Now().Add(cache.ttl)})

			if !active {
				WriteErrorWithCode(w, http.StatusPaymentRequired, "subscription expired", "subscription_expired")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
