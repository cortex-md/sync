package server

import (
	"context"
	"io"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/cortexnotes/cortex-sync/internal/share"
	"github.com/cortexnotes/cortex-sync/internal/template"
	"github.com/cortexnotes/cortex-sync/internal/vault"
	"github.com/google/uuid"
)

type BlobQueue interface {
	RegisterPending(ctx context.Context, key string) error
	ResolvePending(ctx context.Context, key string) error
	ClaimStalePending(ctx context.Context, before time.Time, limit int) ([]string, error)
	EnqueueDeletion(ctx context.Context, keys ...string) error
	ClaimDueDeletions(ctx context.Context, limit int) ([]domain.BlobDeletion, error)
	CompleteDeletion(ctx context.Context, id int64) error
	RetryDeletion(ctx context.Context, id int64, lastError string, next time.Time) error
}

type CollabDocumentRepository interface {
	StoreUpdate(ctx context.Context, update *domain.CollabUpdate) error
	BatchStoreUpdates(ctx context.Context, vaultID uuid.UUID, filePath string, updates [][]byte) error
	LoadDocument(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.CollabDocument, []domain.CollabUpdate, error)
	CompactDocument(ctx context.Context, vaultID uuid.UUID, filePath string, compactedState []byte, upToUpdateID int64) error
	DeleteForPath(ctx context.Context, vaultID uuid.UUID, filePath string) error
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}

type CollabPresenceRepository interface {
	Join(ctx context.Context, p *domain.CollabPresence) error
	Leave(ctx context.Context, clientID string) error
	Touch(ctx context.Context, clientIDs []string) error
	ListRoom(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.CollabPresence, error)
	ListVault(ctx context.Context, vaultID uuid.UUID) ([]domain.CollabPresence, error)
	DeleteStale(ctx context.Context, before time.Time) (int64, error)
}

type DeviceRepository interface {
	ListActiveByUsers(ctx context.Context, userIDs []uuid.UUID) ([]domain.Device, error)
	ListVaultCursorsByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.DeviceVaultCursor, error)
	Create(ctx context.Context, d *domain.Device) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Device, error)
	GetByToken(ctx context.Context, tokenHash string) (*domain.Device, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Device, error)
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.Device, error)
	UpdateLastSeen(ctx context.Context, id uuid.UUID) error
	Revoke(ctx context.Context, id uuid.UUID) error
	Update(ctx context.Context, d *domain.Device) error
	UpsertVaultCursor(ctx context.Context, deviceID, vaultID uuid.UUID, lastEventID int64) error
	ListVaultCursorsByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeviceVaultCursor, error)
}

type EmailOutboxRepository interface {
	Enqueue(ctx context.Context, email *domain.OutboxEmail) error
	Claim(ctx context.Context, id int64, lease time.Duration) (*domain.OutboxEmail, error)
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]domain.OutboxEmail, error)
	MarkSent(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, lastError string, nextAttemptAt time.Time) error
	MarkDead(ctx context.Context, id int64, lastError string) error
	DeleteFinished(ctx context.Context, sentBefore, deadBefore time.Time) (int64, error)
}

type FileDeltaRepository interface {
	Create(ctx context.Context, d *domain.FileDelta) error
	ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, sinceVersion, limit int) ([]domain.FileDelta, error)
	DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, beforeVersion int) (int64, error)
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}

type FileLatestRepository interface {
	Upsert(ctx context.Context, f *domain.FileLatest) error
	Get(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error)
	GetForUpdate(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error)
	ListByPaths(ctx context.Context, vaultID uuid.UUID, filePaths []string) ([]domain.FileLatest, error)
	List(ctx context.Context, query domain.FileListQuery) ([]domain.FileLatest, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceVersion int) ([]domain.FileLatest, error)
	Delete(ctx context.Context, vaultID uuid.UUID, filePath string) error
}

type FileSnapshotRepository interface {
	Create(ctx context.Context, s *domain.FileSnapshot) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.FileSnapshot, error)
	GetLatest(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileSnapshot, error)
	GetByVersion(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error)
	ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error)
	ListLatestByPaths(ctx context.Context, vaultID uuid.UUID, paths []string) ([]domain.FileSnapshot, error)
	ListAtOrBefore(ctx context.Context, vaultID uuid.UUID, paths []string, versions []int32) (map[int]domain.FileSnapshot, error)
	GetAtOrBefore(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error)
	ListByIDs(ctx context.Context, vaultID uuid.UUID, ids []uuid.UUID) ([]domain.FileSnapshot, error)
	ListHistory(ctx context.Context, vaultID uuid.UUID, filePath string, limit int) ([]domain.HistoryEntry, error)
	DeleteOlderVersions(ctx context.Context, vaultID uuid.UUID, filePath string, keepCount int) ([]domain.FileSnapshot, error)
	DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error)
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
	ListBlobKeysByVault(ctx context.Context, vaultID uuid.UUID) ([]string, error)
}

