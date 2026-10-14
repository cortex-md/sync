package subscription

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/google/uuid"
)

const defaultEntitlementCacheTTL = time.Minute

type EntitlementChecker struct {
	subs     SubscriptionRepository
	cache    sync.Map
	cacheTTL time.Duration
	now      func() time.Time
}

type entitlementCacheEntry struct {
	expiresAt time.Time
}

func NewEntitlementChecker(subs SubscriptionRepository, cacheTTL time.Duration) *EntitlementChecker {
	if cacheTTL <= 0 {
		cacheTTL = defaultEntitlementCacheTTL
	}
	return &EntitlementChecker{
		subs:     subs,
		cacheTTL: cacheTTL,
		now:      time.Now,
	}
}

type SubjectResolver func(r *http.Request) (uuid.UUID, error)

func (c *EntitlementChecker) Middleware(next http.Handler) http.Handler {
	return c.MiddlewareFor(nil)(next)
}

func (c *EntitlementChecker) MiddlewareFor(resolve SubjectResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return c.enforce(resolve, next)
	}
}

func (c *EntitlementChecker) enforce(resolve SubjectResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := httpx.ClaimsFromContext(r.Context())
		if claims == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		subject := claims.UserID
		if resolve != nil {
			resolved, err := resolve(r)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					httpx.WriteError(w, http.StatusNotFound, "not found")
					return
				}
				httpx.WriteError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			subject = resolved
		}

		if err := c.Check(r.Context(), subject); err != nil {
			handleSubscriptionError(r.Context(), w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *EntitlementChecker) Check(ctx context.Context, subject uuid.UUID) error {
	if c.isCached(subject) {
		return nil
	}
	sub, err := c.subs.GetByUserID(ctx, subject)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrSubscriptionRequired
	}
	if err != nil {
		return err
	}
	now := c.now()
	if !sub.IsActiveAt(now) {
		return sub.AccessError()
	}
	c.store(subject, sub, now)
	return nil
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
