package port

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error
	Update(ctx context.Context, user *domain.User) error
}

type DeviceRepository interface {
	Create(ctx context.Context, device *domain.Device) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Device, error)
	GetByToken(ctx context.Context, tokenHash string) (*domain.Device, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Device, error)
	UpdateLastSeen(ctx context.Context, id uuid.UUID) error
	Revoke(ctx context.Context, id uuid.UUID) error
	Update(ctx context.Context, device *domain.Device) error
	UpdateSyncCursor(ctx context.Context, id uuid.UUID, lastSyncEventID int64) error
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, token *domain.RefreshToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error)
	RevokeByID(ctx context.Context, id uuid.UUID) error
	RevokeByFamilyID(ctx context.Context, familyID uuid.UUID) error
	RevokeAllByUserID(ctx context.Context, userID uuid.UUID) error
	RevokeAllByDeviceID(ctx context.Context, deviceID uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type VaultRepository interface {
	Create(ctx context.Context, vault *domain.Vault) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Vault, error)
	Update(ctx context.Context, vault *domain.Vault) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type VaultMemberRepository interface {
	Add(ctx context.Context, member *domain.VaultMember) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.VaultMember, error)
	UpdateRole(ctx context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error
	Remove(ctx context.Context, vaultID, userID uuid.UUID) error
}

type VaultInviteRepository interface {
	Create(ctx context.Context, invite *domain.VaultInvite) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.VaultInvite, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error)
	ListByEmail(ctx context.Context, email string) ([]domain.VaultInvite, error)
	Accept(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type VaultKeyRepository interface {
	Upsert(ctx context.Context, key *domain.VaultKey) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultKey, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultKey, error)
	Delete(ctx context.Context, vaultID, userID uuid.UUID) error
}

type VaultEncryptionRepository interface {
	Upsert(ctx context.Context, enc *domain.VaultEncryption) error
	GetByVaultID(ctx context.Context, vaultID uuid.UUID) (*domain.VaultEncryption, error)
}

type FileSnapshotRepository interface {
	Create(ctx context.Context, snapshot *domain.FileSnapshot) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.FileSnapshot, error)
	GetLatest(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileSnapshot, error)
	GetByVersion(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error)
	ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error)
	DeleteOlderVersions(ctx context.Context, vaultID uuid.UUID, filePath string, keepCount int) ([]domain.FileSnapshot, error)
}

type FileDeltaRepository interface {
	Create(ctx context.Context, delta *domain.FileDelta) error
	ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, sinceVersion int) ([]domain.FileDelta, error)
	DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, beforeVersion int) (int64, error)
}

type FileLatestRepository interface {
	Upsert(ctx context.Context, latest *domain.FileLatest) error
	Get(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceVersion int) ([]domain.FileLatest, error)
	Delete(ctx context.Context, vaultID uuid.UUID, filePath string) error
}

type SyncEventRepository interface {
	Create(ctx context.Context, event *domain.SyncEvent) error
	ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error)
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
}

type CollabDocumentRepository interface {
	StoreUpdate(ctx context.Context, update *domain.CollabUpdate) error
	BatchStoreUpdates(ctx context.Context, vaultID uuid.UUID, filePath string, updates [][]byte) error
	LoadDocument(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.CollabDocument, []domain.CollabUpdate, error)
	CompactDocument(ctx context.Context, vaultID uuid.UUID, filePath string, compactedState []byte, stateVector []byte) error
	DeleteDocument(ctx context.Context, vaultID uuid.UUID, filePath string) error
}