type Locker interface {
	TryLock(ctx context.Context, key int64) (bool, error)
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, t *domain.RefreshToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error)
	RevokeByID(ctx context.Context, id uuid.UUID) error
	RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error)
	RevokeByFamilyID(ctx context.Context, familyID uuid.UUID) error
	RevokeAllByUserID(ctx context.Context, userID uuid.UUID) error
	RevokeAllByDeviceID(ctx context.Context, deviceID uuid.UUID) error
	RevokeAllByUserExceptDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type SubscriptionRepository interface {
	Create(ctx context.Context, s *domain.Subscription) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Subscription, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error)
	GetByExternalSubscriptionID(ctx context.Context, externalID string) (*domain.Subscription, error)
	GetByExternalCheckoutID(ctx context.Context, externalID string) (*domain.Subscription, error)
	GetByExternalCustomerID(ctx context.Context, externalID string) (*domain.Subscription, error)
	Update(ctx context.Context, s *domain.Subscription) error
	CreateWebhookEvent(ctx context.Context, e *domain.SubscriptionWebhookEvent) error
	MarkWebhookEventProcessed(ctx context.Context, id string, processedAt time.Time) error
	DeleteWebhookEvent(ctx context.Context, id string) error
}

type SyncEventRepository interface {
	Create(ctx context.Context, event *domain.SyncEvent) error
	GetByID(ctx context.Context, id int64) (*domain.SyncEvent, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error)
	ListScoped(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int, scope domain.Scope) ([]domain.SyncEvent, error)
	LatestID(ctx context.Context, vaultID uuid.UUID) (int64, error)
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
	PrunedThrough(ctx context.Context) (int64, error)
	ListAfter(ctx context.Context, afterID int64, limit int) ([]domain.SyncEvent, error)
}

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error)
	Count(ctx context.Context) (int64, error)
	LockRegistrations(ctx context.Context) error
	UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error
	Update(ctx context.Context, user *domain.User) error
}

type UserTokenRepository interface {
	Create(ctx context.Context, token *domain.UserToken) error
	Consume(ctx context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error)
	Peek(ctx context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error)
	DeleteByUserAndKind(ctx context.Context, userID uuid.UUID, kind domain.UserTokenKind) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type VaultEncryptionRepository interface {
	Upsert(ctx context.Context, e *domain.VaultEncryption) error
	GetByVaultID(ctx context.Context, vaultID uuid.UUID) (*domain.VaultEncryption, error)
}

type VaultInviteRepository interface {
	Create(ctx context.Context, inv *domain.VaultInvite) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.VaultInvite, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.VaultInvite, error)
	RotateToken(ctx context.Context, id uuid.UUID, tokenHash string) error
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error)
	ListPendingByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error)
	PendingExists(ctx context.Context, vaultID uuid.UUID, email string) (bool, error)
	ListByEmail(ctx context.Context, email string) ([]domain.VaultInvite, error)
	ListPendingForEmail(ctx context.Context, email string) ([]domain.InviteDetails, error)
	Accept(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type VaultKeyRepository interface {
	Upsert(ctx context.Context, k *domain.VaultKey) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultKey, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultKey, error)
	Delete(ctx context.Context, vaultID, userID uuid.UUID) error
	DeleteForUser(ctx context.Context, userID uuid.UUID) error
	HasAny(ctx context.Context, vaultID uuid.UUID) (bool, error)
}

type VaultMemberRepository interface {
	Add(ctx context.Context, m *domain.VaultMember) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.VaultMember, error)
	UpdateRole(ctx context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error
	Remove(ctx context.Context, vaultID, userID uuid.UUID) error
	ListRuleAccess(ctx context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error)
}

type VaultAccessRepository interface {
	vault.AccessRepository
}

type VaultRepository interface {
	Create(ctx context.Context, v *domain.Vault) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
	GetForUpdate(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
	ReconcileStorage(ctx context.Context) (int64, error)
	ListSummariesForUser(ctx context.Context, userID uuid.UUID) ([]domain.VaultSummary, error)
	GetSummaryForUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultSummary, error)
	Update(ctx context.Context, v *domain.Vault) error
	Delete(ctx context.Context, id uuid.UUID) error
	AddStorage(ctx context.Context, id uuid.UUID, delta int64) error
	AddStorageWithinQuota(ctx context.Context, id uuid.UUID, delta int64) error
}

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type BlobStorage interface {
	Check(ctx context.Context) error
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) (int64, error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

type Mailer interface {
	Send(ctx context.Context, email domain.OutboxEmail) error
}

type TokenGenerator interface {
	GenerateAccessToken(userID uuid.UUID, email string, deviceID uuid.UUID) (string, error)
	ValidateAccessToken(tokenString string) (*httpx.Claims, error)
	GenerateRefreshToken() (string, error)
	HashToken(token string) string
}

type ShareRepository interface {
	share.ShareRepository
	ListBlobsByVault(ctx context.Context, vaultID uuid.UUID) ([]string, error)
}

type TemplateRepository interface {
	template.TemplateRepository
}
