package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, token *domain.RefreshToken) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, device_id, token_hash, family_id, expires_at, revoked, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		token.ID, token.UserID, token.DeviceID, token.TokenHash, token.FamilyID,
		token.ExpiresAt, token.Revoked, token.CreatedAt,
	)
	return mapError(err)
}

func (r *RefreshTokenRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, device_id, token_hash, family_id, expires_at, revoked, created_at
		 FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.DeviceID, &t.TokenHash, &t.FamilyID, &t.ExpiresAt, &t.Revoked, &t.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &t, nil
}

func (r *RefreshTokenRepository) RevokeByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE id = $1`, id,
	)
	return mapError(err)
}

func (r *RefreshTokenRepository) RevokeByFamilyID(ctx context.Context, familyID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE family_id = $1`, familyID,
	)
	return mapError(err)
}

func (r *RefreshTokenRepository) RevokeAllByUserID(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE user_id = $1`, userID,
	)
	return mapError(err)
}

func (r *RefreshTokenRepository) RevokeAllByDeviceID(ctx context.Context, deviceID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true WHERE device_id = $1`, deviceID,
	)
	return mapError(err)
}

func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM refresh_tokens WHERE expires_at < now() AND revoked = true`,
	)
	if err != nil {
		return 0, mapError(err)
	}
	return tag.RowsAffected(), nil
}
