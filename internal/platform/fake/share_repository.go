package fake

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type ShareRepository struct {
	mu     sync.Mutex
	shares map[string]*domain.NoteShare
}

func NewShareRepository() *ShareRepository {
	return &ShareRepository{shares: make(map[string]*domain.NoteShare)}
}

func (r *ShareRepository) Create(_ context.Context, share *domain.NoteShare) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.shares {
		if existing.VaultID == share.VaultID && existing.FilePath == share.FilePath {
			return false, nil
		}
	}
	now := time.Now()
	share.CreatedAt, share.UpdatedAt = now, now
	stored := *share
	r.shares[share.ID] = &stored
	return true, nil
}

func (r *ShareRepository) Get(_ context.Context, id string) (*domain.NoteShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	share, ok := r.shares[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copied := *share
	return &copied, nil
}

func (r *ShareRepository) GetForUpdate(ctx context.Context, id string) (*domain.NoteShare, error) {
	return r.Get(ctx, id)
}

func (r *ShareRepository) GetByPath(_ context.Context, vaultID uuid.UUID, filePath string) (*domain.NoteShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, share := range r.shares {
		if share.VaultID == vaultID && share.FilePath == filePath {
			copied := *share
			return &copied, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *ShareRepository) ListByVault(_ context.Context, vaultID uuid.UUID) ([]domain.NoteShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.NoteShare
	for _, share := range r.shares {
		if share.VaultID == vaultID {
			result = append(result, *share)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FilePath < result[j].FilePath })
	return result, nil
}

func (r *ShareRepository) UpdateContent(_ context.Context, id string, revision int, blobKey string, size int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	share, ok := r.shares[id]
	if !ok {
		return domain.ErrNotFound
	}
	share.Revision, share.BlobKey, share.SizeBytes, share.UpdatedAt = revision, blobKey, size, time.Now()
	return nil
}

func (r *ShareRepository) UpdateExpiry(_ context.Context, id string, expiresAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	share, ok := r.shares[id]
	if !ok {
		return domain.ErrNotFound
	}
	share.ExpiresAt = expiresAt
	return nil
}

func (r *ShareRepository) Delete(_ context.Context, id string) (*domain.RemovedShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	share, ok := r.shares[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	delete(r.shares, id)
	return &domain.RemovedShare{VaultID: share.VaultID, BlobKey: share.BlobKey, SizeBytes: share.SizeBytes}, nil
}

func (r *ShareRepository) DeleteByPath(_ context.Context, vaultID uuid.UUID, filePath string) ([]domain.RemovedShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []domain.RemovedShare
	for id, share := range r.shares {
		if share.VaultID == vaultID && share.FilePath == filePath {
			removed = append(removed, domain.RemovedShare{VaultID: vaultID, BlobKey: share.BlobKey, SizeBytes: share.SizeBytes})
			delete(r.shares, id)
		}
	}
	return removed, nil
}

func (r *ShareRepository) Move(_ context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, share := range r.shares {
		if share.VaultID == vaultID && share.FilePath == oldPath {
			share.FilePath = newPath
		}
	}
	return nil
}

func (r *ShareRepository) ListBlobsByVault(_ context.Context, vaultID uuid.UUID) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var keys []string
	for _, share := range r.shares {
		if share.VaultID == vaultID && share.BlobKey != "" {
			keys = append(keys, share.BlobKey)
		}
	}
	return keys, nil
}

func (r *ShareRepository) DeleteExpired(_ context.Context, limit int) ([]domain.RemovedShare, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var removed []domain.RemovedShare
	for id, share := range r.shares {
		if len(removed) >= limit {
			break
		}
		if share.ExpiresAt != nil && share.ExpiresAt.Before(now) {
			removed = append(removed, domain.RemovedShare{VaultID: share.VaultID, BlobKey: share.BlobKey, SizeBytes: share.SizeBytes})
			delete(r.shares, id)
		}
	}
	return removed, nil
}
