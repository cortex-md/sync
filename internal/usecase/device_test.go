package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedDevice(t *testing.T, deviceRepo *fake.DeviceRepository, userID uuid.UUID) *domain.Device {
	t.Helper()
	device := &domain.Device{
		ID:          uuid.New(),
		UserID:      userID,
		DeviceName:  "Test Device",
		DeviceType:  "desktop",
		DeviceToken: uuid.New().String(),
		LastSeenAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	err := deviceRepo.Create(context.Background(), device)
	require.NoError(t, err)
	return device
}

func TestDeviceList_Success(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d1 := seedDevice(t, deviceRepo, userID)
	d2 := seedDevice(t, deviceRepo, userID)

	devices, err := uc.List(context.Background(), userID, d1.ID)
	require.NoError(t, err)
	assert.Len(t, devices, 2)

	var foundCurrent bool
	for _, d := range devices {
		if d.ID == d1.ID {
			assert.True(t, d.IsCurrent)
			foundCurrent = true
		}
		if d.ID == d2.ID {
			assert.False(t, d.IsCurrent)
		}
	}
	assert.True(t, foundCurrent)
}

func TestDeviceList_Empty(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	devices, err := uc.List(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
	assert.Empty(t, devices)
}

func TestDeviceList_OnlyOwnDevices(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	user1 := uuid.New()
	user2 := uuid.New()
	seedDevice(t, deviceRepo, user1)
	seedDevice(t, deviceRepo, user2)

	devices, err := uc.List(context.Background(), user1, uuid.New())
	require.NoError(t, err)
	assert.Len(t, devices, 1)
}

func TestDeviceGet_Success(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	device, err := uc.Get(context.Background(), userID, d.ID)
	require.NoError(t, err)
	assert.Equal(t, d.ID, device.ID)
	assert.Equal(t, "Test Device", device.DeviceName)
}

func TestDeviceGet_NotFound(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	_, err := uc.Get(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceGet_OtherUsersDevice(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	ownerID := uuid.New()
	d := seedDevice(t, deviceRepo, ownerID)

	otherUserID := uuid.New()
	_, err := uc.Get(context.Background(), otherUserID, d.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceRevoke_Success(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	currentDevice := seedDevice(t, deviceRepo, userID)
	targetDevice := seedDevice(t, deviceRepo, userID)

	err := uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          userID,
		DeviceID:        targetDevice.ID,
		CurrentDeviceID: currentDevice.ID,
	})
	require.NoError(t, err)

	device, err := uc.Get(context.Background(), userID, targetDevice.ID)
	require.NoError(t, err)
	assert.True(t, device.Revoked)
}

func TestDeviceRevoke_CannotRevokeSelf(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	err := uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          userID,
		DeviceID:        d.ID,
		CurrentDeviceID: d.ID,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestDeviceRevoke_NotFound(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	err := uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          uuid.New(),
		DeviceID:        uuid.New(),
		CurrentDeviceID: uuid.New(),
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceRevoke_OtherUsersDevice(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	ownerID := uuid.New()
	d := seedDevice(t, deviceRepo, ownerID)

	err := uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          uuid.New(),
		DeviceID:        d.ID,
		CurrentDeviceID: uuid.New(),
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceRevoke_AlreadyRevoked(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	currentDevice := seedDevice(t, deviceRepo, userID)
	targetDevice := seedDevice(t, deviceRepo, userID)

	err := uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          userID,
		DeviceID:        targetDevice.ID,
		CurrentDeviceID: currentDevice.ID,
	})
	require.NoError(t, err)

	err = uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          userID,
		DeviceID:        targetDevice.ID,
		CurrentDeviceID: currentDevice.ID,
	})
	require.NoError(t, err)
}

func TestDeviceRevoke_RevokesRefreshTokens(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	currentDevice := seedDevice(t, deviceRepo, userID)
	targetDevice := seedDevice(t, deviceRepo, userID)

	rt := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    userID,
		DeviceID:  targetDevice.ID,
		TokenHash: "test-hash",
		FamilyID:  uuid.New(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
	err := refreshRepo.Create(context.Background(), rt)
	require.NoError(t, err)

	err = uc.Revoke(context.Background(), usecase.RevokeDeviceInput{
		UserID:          userID,
		DeviceID:        targetDevice.ID,
		CurrentDeviceID: currentDevice.ID,
	})
	require.NoError(t, err)

	storedRT, err := refreshRepo.GetByTokenHash(context.Background(), "test-hash")
	require.NoError(t, err)
	assert.True(t, storedRT.Revoked)
}

func TestDeviceUpdate_Success(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	updated, err := uc.Update(context.Background(), usecase.UpdateDeviceInput{
		UserID:     userID,
		DeviceID:   d.ID,
		DeviceName: "Renamed Device",
	})
	require.NoError(t, err)
	assert.Equal(t, "Renamed Device", updated.DeviceName)
}

func TestDeviceUpdate_EmptyName(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	_, err := uc.Update(context.Background(), usecase.UpdateDeviceInput{
		UserID:     userID,
		DeviceID:   d.ID,
		DeviceName: "",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestDeviceUpdate_NotFound(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	_, err := uc.Update(context.Background(), usecase.UpdateDeviceInput{
		UserID:     uuid.New(),
		DeviceID:   uuid.New(),
		DeviceName: "New Name",
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceUpdate_OtherUsersDevice(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	ownerID := uuid.New()
	d := seedDevice(t, deviceRepo, ownerID)

	_, err := uc.Update(context.Background(), usecase.UpdateDeviceInput{
		UserID:     uuid.New(),
		DeviceID:   d.ID,
		DeviceName: "Hijacked",
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceValidate_Success(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	err := uc.ValidateDevice(context.Background(), userID, d.ID)
	require.NoError(t, err)
}

func TestDeviceValidate_Revoked(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	err := deviceRepo.Revoke(context.Background(), d.ID)
	require.NoError(t, err)

	err = uc.ValidateDevice(context.Background(), userID, d.ID)
	assert.ErrorIs(t, err, domain.ErrDeviceRevoked)
}

func TestDeviceValidate_NotFound(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	err := uc.ValidateDevice(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceValidate_WrongUser(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	ownerID := uuid.New()
	d := seedDevice(t, deviceRepo, ownerID)

	err := uc.ValidateDevice(context.Background(), uuid.New(), d.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceUpdateSyncCursor_Success(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	err := uc.UpdateSyncCursor(context.Background(), usecase.UpdateSyncCursorInput{
		UserID:          userID,
		DeviceID:        d.ID,
		LastSyncEventID: 42,
	})
	require.NoError(t, err)

	device, err := deviceRepo.GetByID(context.Background(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(42), device.LastSyncEventID)
}

func TestDeviceUpdateSyncCursor_NotFound(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	err := uc.UpdateSyncCursor(context.Background(), usecase.UpdateSyncCursorInput{
		UserID:          uuid.New(),
		DeviceID:        uuid.New(),
		LastSyncEventID: 42,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceUpdateSyncCursor_OtherUsersDevice(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	ownerID := uuid.New()
	d := seedDevice(t, deviceRepo, ownerID)

	err := uc.UpdateSyncCursor(context.Background(), usecase.UpdateSyncCursorInput{
		UserID:          uuid.New(),
		DeviceID:        d.ID,
		LastSyncEventID: 42,
	})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDeviceUpdateSyncCursor_RevokedDevice(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)
	err := deviceRepo.Revoke(context.Background(), d.ID)
	require.NoError(t, err)

	err = uc.UpdateSyncCursor(context.Background(), usecase.UpdateSyncCursorInput{
		UserID:          userID,
		DeviceID:        d.ID,
		LastSyncEventID: 42,
	})
	assert.ErrorIs(t, err, domain.ErrDeviceRevoked)
}

func TestDeviceUpdateSyncCursor_NegativeEventID(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	err := uc.UpdateSyncCursor(context.Background(), usecase.UpdateSyncCursorInput{
		UserID:          userID,
		DeviceID:        d.ID,
		LastSyncEventID: -1,
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestDeviceUpdateSyncCursor_ZeroEventID(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)

	err := uc.UpdateSyncCursor(context.Background(), usecase.UpdateSyncCursorInput{
		UserID:          userID,
		DeviceID:        d.ID,
		LastSyncEventID: 0,
	})
	require.NoError(t, err)

	device, err := deviceRepo.GetByID(context.Background(), d.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), device.LastSyncEventID)
}

func TestDeviceList_IncludesSyncCursor(t *testing.T) {
	deviceRepo := fake.NewDeviceRepository()
	refreshRepo := fake.NewRefreshTokenRepository()
	uc := usecase.NewDeviceUsecase(deviceRepo, refreshRepo)

	userID := uuid.New()
	d := seedDevice(t, deviceRepo, userID)
	err := deviceRepo.UpdateSyncCursor(context.Background(), d.ID, 99)
	require.NoError(t, err)

	devices, err := uc.List(context.Background(), userID, d.ID)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	assert.Equal(t, int64(99), devices[0].LastSyncEventID)
}
