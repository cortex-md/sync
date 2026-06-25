package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VaultKeyRepository struct {
	pool *pgxpool.Pool
}

func NewVaultKeyRepository(pool *pgxpool.Pool) *VaultKeyRepository {
	return &VaultKeyRepository{pool: pool}
}

func (r *VaultKeyRepository) Upsert(ctx context.Context, key *domain.VaultKey) error {
	_, err := connFromContext(ctx, r.pool).Exec(ctx,
		`INSERT INTO vault_keys (vault_id, user_id, encrypted_key, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (vault_id, user_id) DO UPDATE SET encrypted_key = EXCLUDED.encrypted_key, updated_at = EXCLUDED.updated_at`,
		key.VaultID, key.UserID, key.EncryptedKey, key.CreatedAt, key.UpdatedAt,
	)
	return mapError(err)
}

func (r *VaultKeyRepository) GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultKey, error) {
	var k domain.VaultKey
	err := r.pool.QueryRow(ctx,
		`SELECT vault_id, user_id, encrypted_key, created_at, updated_at
		 FROM vault_keys WHERE vault_id = $1 AND user_id = $2`, vaultID, userID,
	).Scan(&k.VaultID, &k.UserID, &k.EncryptedKey, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &k, nil
}

func (r *VaultKeyRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultKey, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT vault_id, user_id, encrypted_key, created_at, updated_at
		 FROM vault_keys WHERE vault_id = $1 ORDER BY created_at`, vaultID,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var keys []domain.VaultKey
	for rows.Next() {
		var k domain.VaultKey
		if err := rows.Scan(&k.VaultID, &k.UserID, &k.EncryptedKey, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, mapError(err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *VaultKeyRepository) Delete(ctx context.Context, vaultID, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM vault_keys WHERE vault_id = $1 AND user_id = $2`, vaultID, userID,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
