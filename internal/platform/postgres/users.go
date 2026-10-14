package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct{ store }

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{store{pool}}
}

func toUser(u db.User) domain.User {
	return domain.User{
		ID:                        u.ID,
		Email:                     u.Email,
		PasswordHash:              u.PasswordHash,
		DisplayName:               u.DisplayName,
		PublicKey:                 u.PublicKey,
		EmailVerifiedAt:           u.EmailVerifiedAt,
		AvatarKey:                 u.AvatarKey,
		Locale:                    u.Locale,
		IsInstanceAdmin:           u.IsInstanceAdmin,
		KDFVersion:                int(u.KdfVersion),
		KDFSalt:                   u.KdfSalt,
		EncryptedPrivateKey:       u.EncryptedPrivateKey,
		RecoveryWrappedPrivateKey: u.RecoveryWrappedPrivateKey,
		CreatedAt:                 u.CreatedAt,
		UpdatedAt:                 u.UpdatedAt,
	}
}

func (r *UserRepository) LockRegistrations(ctx context.Context) error {
	return mapError(r.q(ctx).LockRegistrations(ctx))
}

func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	if user.Locale == "" {
		user.Locale = "en"
	}
	admin, err := r.q(ctx).CreateUser(ctx, db.CreateUserParams{
		ID:                        user.ID,
		Email:                     user.Email,
		PasswordHash:              user.PasswordHash,
		DisplayName:               user.DisplayName,
		PublicKey:                 user.PublicKey,
		EmailVerifiedAt:           user.EmailVerifiedAt,
		AvatarKey:                 user.AvatarKey,
		Locale:                    user.Locale,
		KdfVersion:                int16(user.KDFVersion),
		KdfSalt:                   user.KDFSalt,
		EncryptedPrivateKey:       user.EncryptedPrivateKey,
		RecoveryWrappedPrivateKey: user.RecoveryWrappedPrivateKey,
		CreatedAt:                 user.CreatedAt,
		UpdatedAt:                 user.UpdatedAt,
	})
	if err != nil {
		return mapError(err)
	}
	user.IsInstanceAdmin = admin
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	u, err := r.q(ctx).GetUserByID(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	user := toUser(u)
	return &user, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	u, err := r.q(ctx).GetUserByEmail(ctx, email)
	if err != nil {
		return nil, mapError(err)
	}
	user := toUser(u)
	return &user, nil
}

func (r *UserRepository) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListUsersByIDs(ctx, ids)
	if err != nil {
		return nil, mapError(err)
	}
	users := make([]domain.User, len(rows))
	for i, u := range rows {
		users[i] = toUser(u)
	}
	return users, nil
}

func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	count, err := r.q(ctx).CountUsers(ctx)
	return count, mapError(err)
}

func (r *UserRepository) UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error {
	return expectRows(r.q(ctx).UpdateUserPublicKey(ctx, db.UpdateUserPublicKeyParams{ID: id, PublicKey: publicKey}))
}

func (r *UserRepository) Update(ctx context.Context, user *domain.User) error {
	return expectRows(r.q(ctx).UpdateUser(ctx, db.UpdateUserParams{
		ID:                        user.ID,
		Email:                     user.Email,
		PasswordHash:              user.PasswordHash,
		DisplayName:               user.DisplayName,
		PublicKey:                 user.PublicKey,
		EmailVerifiedAt:           user.EmailVerifiedAt,
		AvatarKey:                 user.AvatarKey,
		Locale:                    user.Locale,
		KdfVersion:                int16(user.KDFVersion),
		KdfSalt:                   user.KDFSalt,
		EncryptedPrivateKey:       user.EncryptedPrivateKey,
		RecoveryWrappedPrivateKey: user.RecoveryWrappedPrivateKey,
	}))
}

type UserTokenRepository struct{ store }

func NewUserTokenRepository(pool *pgxpool.Pool) *UserTokenRepository {
	return &UserTokenRepository{store{pool}}
}

func (r *UserTokenRepository) Create(ctx context.Context, token *domain.UserToken) error {
	return mapError(r.q(ctx).CreateUserToken(ctx, db.CreateUserTokenParams{
		ID:        token.ID,
		UserID:    token.UserID,
		Kind:      string(token.Kind),
		TokenHash: token.TokenHash,
		Payload:   token.Payload,
		ExpiresAt: token.ExpiresAt,
		CreatedAt: token.CreatedAt,
	}))
}

