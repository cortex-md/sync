package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VaultInviteRepository struct {
	pool *pgxpool.Pool
}

func NewVaultInviteRepository(pool *pgxpool.Pool) *VaultInviteRepository {
	return &VaultInviteRepository{pool: pool}
}

func (r *VaultInviteRepository) Create(ctx context.Context, invite *domain.VaultInvite) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO vault_invites (id, vault_id, inviter_id, invitee_email, role, encrypted_vault_key, accepted, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		invite.ID, invite.VaultID, invite.InviterID, invite.InviteeEmail, invite.Role,
		invite.EncryptedVaultKey, invite.Accepted, invite.ExpiresAt, invite.CreatedAt,
	)
	return mapError(err)
}

func (r *VaultInviteRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.VaultInvite, error) {
	var inv domain.VaultInvite
	err := r.pool.QueryRow(ctx,
		`SELECT id, vault_id, inviter_id, invitee_email, role, encrypted_vault_key, accepted, expires_at, created_at
		 FROM vault_invites WHERE id = $1`, id,
	).Scan(&inv.ID, &inv.VaultID, &inv.InviterID, &inv.InviteeEmail, &inv.Role,
		&inv.EncryptedVaultKey, &inv.Accepted, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &inv, nil
}

func (r *VaultInviteRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, vault_id, inviter_id, invitee_email, role, encrypted_vault_key, accepted, expires_at, created_at
		 FROM vault_invites WHERE vault_id = $1 ORDER BY created_at`, vaultID,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var invites []domain.VaultInvite
	for rows.Next() {
		var inv domain.VaultInvite
		if err := rows.Scan(&inv.ID, &inv.VaultID, &inv.InviterID, &inv.InviteeEmail, &inv.Role,
			&inv.EncryptedVaultKey, &inv.Accepted, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

func (r *VaultInviteRepository) ListByEmail(ctx context.Context, email string) ([]domain.VaultInvite, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, vault_id, inviter_id, invitee_email, role, encrypted_vault_key, accepted, expires_at, created_at
		 FROM vault_invites WHERE invitee_email = $1 ORDER BY created_at`, email,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var invites []domain.VaultInvite
	for rows.Next() {
		var inv domain.VaultInvite
		if err := rows.Scan(&inv.ID, &inv.VaultID, &inv.InviterID, &inv.InviteeEmail, &inv.Role,
			&inv.EncryptedVaultKey, &inv.Accepted, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

func (r *VaultInviteRepository) Accept(ctx context.Context, id uuid.UUID) error {
	tag, err := connFromContext(ctx, r.pool).Exec(ctx,
		`UPDATE vault_invites SET accepted = true WHERE id = $1`, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *VaultInviteRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM vault_invites WHERE id = $1`, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *VaultInviteRepository) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM vault_invites WHERE expires_at < now() AND accepted = false`,
	)
	if err != nil {
		return 0, mapError(err)
	}
	return tag.RowsAffected(), nil
}
