package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

const defaultEntitlementCacheTTL = time.Minute

type EntitlementChecker struct {
	subs     port.SubscriptionRepository
	cache    sync.Map
	cacheTTL time.Duration
	now      func() time.Time
}

type entitlementCacheEntry struct {
	expiresAt time.Time
}

func NewEntitlementChecker(subs port.SubscriptionRepository, cacheTTL time.Duration) *EntitlementChecker {
	if cacheTTL <= 0 {
		cacheTTL = defaultEntitlementCacheTTL
	}
	return &EntitlementChecker{
		subs:     subs,
		cacheTTL: cacheTTL,
		now:      time.Now,
	}
}

func (c *EntitlementChecker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetAuthClaims(r.Context())
		if claims == nil {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		if c.isCached(claims.UserID) {
			next.ServeHTTP(w, r)
			return
		}

		sub, err := c.subs.GetByUserID(r.Context(), claims.UserID)
		if err != nil {
			if err == domain.ErrNotFound {
				WriteErrorWithCode(w, http.StatusPaymentRequired, "subscription required", "subscription_required")
				return
			}
			WriteError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		now := c.now()
		if !sub.IsActiveAt(now) {
			handleSubscriptionError(r.Context(), w, sub.AccessError())
			return
		}

		c.store(claims.UserID, sub, now)
		next.ServeHTTP(w, r)
	})
}

func (c *EntitlementChecker) Invalidate(userID uuid.UUID) {
	c.cache.Delete(userID)
}

func (c *EntitlementChecker) isCached(userID uuid.UUID) bool {
	val, ok := c.cache.Load(userID)
	if !ok {
		return false
	}
	entry := val.(*entitlementCacheEntry)
	if c.now().Before(entry.expiresAt) {
		return true
	}
	c.cache.Delete(userID)
	return false
}

func (c *EntitlementChecker) store(userID uuid.UUID, sub *domain.Subscription, now time.Time) {
	expiresAt := now.Add(c.cacheTTL)
	subExpiresAt := sub.EntitlementExpiresAt
	if subExpiresAt.IsZero() {
		subExpiresAt = sub.CurrentPeriodEnd
	}
	if !subExpiresAt.IsZero() && subExpiresAt.Before(expiresAt) {
		expiresAt = subExpiresAt
	}
	if !expiresAt.After(now) {
		return
	}
	c.cache.Store(userID, &entitlementCacheEntry{expiresAt: expiresAt})
}
