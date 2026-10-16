package revocation

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	scopeDevice = "device"
	scopeUser   = "user"
)

type userRevocation struct {
	at     time.Time
	except uuid.UUID
}

type Registry struct {
	mu      sync.RWMutex
	devices map[uuid.UUID]time.Time
	users   map[uuid.UUID]userRevocation
	ttl     time.Duration
	bus     bus.Bus
	now     func() time.Time
}

func New(tokenTTL time.Duration, signals bus.Bus) *Registry {
	r := &Registry{
		devices: make(map[uuid.UUID]time.Time),
		users:   make(map[uuid.UUID]userRevocation),
		ttl:     tokenTTL + time.Minute,
		bus:     signals,
		now:     time.Now,
	}
	if signals != nil {
		signals.Subscribe(bus.KindTokensRevoked, r.apply)
	}
	return r
}

func (r *Registry) RevokeDevice(ctx context.Context, deviceID uuid.UUID) {
	r.revoke(ctx, scopeDevice, deviceID, uuid.Nil)
}

func (r *Registry) RevokeUser(ctx context.Context, userID uuid.UUID) {
	r.revoke(ctx, scopeUser, userID, uuid.Nil)
}

func (r *Registry) RevokeUserExcept(ctx context.Context, userID uuid.UUID, keepDevice uuid.UUID) {
	r.revoke(ctx, scopeUser, userID, keepDevice)
}

func (r *Registry) Revoked(claims *httpx.Claims) bool {
	if r == nil || claims == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if issuedBefore(claims.IssuedAt, r.devices[claims.DeviceID]) {
		return true
	}
	user, ok := r.users[claims.UserID]
	return ok && claims.DeviceID != user.except && issuedBefore(claims.IssuedAt, user.at)
}

func (r *Registry) revoke(ctx context.Context, scope string, id uuid.UUID, except uuid.UUID) {
	if r == nil {
		return
	}
	at := r.now()
	r.store(scope, id, at, except)
	if r.bus == nil {
		return
	}
	signal := bus.Signal{Kind: bus.KindTokensRevoked, Event: scope, ClientID: id.String(), UserID: except, Data: []byte(strconv.FormatInt(at.UnixNano(), 10))}
	if err := r.bus.Publish(context.WithoutCancel(ctx), signal); err != nil {
		log.Warn().Err(err).Str("scope", scope).Msg("revocation: publish failed")
	}
}

func (r *Registry) apply(signal bus.Signal) {
	id, err := uuid.Parse(signal.ClientID)
	if err != nil {
		return
	}
	nanos, err := strconv.ParseInt(string(signal.Data), 10, 64)
	if err != nil {
		return
	}
	r.store(signal.Event, id, time.Unix(0, nanos), signal.UserID)
}

func (r *Registry) store(scope string, id uuid.UUID, at time.Time, except uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch scope {
	case scopeDevice:
		if existing, ok := r.devices[id]; !ok || at.After(existing) {
			r.devices[id] = at
		}
	case scopeUser:
		if existing, ok := r.users[id]; !ok || at.After(existing.at) {
			r.users[id] = userRevocation{at: at, except: except}
		}
	default:
		return
	}
	cutoff := r.now().Add(-r.ttl)
	for key, revokedAt := range r.devices {
		if revokedAt.Before(cutoff) {
			delete(r.devices, key)
		}
	}
	for key, revoked := range r.users {
		if revoked.at.Before(cutoff) {
			delete(r.users, key)
		}
	}
}

func issuedBefore(issuedAt time.Time, revokedAt time.Time) bool {
	return !revokedAt.IsZero() && !issuedAt.After(revokedAt)
}

type Validator struct {
	inner    httpx.TokenValidator
	registry *Registry
}

func NewValidator(inner httpx.TokenValidator, registry *Registry) *Validator {
	return &Validator{inner: inner, registry: registry}
}

func (v *Validator) ValidateAccessToken(token string) (*httpx.Claims, error) {
	claims, err := v.inner.ValidateAccessToken(token)
	if err != nil {
		return nil, err
	}
	if v.registry.Revoked(claims) {
		return nil, ErrRevoked
	}
	return claims, nil
}
