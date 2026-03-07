package job_test

import (
	"context"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/job"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleaner_DeletesExpiredRefreshTokens(t *testing.T) {
	tokenRepo := fake.NewRefreshTokenRepository()
	inviteRepo := fake.NewVaultInviteRepository()

	expired := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		DeviceID:  uuid.New(),
		FamilyID:  uuid.New(),
		TokenHash: "expired-hash",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	active := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		DeviceID:  uuid.New(),
		FamilyID:  uuid.New(),
		TokenHash: "active-hash",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	require.NoError(t, tokenRepo.Create(context.Background(), expired))
	require.NoError(t, tokenRepo.Create(context.Background(), active))

	cleaner := job.NewCleaner(tokenRepo, inviteRepo, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cleaner.Run(ctx)

	_, err := tokenRepo.GetByTokenHash(context.Background(), "expired-hash")
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = tokenRepo.GetByTokenHash(context.Background(), "active-hash")
	assert.NoError(t, err)
}

func TestCleaner_DeletesExpiredInvites(t *testing.T) {
	tokenRepo := fake.NewRefreshTokenRepository()
	inviteRepo := fake.NewVaultInviteRepository()

	expiredID := uuid.New()
	activeID := uuid.New()

	expiredInvite := &domain.VaultInvite{
		ID:           expiredID,
		VaultID:      uuid.New(),
		InviterID:    uuid.New(),
		InviteeEmail: "expired@test.com",
		Role:         domain.VaultRoleViewer,
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
	}
	activeInvite := &domain.VaultInvite{
		ID:           activeID,
		VaultID:      uuid.New(),
		InviterID:    uuid.New(),
		InviteeEmail: "active@test.com",
		Role:         domain.VaultRoleViewer,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	require.NoError(t, inviteRepo.Create(context.Background(), expiredInvite))
	require.NoError(t, inviteRepo.Create(context.Background(), activeInvite))

	cleaner := job.NewCleaner(tokenRepo, inviteRepo, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cleaner.Run(ctx)

	_, err := inviteRepo.GetByID(context.Background(), expiredID)
	assert.ErrorIs(t, err, domain.ErrNotFound)

	_, err = inviteRepo.GetByID(context.Background(), activeID)
	assert.NoError(t, err)
}

func TestCleaner_StopsOnContextCancel(t *testing.T) {
	tokenRepo := fake.NewRefreshTokenRepository()
	inviteRepo := fake.NewVaultInviteRepository()

	cleaner := job.NewCleaner(tokenRepo, inviteRepo, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		cleaner.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("cleaner did not stop after context cancellation")
	}
}

func TestCleaner_DeletesOldSyncEvents(t *testing.T) {
	tokenRepo := fake.NewRefreshTokenRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	eventRepo := fake.NewSyncEventRepository()

	vaultID := uuid.New()
	actorID := uuid.New()
	deviceID := uuid.New()

	old := &domain.SyncEvent{
		VaultID:   vaultID,
		EventType: domain.EventFileCreated,
		FilePath:  "old.md",
		Version:   1,
		ActorID:   actorID,
		DeviceID:  deviceID,
		Metadata:  map[string]any{},
		CreatedAt: time.Now().Add(-40 * 24 * time.Hour),
	}
	recent := &domain.SyncEvent{
		VaultID:   vaultID,
		EventType: domain.EventFileCreated,
		FilePath:  "recent.md",
		Version:   1,
		ActorID:   actorID,
		DeviceID:  deviceID,
		Metadata:  map[string]any{},
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}

	require.NoError(t, eventRepo.Create(context.Background(), old))
	require.NoError(t, eventRepo.Create(context.Background(), recent))

	cleaner := job.NewCleaner(tokenRepo, inviteRepo, time.Hour)
	cleaner.SetSyncEvents(eventRepo, 30*24*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cleaner.Run(ctx)

	remaining, err := eventRepo.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, len(remaining))
	assert.Equal(t, "recent.md", remaining[0].FilePath)
}

func TestCleaner_ZeroRetentionSkipsSyncEventCleanup(t *testing.T) {
	tokenRepo := fake.NewRefreshTokenRepository()
	inviteRepo := fake.NewVaultInviteRepository()
	eventRepo := fake.NewSyncEventRepository()

	vaultID := uuid.New()
	actorID := uuid.New()
	deviceID := uuid.New()

	old := &domain.SyncEvent{
		VaultID:   vaultID,
		EventType: domain.EventFileCreated,
		FilePath:  "old.md",
		Version:   1,
		ActorID:   actorID,
		DeviceID:  deviceID,
		Metadata:  map[string]any{},
		CreatedAt: time.Now().Add(-100 * 24 * time.Hour),
	}
	require.NoError(t, eventRepo.Create(context.Background(), old))

	cleaner := job.NewCleaner(tokenRepo, inviteRepo, time.Hour)
	cleaner.SetSyncEvents(eventRepo, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cleaner.Run(ctx)

	remaining, err := eventRepo.ListByVaultID(context.Background(), vaultID, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, len(remaining))
}
