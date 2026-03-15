package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VaultEncryptionRepository struct {
	pool *pgxpool.Pool
}

func NewVaultEncryptionRepository(pool *pgxpool.Pool) *VaultEncryptionRepository {
	return &VaultEncryptionRepository{pool: pool}
}

func (r *VaultEncryptionRepository) Upsert(ctx context.Context, enc *domain.VaultEncryption) error {
	_, err := connFromContext(ctx, r.pool).Exec(ctx,
		`INSERT INTO vault_encryption (vault_id, salt, encrypted_vek, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (vault_id) DO UPDATE SET salt = EXCLUDED.salt, encrypted_vek = EXCLUDED.encrypted_vek`,
		enc.VaultID, enc.Salt, enc.EncryptedVEK, enc.CreatedAt,
	)
	return mapError(err)
}

func (r *VaultEncryptionRepository) GetByVaultID(ctx context.Context, vaultID uuid.UUID) (*domain.VaultEncryption, error) {
	var enc domain.VaultEncryption
	err := r.pool.QueryRow(ctx,
		`SELECT vault_id, salt, encrypted_vek, created_at
		 FROM vault_encryption WHERE vault_id = $1`, vaultID,
	).Scan(&enc.VaultID, &enc.Salt, &enc.EncryptedVEK, &enc.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &enc, nil
}
