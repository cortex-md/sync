package device

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Service struct {
	revoker       TokenRevoker
	devices       DeviceRepository
	refreshTokens RefreshTokenRepository
}

type Deps struct {
	Devices       DeviceRepository
	RefreshTokens RefreshTokenRepository
	Revoker       TokenRevoker
}

func NewService(d Deps) *Service {
	return &Service{
		devices:       d.Devices,
		refreshTokens: d.RefreshTokens,
		revoker:       d.Revoker,
	}
}

type DeviceInfo struct {
	ID         uuid.UUID
	DeviceName string
	DeviceType string
	LastSeenAt string
	CreatedAt  string
	Revoked    bool
	IsCurrent  bool
}

func (uc *Service) List(ctx context.Context, userID uuid.UUID, currentDeviceID uuid.UUID) ([]DeviceInfo, error) {
	devices, err := uc.devices.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	result := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		result = append(result, DeviceInfo{
			ID:         d.ID,
			DeviceName: d.DeviceName,
			DeviceType: d.DeviceType,
			LastSeenAt: d.LastSeenAt.Format("2006-01-02T15:04:05Z"),
			CreatedAt:  d.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Revoked:    d.Revoked,
			IsCurrent:  d.ID == currentDeviceID,
		})
	}
	return result, nil
}

func (uc *Service) Get(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (*DeviceInfo, error) {
	device, err := uc.devices.GetByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	if device.UserID != userID {
		return nil, domain.ErrNotFound
	}

	return &DeviceInfo{
		ID:         device.ID,
		DeviceName: device.DeviceName,
		DeviceType: device.DeviceType,
		LastSeenAt: device.LastSeenAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:  device.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Revoked:    device.Revoked,
	}, nil
}

type RevokeDeviceInput struct {
	UserID          uuid.UUID
	DeviceID        uuid.UUID
	CurrentDeviceID uuid.UUID
}

func (uc *Service) Revoke(ctx context.Context, input RevokeDeviceInput) error {
	device, err := uc.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		return err
	}

	if device.UserID != input.UserID {
		return domain.ErrNotFound
	}

	if device.ID == input.CurrentDeviceID {
		return domain.ErrInvalidInput
	}

	if device.Revoked {
		return nil
	}

	if err := uc.devices.Revoke(ctx, device.ID); err != nil {
		return err
	}

	if err := uc.refreshTokens.RevokeAllByDeviceID(ctx, device.ID); err != nil {
		return err
	}
	if uc.revoker != nil {
		uc.revoker.RevokeDevice(ctx, device.ID)
	}
	return nil
}

type UpdateDeviceInput struct {
	UserID     uuid.UUID
	DeviceID   uuid.UUID
	DeviceName string
}

func (uc *Service) Update(ctx context.Context, input UpdateDeviceInput) (*DeviceInfo, error) {
	device, err := uc.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		return nil, err
	}

	if device.UserID != input.UserID {
		return nil, domain.ErrNotFound
	}

	if input.DeviceName == "" {
		return nil, domain.ErrInvalidInput
	}

	device.DeviceName = input.DeviceName
	if err := uc.devices.Update(ctx, device); err != nil {
		return nil, err
	}

	return &DeviceInfo{
		ID:         device.ID,
		DeviceName: device.DeviceName,
		DeviceType: device.DeviceType,
		LastSeenAt: device.LastSeenAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:  device.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Revoked:    device.Revoked,
	}, nil
}
