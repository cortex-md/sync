package device

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
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
