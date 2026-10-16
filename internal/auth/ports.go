package auth

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/httpx"
	"github.com/google/uuid"
)

type DeviceRepository interface {
	Create(ctx context.Context, device *domain.Device) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Device, error)
	GetByToken(ctx context.Context, tokenHash string) (*domain.Device, error)
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Device, error)
	UpdateLastSeen(ctx context.Context, id uuid.UUID) error
	Revoke(ctx context.Context, id uuid.UUID) error
	Update(ctx context.Context, device *domain.Device) error
}

type PasswordHasher interface {
	HashFor(kdfVersion int, password string) (string, error)
	Compare(hash string, password string) error
	NeedsRehash(hash string, kdfVersion int) bool
	DummyHash() string
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, token *domain.RefreshToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error)
	RevokeByID(ctx context.Context, id uuid.UUID) error
	RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error)
	RevokeByFamilyID(ctx context.Context, familyID uuid.UUID) error
	RevokeAllByUserID(ctx context.Context, userID uuid.UUID) error
	RevokeAllByDeviceID(ctx context.Context, deviceID uuid.UUID) error
	RevokeAllByUserExceptDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type TokenGenerator interface {
	GenerateAccessToken(userID uuid.UUID, email string, deviceID uuid.UUID) (string, error)
	ValidateAccessToken(tokenString string) (*httpx.Claims, error)
	GenerateRefreshToken() (string, error)
	HashToken(token string) string
}

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Count(ctx context.Context) (int64, error)
	LockRegistrations(ctx context.Context) error
	UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error
	Update(ctx context.Context, user *domain.User) error
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error)
}

type TokenRevoker interface {
	RevokeDevice(ctx context.Context, deviceID uuid.UUID)
	RevokeUser(ctx context.Context, userID uuid.UUID)
}
