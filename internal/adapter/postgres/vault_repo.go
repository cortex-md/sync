package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VaultRepository struct {
	pool *pgxpool.Pool
}

func NewVaultRepository(pool *pgxpool.Pool) *VaultRepository {
	return &VaultRepository{pool: pool}
}

func (r *VaultRepository) Create(ctx context.Context, vault *domain.Vault) error {
	_, err := connFromContext(ctx, r.pool).Exec(ctx,
		`INSERT INTO vaults (id, name, description, owner_id, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		vault.ID, vault.Name, vault.Description, vault.OwnerID, vault.CreatedAt, vault.UpdatedAt,
	)
	return mapError(err)
}

func (r *VaultRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Vault, error) {
	var v domain.Vault
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, owner_id, created_at, updated_at
		 FROM vaults WHERE id = $1`, id,
	).Scan(&v.ID, &v.Name, &v.Description, &v.OwnerID, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &v, nil
}

func (r *VaultRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Vault, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, description, owner_id, created_at, updated_at
		 FROM vaults WHERE owner_id = $1 ORDER BY created_at`, userID,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var vaults []domain.Vault
	for rows.Next() {
		var v domain.Vault
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.OwnerID, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, mapError(err)
		}
		vaults = append(vaults, v)
	}
	return vaults, rows.Err()
}

func (r *VaultRepository) Update(ctx context.Context, vault *domain.Vault) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE vaults SET name = $1, description = $2, updated_at = now() WHERE id = $3`,
		vault.Name, vault.Description, vault.ID,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *VaultRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM vaults WHERE id = $1`, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
