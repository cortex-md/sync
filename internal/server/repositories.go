package server

import (
	"github.com/cortexnotes/cortex-sync/internal/platform/fake"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repositories struct {
	Users         UserRepository
	Devices       DeviceRepository
	RefreshTokens RefreshTokenRepository
	UserTokens    UserTokenRepository
	Vaults        VaultRepository
	Members       VaultMemberRepository
	Access        VaultAccessRepository
	Invites       VaultInviteRepository
	Keys          VaultKeyRepository
	Encryption    VaultEncryptionRepository
	Snapshots     FileSnapshotRepository
	Deltas        FileDeltaRepository
	Latest        FileLatestRepository
	Events        SyncEventRepository
	CollabDocs    CollabDocumentRepository
	Presence      CollabPresenceRepository
	Subscriptions SubscriptionRepository
	Outbox        EmailOutboxRepository
	Shares        ShareRepository
	Templates     TemplateRepository
	Blobs         BlobStorage
	BlobQueue     BlobQueue
	Tx            Transactor
	Locker        Locker
}

func FakeRepositories() Repositories {
	users := fake.NewUserRepository()
	devices := fake.NewDeviceRepository()
	vaults := fake.NewVaultRepository()
	members := fake.NewVaultMemberRepository()
	invites := fake.NewVaultInviteRepository()
	keys := fake.NewVaultKeyRepository()
	snapshots := fake.NewFileSnapshotRepository()
	vaults.ResolveSummariesWith(members, keys)
	invites.ResolveDetailsWith(vaults, users)
	snapshots.ResolveAuthorsWith(users, devices)
	latest := fake.NewFileLatestRepository()
	members.ResolveFilesWith(latest)

	return Repositories{
		Users:         users,
		Devices:       devices,
		RefreshTokens: fake.NewRefreshTokenRepository(),
		UserTokens:    fake.NewUserTokenRepository(),
		Vaults:        vaults,
		Members:       members,
		Access:        members,
		Invites:       invites,
		Keys:          keys,
		Encryption:    fake.NewVaultEncryptionRepository(),
		Snapshots:     snapshots,
		Deltas:        fake.NewFileDeltaRepository(),
		Latest:        latest,
		Events:        fake.NewSyncEventRepository(),
		CollabDocs:    fake.NewCollabDocumentRepository(),
		Presence:      fake.NewCollabPresenceRepository(),
		Subscriptions: fake.NewSubscriptionRepository(),
		Outbox:        fake.NewEmailOutboxRepository(),
		Shares:        fake.NewShareRepository(),
		Templates:     fake.NewTemplateRepository(),
		Blobs:         fake.NewBlobStorage(),
		BlobQueue:     fake.NewBlobQueue(),
		Tx:            fake.NewTransactor(),
		Locker:        fake.NewLocker(),
	}
}

func PostgresRepositories(pool *pgxpool.Pool, blobs BlobStorage) Repositories {
	return Repositories{
		Users:         postgres.NewUserRepository(pool),
		Devices:       postgres.NewDeviceRepository(pool),
		RefreshTokens: postgres.NewRefreshTokenRepository(pool),
		UserTokens:    postgres.NewUserTokenRepository(pool),
		Vaults:        postgres.NewVaultRepository(pool),
		Members:       postgres.NewVaultMemberRepository(pool),
		Access:        postgres.NewVaultAccessRepository(pool),
		Invites:       postgres.NewVaultInviteRepository(pool),
		Keys:          postgres.NewVaultKeyRepository(pool),
		Encryption:    postgres.NewVaultEncryptionRepository(pool),
		Snapshots:     postgres.NewFileSnapshotRepository(pool),
		Deltas:        postgres.NewFileDeltaRepository(pool),
		Latest:        postgres.NewFileLatestRepository(pool),
		Events:        postgres.NewSyncEventRepository(pool),
		CollabDocs:    postgres.NewCollabDocumentRepository(pool),
		Presence:      postgres.NewCollabPresenceRepository(pool),
		Subscriptions: postgres.NewSubscriptionRepository(pool),
		Outbox:        postgres.NewEmailOutboxRepository(pool),
		Shares:        postgres.NewShareRepository(pool),
		Templates:     postgres.NewTemplateRepository(pool),
		Blobs:         blobs,
		BlobQueue:     postgres.NewBlobQueue(pool),
		Tx:            postgres.NewTransactor(pool),
		Locker:        postgres.NewLocker(pool),
	}
}
