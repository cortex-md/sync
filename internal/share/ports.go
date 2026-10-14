package share

import (
	"context"
	"io"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type ShareRepository interface {
	Create(ctx context.Context, share *domain.NoteShare) (bool, error)
	Get(ctx context.Context, id string) (*domain.NoteShare, error)
	GetForUpdate(ctx context.Context, id string) (*domain.NoteShare, error)
	GetByPath(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.NoteShare, error)
	ListByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.NoteShare, error)
	UpdateContent(ctx context.Context, id string, revision int, blobKey string, size int64) error
	UpdateExpiry(ctx context.Context, id string, expiresAt *time.Time) error
	Delete(ctx context.Context, id string) (*domain.RemovedShare, error)
	DeleteByPath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.RemovedShare, error)
	Move(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error
	DeleteExpired(ctx context.Context, limit int) ([]domain.RemovedShare, error)
}

type VaultStorage interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error)
	AddStorage(ctx context.Context, id uuid.UUID, delta int64) error
	AddStorageWithinQuota(ctx context.Context, id uuid.UUID, delta int64) error
}

type FileLookup interface {
	Get(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error)
}

type BlobStorage interface {
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) (int64, error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
}

type BlobQueue interface {
	RegisterPending(ctx context.Context, key string) error
	ResolvePending(ctx context.Context, key string) error
	EnqueueDeletion(ctx context.Context, keys ...string) error
}
