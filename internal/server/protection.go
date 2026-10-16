package server

import (
	"crypto/sha256"
	"net/http"

	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/feature"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/platform/middleware"
	"github.com/cortexnotes/cortex-sync/internal/realtime"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"golang.org/x/time/rate"
)

type protection struct {
	clientIP      func(http.Handler) http.Handler
	global        func(http.Handler) http.Handler
	sensitive     func(http.Handler) http.Handler
	registrations func(http.Handler) http.Handler
	public        func(http.Handler) http.Handler
	user          func(http.Handler) http.Handler
	heavy         func(http.Handler) http.Handler
	upload        func(http.Handler) http.Handler
	batch         func(http.Handler) http.Handler
	streams       func(http.Handler) http.Handler
	avatarBody    func(http.Handler) http.Handler
	collabSockets *middleware.ConcurrencyLimiter
}

func userKey(r *http.Request) string {
	if claims := httpx.ClaimsFromContext(r.Context()); claims != nil {
		return claims.UserID.String()
	}
	return "ip:" + middleware.ClientIPFrom(r)
}

func perSecond(value float64) rate.Limit {
	return rate.Limit(value)
}

func perHour(count int64) rate.Limit {
	return rate.Limit(float64(count) / 3600)
}

func chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

func newProtection(cfg *config.Config) protection {
	pc := cfg.Protection
	keys := pc.RateLimitMaxKeys
	read := middleware.NewKeyedLimiter(perSecond(pc.UserReadPerSecond), pc.UserReadBurst, keys)
	write := middleware.NewKeyedLimiter(perSecond(pc.UserWritePerSecond), pc.UserWriteBurst, keys)
	budget := middleware.NewKeyedLimiter(perHour(pc.UploadBytesPerHour), int(pc.UploadBytesPerHour), keys)

	return protection{
		clientIP:      middleware.ClientIP(middleware.NewIPResolver(cfg.Proxy.Mode, cfg.Proxy.Hops)),
		global:        middleware.LimitByIP(middleware.NewKeyedLimiter(perSecond(cfg.RateLimit.RequestsPerSecond), cfg.RateLimit.Burst, keys)),
		sensitive:     middleware.LimitByIP(middleware.NewKeyedLimiter(perSecond(cfg.RateLimit.SensitiveRequestsPerSecond), cfg.RateLimit.SensitiveBurst, keys)),
		registrations: middleware.LimitByIP(middleware.NewKeyedLimiter(perHour(int64(pc.RegistrationsPerHour)), pc.RegistrationsPerHour, keys)),
		public:        middleware.LimitByIP(middleware.NewKeyedLimiter(perSecond(pc.PublicPerSecond), pc.PublicBurst, keys)),
		user: func(next http.Handler) http.Handler {
			reads := middleware.LimitBy(read, userKey)(next)
			writes := middleware.LimitBy(write, userKey)(next)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet || r.Method == http.MethodHead {
					reads.ServeHTTP(w, r)
					return
				}
				writes.ServeHTTP(w, r)
			})
		},
		heavy: middleware.LimitBy(middleware.NewKeyedLimiter(perSecond(pc.UserHeavyPerSecond), pc.UserHeavyBurst, keys), userKey),
		upload: chain(
			middleware.RequireContentLength(cfg.Server.MaxUploadBytes),
			middleware.AllowBody(cfg.Server.MaxUploadBytes),
			middleware.LimitConcurrency(middleware.NewConcurrencyLimiter(pc.MaxConcurrentUploads, pc.MaxConcurrentUploadsPerUser), userKey),
			middleware.LimitBytes(budget, userKey),
		),
		batch:         middleware.LimitConcurrency(middleware.NewConcurrencyLimiter(0, pc.MaxConcurrentBatchesPerUser), userKey),
		streams:       middleware.LimitConcurrency(middleware.NewConcurrencyLimiter(pc.MaxSSETotal, pc.MaxSSEPerUser), userKey),
		avatarBody:    middleware.AllowBody(cfg.Avatar.MaxBytes + 4096),
		collabSockets: middleware.NewConcurrencyLimiter(0, pc.MaxCollabPerUser),
	}
}

func (p protection) firstPage(limit func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		limited := limit(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") != "" {
				next.ServeHTTP(w, r)
				return
			}
			limited.ServeHTTP(w, r)
		})
	}
}

func connectionLimiter(limiter *middleware.ConcurrencyLimiter) realtime.ConnectionLimiter {
	if limiter == nil {
		return nil
	}
	return limiter
}

func derivedKey(secret string, label string) []byte {
	sum := sha256.Sum256([]byte(label + ":" + secret))
	return sum[:]
}

func inviteEmailTrust(features feature.Set) vault.EmailTrust {
	if features.Mail {
		return vault.EmailTrustVerified
	}
	return vault.EmailTrustNone
}
