package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FileDeltaRepository struct {
	pool *pgxpool.Pool
}

func NewFileDeltaRepository(pool *pgxpool.Pool) *FileDeltaRepository {
	return &FileDeltaRepository{pool: pool}
}

func (r *FileDeltaRepository) Create(ctx context.Context, delta *domain.FileDelta) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO file_deltas (id, vault_id, file_path, base_version, target_version, encrypted_delta, size_bytes, created_by, device_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		delta.ID, delta.VaultID, delta.FilePath, delta.BaseVersion, delta.TargetVersion,
		delta.EncryptedDelta, delta.SizeBytes, delta.CreatedBy, delta.DeviceID, delta.CreatedAt,
	)
	return mapError(err)
}

func (r *FileDeltaRepository) ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, sinceVersion int) ([]domain.FileDelta, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, vault_id, file_path, base_version, target_version, encrypted_delta, size_bytes, created_by, device_id, created_at
		 FROM file_deltas WHERE vault_id = $1 AND file_path = $2 AND base_version >= $3 ORDER BY base_version`,
		vaultID, filePath, sinceVersion,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var deltas []domain.FileDelta
	for rows.Next() {
		var d domain.FileDelta
		if err := rows.Scan(&d.ID, &d.VaultID, &d.FilePath, &d.BaseVersion, &d.TargetVersion,
			&d.EncryptedDelta, &d.SizeBytes, &d.CreatedBy, &d.DeviceID, &d.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		deltas = append(deltas, d)
	}
	return deltas, rows.Err()
}

func (r *FileDeltaRepository) DeleteByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string, beforeVersion int) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM file_deltas WHERE vault_id = $1 AND file_path = $2 AND target_version < $3`,
		vaultID, filePath, beforeVersion,
	)
	if err != nil {
		return 0, mapError(err)
	}
	return tag.RowsAffected(), nil
}