func (r *UserTokenRepository) Consume(ctx context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error) {
	t, err := r.q(ctx).ConsumeUserToken(ctx, db.ConsumeUserTokenParams{TokenHash: tokenHash, Kind: string(kind)})
	if err != nil {
		return nil, mapError(err)
	}
	return toUserToken(t), nil
}

func (r *UserTokenRepository) Peek(ctx context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error) {
	t, err := r.q(ctx).GetActiveUserToken(ctx, db.GetActiveUserTokenParams{TokenHash: tokenHash, Kind: string(kind)})
	if err != nil {
		return nil, mapError(err)
	}
	return toUserToken(t), nil
}

func toUserToken(t db.UserToken) *domain.UserToken {
	return &domain.UserToken{
		ID:        t.ID,
		UserID:    t.UserID,
		Kind:      domain.UserTokenKind(t.Kind),
		TokenHash: t.TokenHash,
		Payload:   t.Payload,
		ExpiresAt: t.ExpiresAt,
		UsedAt:    t.UsedAt,
		CreatedAt: t.CreatedAt,
	}
}

func (r *UserTokenRepository) DeleteByUserAndKind(ctx context.Context, userID uuid.UUID, kind domain.UserTokenKind) error {
	return mapError(r.q(ctx).DeleteUserTokensByKind(ctx, db.DeleteUserTokensByKindParams{UserID: userID, Kind: string(kind)}))
}

func (r *UserTokenRepository) DeleteExpired(ctx context.Context) (int64, error) {
	n, err := r.q(ctx).DeleteExpiredUserTokens(ctx)
	return n, mapError(err)
}

type DeviceRepository struct{ store }

func NewDeviceRepository(pool *pgxpool.Pool) *DeviceRepository {
	return &DeviceRepository{store{pool}}
}

func toDevice(d db.Device) domain.Device {
	return domain.Device{
		ID:          d.ID,
		UserID:      d.UserID,
		DeviceName:  d.DeviceName,
		DeviceType:  d.DeviceType,
		DeviceToken: d.DeviceToken,
		LastSeenAt:  d.LastSeenAt,
		CreatedAt:   d.CreatedAt,
		Revoked:     d.Revoked,
	}
}

func (r *DeviceRepository) Create(ctx context.Context, d *domain.Device) error {
	return mapError(r.q(ctx).CreateDevice(ctx, db.CreateDeviceParams{
		ID:          d.ID,
		UserID:      d.UserID,
		DeviceName:  d.DeviceName,
		DeviceType:  d.DeviceType,
		DeviceToken: d.DeviceToken,
		LastSeenAt:  d.LastSeenAt,
		CreatedAt:   d.CreatedAt,
		Revoked:     d.Revoked,
	}))
}

func (r *DeviceRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Device, error) {
	d, err := r.q(ctx).GetDeviceByID(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	device := toDevice(d)
	return &device, nil
}

func (r *DeviceRepository) GetByToken(ctx context.Context, tokenHash string) (*domain.Device, error) {
	d, err := r.q(ctx).GetDeviceByToken(ctx, tokenHash)
	if err != nil {
		return nil, mapError(err)
	}
	device := toDevice(d)
	return &device, nil
}

func (r *DeviceRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Device, error) {
	rows, err := r.q(ctx).ListDevicesByUser(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	devices := make([]domain.Device, len(rows))
	for i, d := range rows {
		devices[i] = toDevice(d)
	}
	return devices, nil
}

func (r *DeviceRepository) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.Device, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListDevicesByIDs(ctx, ids)
	if err != nil {
		return nil, mapError(err)
	}
	devices := make([]domain.Device, len(rows))
	for i, d := range rows {
		devices[i] = toDevice(d)
	}
	return devices, nil
}

func (r *DeviceRepository) UpdateLastSeen(ctx context.Context, id uuid.UUID) error {
	return mapError(r.q(ctx).TouchDevice(ctx, id))
}

func (r *DeviceRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	return expectRows(r.q(ctx).RevokeDevice(ctx, id))
}

func (r *DeviceRepository) Update(ctx context.Context, d *domain.Device) error {
	return expectRows(r.q(ctx).UpdateDevice(ctx, db.UpdateDeviceParams{
		ID:          d.ID,
		UserID:      d.UserID,
		DeviceName:  d.DeviceName,
		DeviceType:  d.DeviceType,
		DeviceToken: d.DeviceToken,
		LastSeenAt:  d.LastSeenAt,
		Revoked:     d.Revoked,
	}))
}

func (r *DeviceRepository) UpsertVaultCursor(ctx context.Context, deviceID, vaultID uuid.UUID, lastEventID int64) error {
	return mapError(r.q(ctx).UpsertDeviceVaultCursor(ctx, db.UpsertDeviceVaultCursorParams{
		DeviceID: deviceID, VaultID: vaultID, LastEventID: lastEventID,
	}))
}

func (r *DeviceRepository) ListVaultCursorsByUser(ctx context.Context, userID uuid.UUID) ([]domain.DeviceVaultCursor, error) {
	rows, err := r.q(ctx).ListDeviceVaultCursorsByUser(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}
	cursors := make([]domain.DeviceVaultCursor, len(rows))
	for i, c := range rows {
		cursors[i] = domain.DeviceVaultCursor{DeviceID: c.DeviceID, VaultID: c.VaultID, LastEventID: c.LastEventID, SyncedAt: c.SyncedAt}
	}
	return cursors, nil
}

type RefreshTokenRepository struct{ store }

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{store{pool}}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, t *domain.RefreshToken) error {
	return mapError(r.q(ctx).CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		ID:        t.ID,
		UserID:    t.UserID,
		DeviceID:  t.DeviceID,
		TokenHash: t.TokenHash,
		FamilyID:  t.FamilyID,
		ExpiresAt: t.ExpiresAt,
		Revoked:   t.Revoked,
		CreatedAt: t.CreatedAt,
	}))
}

