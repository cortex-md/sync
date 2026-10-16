package revocation

import (
	"context"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestRegistryRevokesTokensIssuedBeforeRevocation(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	registry := New(15*time.Minute, nil)
	registry.now = func() time.Time { return now }
	user, device := uuid.New(), uuid.New()
	old := &httpx.Claims{UserID: user, DeviceID: device, IssuedAt: now.Add(-time.Minute)}

	registry.RevokeDevice(context.Background(), device)
	assert.True(t, registry.Revoked(old))
	assert.False(t, registry.Revoked(&httpx.Claims{UserID: user, DeviceID: device, IssuedAt: now.Add(time.Second)}))
	assert.False(t, registry.Revoked(&httpx.Claims{UserID: user, DeviceID: uuid.New(), IssuedAt: now.Add(-time.Minute)}))

	registry.RevokeUser(context.Background(), user)
	assert.True(t, registry.Revoked(&httpx.Claims{UserID: user, DeviceID: uuid.New(), IssuedAt: now.Add(-time.Minute)}))
}

func TestRegistryPropagatesThroughBus(t *testing.T) {
	signals := bus.NewMemory()
	origin := New(15*time.Minute, signals)
	replica := New(15*time.Minute, signals)
	device := uuid.New()

	origin.RevokeDevice(context.Background(), device)

	assert.True(t, replica.Revoked(&httpx.Claims{DeviceID: device, IssuedAt: time.Now().Add(-time.Minute)}))
}

func TestRegistryUserRevocationCanKeepTheCurrentDevice(t *testing.T) {
	registry := New(15*time.Minute, nil)
	user, current, other := uuid.New(), uuid.New(), uuid.New()
	issued := time.Now().Add(-time.Minute)

	registry.RevokeUserExcept(context.Background(), user, current)

	assert.False(t, registry.Revoked(&httpx.Claims{UserID: user, DeviceID: current, IssuedAt: issued}))
	assert.True(t, registry.Revoked(&httpx.Claims{UserID: user, DeviceID: other, IssuedAt: issued}))
}
