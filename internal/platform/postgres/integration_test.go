//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/config"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres"
	"github.com/cortexnotes/cortex-sync/internal/platform/txhook"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("CORTEX_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CORTEX_TEST_DATABASE_URL is not set")
	}
	cfg := config.DatabaseConfig{URL: url, MaxConns: 4, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute}
	require.NoError(t, postgres.RunMigrationCommand(cfg, []string{"up"}))
	pool, err := postgres.NewPool(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func seedDevice(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) uuid.UUID {
	t.Helper()
	device := &domain.Device{ID: uuid.New(), UserID: userID, DeviceName: "d", DeviceType: "desktop", DeviceToken: uuid.NewString(), LastSeenAt: time.Now(), CreatedAt: time.Now()}
	require.NoError(t, postgres.NewDeviceRepository(pool).Create(context.Background(), device))
	return device.ID
}

func seedVault(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	user := &domain.User{ID: uuid.New(), Email: uuid.NewString() + "@example.com", PasswordHash: "sha256$x", DisplayName: "T", Locale: "en", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, postgres.NewUserRepository(pool).Create(ctx, user))
	vault := &domain.Vault{ID: uuid.New(), Name: "v", OwnerID: user.ID, AllowGuests: true, AllowPublicLinks: true, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, postgres.NewVaultRepository(pool).Create(ctx, vault))
	return user.ID, vault.ID
}

func TestOutboxClaimUsesLeasesAndDeadLetters(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	outbox := postgres.NewEmailOutboxRepository(pool)
	email := &domain.OutboxEmail{To: "lease@example.com", Template: "password_changed", Locale: "en"}
	require.NoError(t, outbox.Enqueue(ctx, email))

	claimed, err := outbox.Claim(ctx, email.ID, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, claimed.Attempts)

	_, err = outbox.Claim(ctx, email.ID, time.Minute)
	assert.ErrorIs(t, err, domain.ErrNotFound, "a leased email is not claimable twice")

	require.NoError(t, outbox.MarkRetry(ctx, email.ID, "smtp down", time.Now().Add(-time.Second)))
	due, err := outbox.ClaimDue(ctx, 100, time.Minute)
	require.NoError(t, err)
	found := false
	for _, e := range due {
		found = found || e.ID == email.ID
	}
	assert.True(t, found)

	require.NoError(t, outbox.MarkDead(ctx, email.ID, "rejected"))
	_, err = pool.Exec(ctx, `UPDATE email_outbox SET next_attempt_at = now() - interval '1 hour' WHERE id = $1`, email.ID)
	require.NoError(t, err)
	_, err = outbox.Claim(ctx, email.ID, time.Minute)
	assert.ErrorIs(t, err, domain.ErrNotFound, "dead letters are never claimed")

	deleted, err := outbox.DeleteFinished(ctx, time.Now(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(1))
}

func TestTransactorRollbackDropsAfterCommitHooks(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	tx := postgres.NewTransactor(pool)
	outbox := postgres.NewEmailOutboxRepository(pool)
	hookRan := false
	failure := errors.New("abort")

	var id int64
	err := tx.RunInTx(ctx, func(ctx context.Context) error {
		email := &domain.OutboxEmail{To: "rollback@example.com", Template: "password_changed", Locale: "en"}
		if err := outbox.Enqueue(ctx, email); err != nil {
			return err
		}
		id = email.ID
		txhook.AfterCommit(ctx, func() { hookRan = true })
		return failure
	})

	assert.ErrorIs(t, err, failure)
	assert.False(t, hookRan)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE id = $1`, id).Scan(&count))
	assert.Zero(t, count)

	require.NoError(t, tx.RunInTx(ctx, func(ctx context.Context) error {
		txhook.AfterCommit(ctx, func() { hookRan = true })
		return nil
	}))
	assert.True(t, hookRan)
}

func TestSyncEventRetentionAdvancesWatermark(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID, vaultID := seedVault(t, pool)
	deviceID := seedDevice(t, pool, userID)
	events := postgres.NewSyncEventRepository(pool)
	var last int64
	for range 3 {
		event := &domain.SyncEvent{VaultID: vaultID, EventType: domain.EventFileCreated, FilePath: "a.md", Version: 1, ActorID: userID, DeviceID: deviceID, CreatedAt: time.Now().Add(-48 * time.Hour)}
		require.NoError(t, events.Create(ctx, event))
		last = event.ID
	}

	deleted, err := events.DeleteOlderThan(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(3))

	pruned, err := events.PrunedThrough(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, pruned, last)
}

func TestDeltaDeletionReturnsFreedBytes(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID, vaultID := seedVault(t, pool)
	deviceID := seedDevice(t, pool, userID)
	deltas := postgres.NewFileDeltaRepository(pool)
	for version := 1; version <= 3; version++ {
		require.NoError(t, deltas.Create(ctx, &domain.FileDelta{
			ID: uuid.New(), VaultID: vaultID, FilePath: "a.md", BaseVersion: version, TargetVersion: version + 1,
			EncryptedDelta: []byte("12345"), SizeBytes: 5, CreatedBy: userID, DeviceID: deviceID, CreatedAt: time.Now(),
		}))
	}

	freed, err := deltas.DeleteByFilePath(ctx, vaultID, "a.md", 4)
	require.NoError(t, err)
	assert.EqualValues(t, 10, freed)
}

func TestCollabCompactionRejectsOlderState(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	_, vaultID := seedVault(t, pool)
	docs := postgres.NewCollabDocumentRepository(pool)
	for i := range 3 {
		require.NoError(t, docs.StoreUpdate(ctx, &domain.CollabUpdate{VaultID: vaultID, FilePath: "a.md", Data: []byte{byte(i)}}))
	}
	_, updates, err := docs.LoadDocument(ctx, vaultID, "a.md")
	require.NoError(t, err)
	require.Len(t, updates, 3)

	require.NoError(t, docs.CompactDocument(ctx, vaultID, "a.md", []byte("newer"), updates[2].ID))
	assert.ErrorIs(t, docs.CompactDocument(ctx, vaultID, "a.md", []byte("older"), updates[0].ID), domain.ErrConflict)

	doc, remaining, err := docs.LoadDocument(ctx, vaultID, "a.md")
	require.NoError(t, err)
	assert.Equal(t, []byte("newer"), doc.CompactedState)
	assert.Empty(t, remaining)
}

func TestRefreshTokenRotationRecordsTimestamp(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID, _ := seedVault(t, pool)
	deviceID := seedDevice(t, pool, userID)
	tokens := postgres.NewRefreshTokenRepository(pool)
	token := &domain.RefreshToken{ID: uuid.New(), UserID: userID, DeviceID: deviceID, TokenHash: uuid.NewString(), FamilyID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	require.NoError(t, tokens.Create(ctx, token))

	consumed, err := tokens.RevokeIfActive(ctx, token.ID)
	require.NoError(t, err)
	require.True(t, consumed)

	stored, err := tokens.GetByTokenHash(ctx, token.TokenHash)
	require.NoError(t, err)
	assert.True(t, stored.Revoked)
	require.NotNil(t, stored.RotatedAt)
}

func TestPendingInvitesAreUniquePerEmailAndExpiredOnesAreReplaced(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID, vaultID := seedVault(t, pool)
	invites := postgres.NewVaultInviteRepository(pool)
	newInvite := func(expires time.Time) *domain.VaultInvite {
		return &domain.VaultInvite{ID: uuid.New(), VaultID: vaultID, InviterID: userID, InviteeEmail: "Dup@Example.com", Role: domain.VaultRoleEditor, TokenHash: uuid.NewString(), ExpiresAt: expires, CreatedAt: time.Now()}
	}

	require.NoError(t, invites.Create(ctx, newInvite(time.Now().Add(-time.Minute))))
	require.NoError(t, invites.Create(ctx, newInvite(time.Now().Add(time.Hour))), "an expired pending invite is replaced")
	assert.ErrorIs(t, invites.Create(ctx, newInvite(time.Now().Add(time.Hour))), domain.ErrAlreadyExists)

	pending, err := invites.ListPendingByVault(ctx, vaultID)
	require.NoError(t, err)
	assert.Len(t, pending, 1)
}

func TestReconcileStorageRecomputesUsage(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	userID, vaultID := seedVault(t, pool)
	deviceID := seedDevice(t, pool, userID)
	require.NoError(t, postgres.NewFileDeltaRepository(pool).Create(ctx, &domain.FileDelta{
		ID: uuid.New(), VaultID: vaultID, FilePath: "a.md", BaseVersion: 1, TargetVersion: 2,
		EncryptedDelta: []byte("1234567"), SizeBytes: 7, CreatedBy: userID, DeviceID: deviceID, CreatedAt: time.Now(),
	}))
	_, err := pool.Exec(ctx, `UPDATE vaults SET storage_used_bytes = 999 WHERE id = $1`, vaultID)
	require.NoError(t, err)

	_, err = postgres.NewVaultRepository(pool).ReconcileStorage(ctx)
	require.NoError(t, err)

	var used int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT storage_used_bytes FROM vaults WHERE id = $1`, vaultID).Scan(&used))
	assert.EqualValues(t, 7, used)
}
