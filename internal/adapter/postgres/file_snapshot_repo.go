package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FileSnapshotRepository struct {
	pool *pgxpool.Pool
}

func NewFileSnapshotRepository(pool *pgxpool.Pool) *FileSnapshotRepository {
	return &FileSnapshotRepository{pool: pool}
}

func (r *FileSnapshotRepository) Create(ctx context.Context, snapshot *domain.FileSnapshot) error {
	_, err := connFromContext(ctx, r.pool).Exec(ctx,
		`INSERT INTO file_snapshots (id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		snapshot.ID, snapshot.VaultID, snapshot.FilePath, snapshot.Version, snapshot.EncryptedBlobKey,
		snapshot.SizeBytes, snapshot.Checksum, snapshot.CreatedBy, snapshot.DeviceID, snapshot.CreatedAt,
	)
	return mapError(err)
}

func (r *FileSnapshotRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.FileSnapshot, error) {
	var s domain.FileSnapshot
	err := r.pool.QueryRow(ctx,
		`SELECT id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at
		 FROM file_snapshots WHERE id = $1`, id,
	).Scan(&s.ID, &s.VaultID, &s.FilePath, &s.Version, &s.EncryptedBlobKey,
		&s.SizeBytes, &s.Checksum, &s.CreatedBy, &s.DeviceID, &s.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &s, nil
}

func (r *FileSnapshotRepository) GetLatest(ctx context.Context, vaultID uuid.UUID, filePath string) (*domain.FileSnapshot, error) {
	var s domain.FileSnapshot
	err := r.pool.QueryRow(ctx,
		`SELECT id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at
		 FROM file_snapshots WHERE vault_id = $1 AND file_path = $2 ORDER BY version DESC LIMIT 1`, vaultID, filePath,
	).Scan(&s.ID, &s.VaultID, &s.FilePath, &s.Version, &s.EncryptedBlobKey,
		&s.SizeBytes, &s.Checksum, &s.CreatedBy, &s.DeviceID, &s.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &s, nil
}

func (r *FileSnapshotRepository) GetByVersion(ctx context.Context, vaultID uuid.UUID, filePath string, version int) (*domain.FileSnapshot, error) {
	var s domain.FileSnapshot
	err := r.pool.QueryRow(ctx,
		`SELECT id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at
		 FROM file_snapshots WHERE vault_id = $1 AND file_path = $2 AND version = $3`, vaultID, filePath, version,
	).Scan(&s.ID, &s.VaultID, &s.FilePath, &s.Version, &s.EncryptedBlobKey,
		&s.SizeBytes, &s.Checksum, &s.CreatedBy, &s.DeviceID, &s.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &s, nil
}

func (r *FileSnapshotRepository) ListByFilePath(ctx context.Context, vaultID uuid.UUID, filePath string) ([]domain.FileSnapshot, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at
		 FROM file_snapshots WHERE vault_id = $1 AND file_path = $2 ORDER BY version`, vaultID, filePath,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var snapshots []domain.FileSnapshot
	for rows.Next() {
		var s domain.FileSnapshot
		if err := rows.Scan(&s.ID, &s.VaultID, &s.FilePath, &s.Version, &s.EncryptedBlobKey,
			&s.SizeBytes, &s.Checksum, &s.CreatedBy, &s.DeviceID, &s.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, rows.Err()
}

func (r *FileSnapshotRepository) DeleteOlderVersions(ctx context.Context, vaultID uuid.UUID, filePath string, keepCount int) ([]domain.FileSnapshot, error) {
	rows, err := r.pool.Query(ctx,
		`DELETE FROM file_snapshots
		 WHERE vault_id = $1 AND file_path = $2 AND id NOT IN (
			 SELECT id FROM file_snapshots
			 WHERE vault_id = $1 AND file_path = $2
			 ORDER BY version DESC LIMIT $3
		 )
		 RETURNING id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at`,
		vaultID, filePath, keepCount,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var deleted []domain.FileSnapshot
	for rows.Next() {
		var s domain.FileSnapshot
		if err := rows.Scan(&s.ID, &s.VaultID, &s.FilePath, &s.Version, &s.EncryptedBlobKey,
			&s.SizeBytes, &s.Checksum, &s.CreatedBy, &s.DeviceID, &s.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		deleted = append(deleted, s)
	}
	return deleted, rows.Err()
}
