package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FileLatestRepository struct {
	pool *pgxpool.Pool
}

func NewFileLatestRepository(pool *pgxpool.Pool) *FileLatestRepository {
	return &FileLatestRepository{pool: pool}
}

func (r *FileLatestRepository) Upsert(ctx context.Context, latest *domain.FileLatest) error {
	_, err := connFromContext(ctx, r.pool).Exec(ctx,
		`INSERT INTO file_latest (vault_id, file_path, current_version, latest_snapshot_version, checksum, size_bytes, content_type, deleted, last_modified_by, last_device_id, updated_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (vault_id, file_path) DO UPDATE SET
		   current_version = EXCLUDED.current_version,
		   latest_snapshot_version = EXCLUDED.latest_snapshot_version,
		   checksum = EXCLUDED.checksum,
		   size_bytes = EXCLUDED.size_bytes,
		   content_type = EXCLUDED.content_type,
		   deleted = EXCLUDED.deleted,
		   last_modified_by = EXCLUDED.last_modified_by,
		   last_device_id = EXCLUDED.last_device_id,
		   updated_at = EXCLUDED.updated_at`,
		latest.VaultID, latest.FilePath, latest.CurrentVersion, latest.LatestSnapshotVersion,
		latest.Checksum, latest.SizeBytes, latest.ContentType, latest.Deleted,
		latest.LastModifiedBy, latest.LastDeviceID, latest.UpdatedAt, latest.CreatedAt,
	)
	return mapError(err)
}

func (r *FileLatestRepository) Get(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileLatest, error) {
	var f domain.FileLatest
	err := connFromContext(ctx, r.pool).QueryRow(ctx,
		`SELECT vault_id, file_path, current_version, latest_snapshot_version, checksum, size_bytes, content_type, deleted, last_modified_by, last_device_id, updated_at, created_at
		 FROM file_latest WHERE vault_id = $1 AND file_path = $2`, vaultID, filePath,
	).Scan(&f.VaultID, &f.FilePath, &f.CurrentVersion, &f.LatestSnapshotVersion,
		&f.Checksum, &f.SizeBytes, &f.ContentType, &f.Deleted,
		&f.LastModifiedBy, &f.LastDeviceID, &f.UpdatedAt, &f.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &f, nil
}

func (r *FileLatestRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceVersion int) ([]domain.FileLatest, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT vault_id, file_path, current_version, latest_snapshot_version, checksum, size_bytes, content_type, deleted, last_modified_by, last_device_id, updated_at, created_at
		 FROM file_latest WHERE vault_id = $1 AND current_version > $2 ORDER BY file_path`, vaultID, sinceVersion,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var files []domain.FileLatest
	for rows.Next() {
		var f domain.FileLatest
		if err := rows.Scan(&f.VaultID, &f.FilePath, &f.CurrentVersion, &f.LatestSnapshotVersion,
			&f.Checksum, &f.SizeBytes, &f.ContentType, &f.Deleted,
			&f.LastModifiedBy, &f.LastDeviceID, &f.UpdatedAt, &f.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (r *FileLatestRepository) Delete(ctx context.Context, vaultID uuid.UUID, filePath string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM file_latest WHERE vault_id = $1 AND file_path = $2`, vaultID, filePath,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
