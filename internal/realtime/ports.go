package realtime

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type CollabDocumentRepository interface {
	StoreUpdate(ctx context.Context, update *domain.CollabUpdate) error
	BatchStoreUpdates(ctx context.Context, vaultID uuid.UUID, filePath string, updates [][]byte) error
	LoadDocument(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.CollabDocument, []domain.CollabUpdate, error)
	CompactDocument(ctx context.Context, vaultID uuid.UUID, filePath string, compactedState []byte, upToUpdateID int64) error
	DeleteForPath(ctx context.Context, vaultID uuid.UUID, filePath string) error
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}

type CollabPresenceRepository interface {
	Join(ctx context.Context, presence *domain.CollabPresence) error
	Leave(ctx context.Context, clientID string) error
	Touch(ctx context.Context, clientIDs []string) error
	ListRoom(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.CollabPresence, error)
	ListVault(ctx context.Context, vaultID uuid.UUID) ([]domain.CollabPresence, error)
	DeleteStale(ctx context.Context, before time.Time) (int64, error)
}

type SyncEventRepository interface {
	Create(ctx context.Context, event *domain.SyncEvent) error
	GetByID(ctx context.Context, id int64) (*domain.SyncEvent, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error)
	ListScoped(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int, scope domain.Scope) ([]domain.SyncEvent, error)
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
	PrunedThrough(ctx context.Context) (int64, error)
}

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Count(ctx context.Context) (int64, error)
	UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error
	Update(ctx context.Context, user *domain.User) error
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error)
}

type VaultMemberRepository interface {
	Add(ctx context.Context, member *domain.VaultMember) error
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.VaultMember, error)
	UpdateRole(ctx context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error
	Remove(ctx context.Context, vaultID, userID uuid.UUID) error
	ListRuleAccess(ctx context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error)
}
