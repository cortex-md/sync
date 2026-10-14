package file

import (
	"context"
	"io"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type BlobStorage interface {
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) (int64, error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

type SharePaths interface {
	RemoveForPath(ctx context.Context, vaultID uuid.UUID, filePath string) error
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}

type BlobQueue interface {
	RegisterPending(ctx context.Context, key string) error
	ResolvePending(ctx context.Context, key string) error
	EnqueueDeletion(ctx context.Context, keys ...string) error
}

type VaultMemberRepository interface {
	GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error)
	ListRuleAccess(ctx context.Context, vaultID, userID uuid.UUID) ([]domain.RuleAccess, error)
}

type AccessPaths interface {
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) (bool, error)
	RemoveForPath(ctx context.Context, vaultID uuid.UUID, path string) (bool, error)
	Changed(ctx context.Context, vaultID uuid.UUID)
}

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

type DeviceRepository interface {
	UpdateLastSeen(ctx context.Context, id uuid.UUID) error
	UpsertVaultCursor(ctx context.Context, deviceID, vaultID uuid.UUID, lastEventID int64) error
}

type VaultStorage interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
	AddStorage(ctx context.Context, id uuid.UUID, delta int64) error
	AddStorageWithinQuota(ctx context.Context, id uuid.UUID, delta int64) error
}

type FileSnapshotRepository interface {
	Create(ctx context.Context, snapshot *domain.FileSnapshot) error
	GetLatest(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileSnapshot, error)
	GetByVersion(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error)
	ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error)
	ListLatestByPaths(ctx context.Context, vaultID uuid.UUID, paths []string) ([]domain.FileSnapshot, error)
	ListAtOrBefore(ctx context.Context, vaultID uuid.UUID, paths []string, versions []int32) (map[int]domain.FileSnapshot, error)
	GetAtOrBefore(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error)
	ListHistory(ctx context.Context, vaultID uuid.UUID, filePath string, limit int) ([]domain.HistoryEntry, error)
	DeleteOlderVersions(ctx context.Context, vaultID uuid.UUID, filePath string, keepCount int) ([]domain.FileSnapshot, error)
	DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error)
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}

type FileDeltaRepository interface {
	Create(ctx context.Context, delta *domain.FileDelta) error
	ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, sinceVersion int) ([]domain.FileDelta, error)
	DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, beforeVersion int) (int64, error)
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}

type FileLatestRepository interface {
	Upsert(ctx context.Context, latest *domain.FileLatest) error
	Get(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error)
	GetForUpdate(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error)
	ListByPaths(ctx context.Context, vaultID uuid.UUID, filePaths []string) ([]domain.FileLatest, error)
	List(ctx context.Context, query domain.FileListQuery) ([]domain.FileLatest, error)
}

type SyncEventRepository interface {
	Create(ctx context.Context, event *domain.SyncEvent) error
	ListScoped(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int, scope domain.Scope) ([]domain.SyncEvent, error)
	LatestID(ctx context.Context, vaultID uuid.UUID) (int64, error)
}

type CollabDocumentRepository interface {
	DeleteForPath(ctx context.Context, vaultID uuid.UUID, filePath string) error
	MovePath(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
}
