package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VaultMemberRepository struct {
	pool *pgxpool.Pool
}

func NewVaultMemberRepository(pool *pgxpool.Pool) *VaultMemberRepository {
	return &VaultMemberRepository{pool: pool}
}

func (r *VaultMemberRepository) Add(ctx context.Context, member *domain.VaultMember) error {
	_, err := connFromContext(ctx, r.pool).Exec(ctx,
		`INSERT INTO vault_members (vault_id, user_id, role, joined_at)
		 VALUES ($1, $2, $3, $4)`,
		member.VaultID, member.UserID, member.Role, member.JoinedAt,
	)
	return mapError(err)
}

func (r *VaultMemberRepository) GetByVaultAndUser(ctx context.Context, vaultID, userID uuid.UUID) (*domain.VaultMember, error) {
	var m domain.VaultMember
	err := r.pool.QueryRow(ctx,
		`SELECT vault_id, user_id, role, joined_at
		 FROM vault_members WHERE vault_id = $1 AND user_id = $2`, vaultID, userID,
	).Scan(&m.VaultID, &m.UserID, &m.Role, &m.JoinedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &m, nil
}

func (r *VaultMemberRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultMember, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT vault_id, user_id, role, joined_at
		 FROM vault_members WHERE vault_id = $1 ORDER BY joined_at`, vaultID,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var members []domain.VaultMember
	for rows.Next() {
		var m domain.VaultMember
		if err := rows.Scan(&m.VaultID, &m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, mapError(err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (r *VaultMemberRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.VaultMember, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT vault_id, user_id, role, joined_at
		 FROM vault_members WHERE user_id = $1 ORDER BY joined_at`, userID,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var members []domain.VaultMember
	for rows.Next() {
		var m domain.VaultMember
		if err := rows.Scan(&m.VaultID, &m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, mapError(err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (r *VaultMemberRepository) UpdateRole(ctx context.Context, vaultID, userID uuid.UUID, role domain.VaultRole) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE vault_members SET role = $1 WHERE vault_id = $2 AND user_id = $3`,
		role, vaultID, userID,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *VaultMemberRepository) Remove(ctx context.Context, vaultID, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM vault_members WHERE vault_id = $1 AND user_id = $2`,
		vaultID, userID,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
