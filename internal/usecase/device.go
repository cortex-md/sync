package usecase

import (
	"context"
	"errors"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type DeviceUsecase struct {
	devices       port.DeviceRepository
	refreshTokens port.RefreshTokenRepository
}

func NewDeviceUsecase(devices port.DeviceRepository, refreshTokens port.RefreshTokenRepository) *DeviceUsecase {
	return &DeviceUsecase{
		devices:       devices,
		refreshTokens: refreshTokens,
	}
}

type DeviceInfo struct {
	ID              uuid.UUID
	DeviceName      string
	DeviceType      string
	LastSeenAt      string
	CreatedAt       string
	Revoked         bool
	IsCurrent       bool
	LastSyncEventID int64
}

func (uc *DeviceUsecase) List(ctx context.Context, userID uuid.UUID, currentDeviceID uuid.UUID) ([]DeviceInfo, error) {
	devices, err := uc.devices.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	result := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		result = append(result, DeviceInfo{
			ID:              d.ID,
			DeviceName:      d.DeviceName,
			DeviceType:      d.DeviceType,
			LastSeenAt:      d.LastSeenAt.Format("2006-01-02T15:04:05Z"),
			CreatedAt:       d.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Revoked:         d.Revoked,
			IsCurrent:       d.ID == currentDeviceID,
			LastSyncEventID: d.LastSyncEventID,
		})
	}
	return result, nil
}

func (uc *DeviceUsecase) Get(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) (*DeviceInfo, error) {
	device, err := uc.devices.GetByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	if device.UserID != userID {
		return nil, domain.ErrNotFound
	}

	return &DeviceInfo{
		ID:              device.ID,
		DeviceName:      device.DeviceName,
		DeviceType:      device.DeviceType,
		LastSeenAt:      device.LastSeenAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:       device.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Revoked:         device.Revoked,
		LastSyncEventID: device.LastSyncEventID,
	}, nil
}

type RevokeDeviceInput struct {
	UserID          uuid.UUID
	DeviceID        uuid.UUID
	CurrentDeviceID uuid.UUID
}

func (uc *DeviceUsecase) Revoke(ctx context.Context, input RevokeDeviceInput) error {
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

	return uc.refreshTokens.RevokeAllByDeviceID(ctx, device.ID)
}

type UpdateDeviceInput struct {
	UserID     uuid.UUID
	DeviceID   uuid.UUID
	DeviceName string
}

func (uc *DeviceUsecase) Update(ctx context.Context, input UpdateDeviceInput) (*DeviceInfo, error) {
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
		ID:              device.ID,
		DeviceName:      device.DeviceName,
		DeviceType:      device.DeviceType,
		LastSeenAt:      device.LastSeenAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:       device.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Revoked:         device.Revoked,
		LastSyncEventID: device.LastSyncEventID,
	}, nil
}

type UpdateSyncCursorInput struct {
	UserID          uuid.UUID
	DeviceID        uuid.UUID
	LastSyncEventID int64
}

func (uc *DeviceUsecase) UpdateSyncCursor(ctx context.Context, input UpdateSyncCursorInput) error {
	device, err := uc.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		return err
	}

	if device.UserID != input.UserID {
		return domain.ErrNotFound
	}

	if device.Revoked {
		return domain.ErrDeviceRevoked
	}

	if input.LastSyncEventID < 0 {
		return domain.ErrInvalidInput
	}

	return uc.devices.UpdateSyncCursor(ctx, input.DeviceID, input.LastSyncEventID)
}

func (uc *DeviceUsecase) ValidateDevice(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error {
	device, err := uc.devices.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrNotFound
		}
		return err
	}

	if device.UserID != userID {
		return domain.ErrNotFound
	}

	if device.Revoked {
		return domain.ErrDeviceRevoked
	}

	return uc.devices.UpdateLastSeen(ctx, deviceID)
}