func (r *RefreshTokenRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	t, err := r.q(ctx).GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		return nil, mapError(err)
	}
	return &domain.RefreshToken{
		ID:        t.ID,
		UserID:    t.UserID,
		DeviceID:  t.DeviceID,
		TokenHash: t.TokenHash,
		FamilyID:  t.FamilyID,
		ExpiresAt: t.ExpiresAt,
		Revoked:   t.Revoked,
		CreatedAt: t.CreatedAt,
	}, nil
}

func (r *RefreshTokenRepository) RevokeByID(ctx context.Context, id uuid.UUID) error {
	return mapError(r.q(ctx).RevokeRefreshToken(ctx, id))
}

func (r *RefreshTokenRepository) RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q(ctx).RevokeRefreshTokenIfActive(ctx, id)
	return n == 1, mapError(err)
}

func (r *RefreshTokenRepository) RevokeByFamilyID(ctx context.Context, familyID uuid.UUID) error {
	return mapError(r.q(ctx).RevokeRefreshTokenFamily(ctx, familyID))
}

func (r *RefreshTokenRepository) RevokeAllByUserID(ctx context.Context, userID uuid.UUID) error {
	return mapError(r.q(ctx).RevokeRefreshTokensByUser(ctx, userID))
}

func (r *RefreshTokenRepository) RevokeAllByDeviceID(ctx context.Context, deviceID uuid.UUID) error {
	return mapError(r.q(ctx).RevokeRefreshTokensByDevice(ctx, deviceID))
}

func (r *RefreshTokenRepository) RevokeAllByUserExceptDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error {
	return mapError(r.q(ctx).RevokeRefreshTokensByUserExceptDevice(ctx, db.RevokeRefreshTokensByUserExceptDeviceParams{UserID: userID, DeviceID: deviceID}))
}

func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context) (int64, error) {
	n, err := r.q(ctx).DeleteExpiredRefreshTokens(ctx)
	return n, mapError(err)
}

func (r *DeviceRepository) ListActiveByUsers(ctx context.Context, userIDs []uuid.UUID) ([]domain.Device, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).ListDevicesByUsers(ctx, userIDs)
	if err != nil {
		return nil, mapError(err)
	}
	devices := make([]domain.Device, len(rows))
	for i, d := range rows {
		devices[i] = toDevice(d)
	}
	return devices, nil
}

func (r *DeviceRepository) ListVaultCursorsByVault(ctx context.Context, vaultID uuid.UUID) ([]domain.DeviceVaultCursor, error) {
	rows, err := r.q(ctx).ListDeviceVaultCursorsByVault(ctx, vaultID)
	if err != nil {
		return nil, mapError(err)
	}
	cursors := make([]domain.DeviceVaultCursor, len(rows))
	for i, c := range rows {
		cursors[i] = domain.DeviceVaultCursor{DeviceID: c.DeviceID, VaultID: c.VaultID, LastEventID: c.LastEventID, SyncedAt: c.SyncedAt}
	}
	return cursors, nil
}
