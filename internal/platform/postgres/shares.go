package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ShareRepository struct{ store }

func NewShareRepository(pool *pgxpool.Pool) *ShareRepository {
	return &ShareRepository{store{pool}}
}

func toShare(s db.NoteShare) domain.NoteShare {
	share := domain.NoteShare{
		ID:         s.ID,
		VaultID:    s.VaultID,
		FilePath:   s.FilePath,
		CreatedBy:  s.CreatedBy,
		WrappedKey: s.WrappedKey,
		Revision:   int(s.Revision),
		SizeBytes:  s.SizeBytes,
		ExpiresAt:  s.ExpiresAt,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
	if s.BlobKey != nil {
		share.BlobKey = *s.BlobKey
	}
	return share
}

func removedShare(vaultID uuid.UUID, blobKey *string, size int64) domain.RemovedShare {
	removed := domain.RemovedShare{VaultID: vaultID, SizeBytes: size}
	if blobKey != nil {
		removed.BlobKey = *blobKey
	}
	return removed
}

func (r *ShareRepository) Create(ctx context.Context, share *domain.NoteShare) (bool, error) {
	row, err := r.q(ctx).CreateShare(ctx, db.CreateShareParams{
		ID:         share.ID,
		VaultID:    share.VaultID,
		FilePath:   share.FilePath,
		CreatedBy:  share.CreatedBy,
		WrappedKey: share.WrappedKey,
		ExpiresAt:  share.ExpiresAt,
	})
	if err != nil {
		mapped := mapError(err)
		if errors.Is(mapped, domain.ErrNotFound) {
			return false, nil
		}
		return false, mapped
	}
	*share = toShare(row)
	return true, nil
}

func (r *ShareRepository) Get(ctx context.Context, id string) (*domain.NoteShare, error) {
	row, err := r.q(ctx).GetShare(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	share := toShare(row)
	return &share, nil
}

func (r *ShareRepository) GetForUpdate(ctx context.Context, id string) (*domain.NoteShare, error) {
	row, err := r.q(ctx).GetShareForUpdate(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	share := toShare(row)
	return &share, nil
}

func (r *ShareRepository) GetByPath(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.NoteShare, error) {
	row, err := r.q(ctx).GetShareByPath(ctx, db.GetShareByPathParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	share := toShare(row)
	return &share, nil
}

func (r *ShareRepository) ListByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.NoteShare, error) {
	rows, err := r.q(ctx).ListSharesByVault(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	shares := make([]domain.NoteShare, len(rows))
	for i, row := range rows {
		shares[i] = toShare(row)
	}
	return shares, nil
}

func (r *ShareRepository) UpdateContent(ctx context.Context, id string, revision int, blobKey string, size int64) error {
	return mapError(r.q(ctx).UpdateShareContent(ctx, db.UpdateShareContentParams{
		ID: id, Revision: int32(revision), BlobKey: &blobKey, SizeBytes: size,
	}))
}

func (r *ShareRepository) UpdateExpiry(ctx context.Context, id string, expiresAt *time.Time) error {
	return mapError(r.q(ctx).UpdateShareExpiry(ctx, db.UpdateShareExpiryParams{ID: id, ExpiresAt: expiresAt}))
}

func (r *ShareRepository) Delete(ctx context.Context, id string) (*domain.RemovedShare, error) {
	row, err := r.q(ctx).DeleteShare(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	removed := removedShare(row.VaultID, row.BlobKey, row.SizeBytes)
	return &removed, nil
}

func (r *ShareRepository) DeleteByPath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.RemovedShare, error) {
	rows, err := r.q(ctx).DeleteShareByPath(ctx, db.DeleteShareByPathParams{VaultID: vaultID, FilePath: filePath})
	if err != nil {
		return nil, mapError(err)
	}
	removed := make([]domain.RemovedShare, len(rows))
	for i, row := range rows {
		removed[i] = removedShare(row.VaultID, row.BlobKey, row.SizeBytes)
	}
	return removed, nil
}

func (r *ShareRepository) Move(ctx context.Context, vaultID uuid.UUID, oldPath, newPath string) error {
	return mapError(r.q(ctx).MoveShare(ctx, db.MoveShareParams{VaultID: vaultID, OldPath: oldPath, NewPath: newPath}))
}

func (r *ShareRepository) ListBlobsByVault(ctx context.Context, vaultID uuid.UUID) ([]string, error) {
	rows, err := r.q(ctx).ListShareBlobsByVault(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	keys := make([]string, 0, len(rows))
	for _, key := range rows {
		if key != nil {
			keys = append(keys, *key)
		}
	}
	return keys, nil
}

func (r *ShareRepository) DeleteExpired(ctx context.Context, limit int) ([]domain.RemovedShare, error) {
	rows, err := r.q(ctx).DeleteExpiredShares(ctx, int32(limit))
	if err != nil {
		return nil, mapError(err)
	}
	removed := make([]domain.RemovedShare, len(rows))
	for i, row := range rows {
		removed[i] = removedShare(row.VaultID, row.BlobKey, row.SizeBytes)
	}
	return removed, nil
}
