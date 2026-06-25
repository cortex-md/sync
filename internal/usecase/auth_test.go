package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAuthUsecase() (*usecase.AuthUsecase, *fake.UserRepository, *fake.DeviceRepository, *fake.RefreshTokenRepository) {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")
	uc := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	return uc, userRepo, deviceRepo, refreshTokenRepo
}

func TestRegister_Success(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	output, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:       "test@example.com",
		Password:    "password123",
		DisplayName: "Test User",
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, output.User.ID)
	assert.Equal(t, "test@example.com", output.User.Email)
	assert.Equal(t, "Test User", output.User.DisplayName)
	assert.NotEmpty(t, output.User.PasswordHash)
}

func TestRegister_NotifiesDevOps(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()
	notifier := &recordingDevOpsNotifier{}
	uc.SetDevOpsNotifier(notifier)

	output, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:       "test@example.com",
		Password:    "password123",
		DisplayName: "Test User",
	})

	require.NoError(t, err)
	require.Len(t, notifier.events, 1)
	event := notifier.events[0]
	assert.Equal(t, "account.created", event.Type)
	assert.Equal(t, output.User.ID, event.UserID)
	assert.Equal(t, "test@example.com", event.Email)
	assert.Equal(t, "Test User", event.DisplayName)
	assert.Equal(t, "open", event.Fields["registration_mode"])
}

func TestRegister_IgnoresDevOpsNotifierError(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()
	notifier := &recordingDevOpsNotifier{err: errors.New("discord unavailable")}
	uc.SetDevOpsNotifier(notifier)

	output, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:       "test@example.com",
		Password:    "password123",
		DisplayName: "Test User",
	})

	require.NoError(t, err)
	require.NotNil(t, output)
	require.Len(t, notifier.events, 1)
}

func TestRegister_DuplicateEmail(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	_, err = uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password456",
	})
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

func TestRegister_ClosedRegistration(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()
	uc.SetRegistrationMode("closed")

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:       "user@example.com",
		Password:    "password123",
		DisplayName: "User",
	})

	assert.ErrorIs(t, err, domain.ErrRegistrationClosed)
}

func TestRegister_FirstUserRegistration(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()
	uc.SetRegistrationMode("first-user")

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:       "owner@example.com",
		Password:    "password123",
		DisplayName: "Owner",
	})
	require.NoError(t, err)

	_, err = uc.Register(context.Background(), usecase.RegisterInput{
		Email:       "second@example.com",
		Password:    "password123",
		DisplayName: "Second",
	})

	assert.ErrorIs(t, err, domain.ErrRegistrationClosed)
}

func TestRegister_EmptyEmail(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "",
		Password: "password123",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestRegister_ShortPassword(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "short",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestLogin_Success(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	deviceID := uuid.New()
	output, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:      "test@example.com",
		Password:   "password123",
		DeviceID:   deviceID,
		DeviceName: "Test Device",
		DeviceType: "desktop",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, output.AccessToken)
	assert.NotEmpty(t, output.RefreshToken)
	assert.Equal(t, "test@example.com", output.User.Email)
}

func TestLogin_WrongPassword(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	_, err = uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "wrongpassword",
		DeviceID: uuid.New(),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestLogin_NonExistentUser(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "nonexistent@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestLogin_RevokedDeviceReactivated(t *testing.T) {
	uc, _, deviceRepo, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	deviceID := uuid.New()
	_, err = uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: deviceID,
	})
	require.NoError(t, err)

	err = deviceRepo.Revoke(context.Background(), deviceID)
	require.NoError(t, err)

	notifier := &recordingDevOpsNotifier{}
	uc.SetDevOpsNotifier(notifier)

	out, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: deviceID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.AccessToken)

	device, err := deviceRepo.GetByID(context.Background(), deviceID)
	require.NoError(t, err)
	assert.False(t, device.Revoked)
	require.Len(t, notifier.events, 1)
	assert.Equal(t, "auth.revoked_device_reactivated", notifier.events[0].Type)
	assert.Equal(t, deviceID.String(), notifier.events[0].Fields["device_id"])
}

func TestLogin_DeviceReassignedToNewUser(t *testing.T) {
	uc, _, deviceRepo, _ := newTestAuthUsecase()

	regA, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "userA@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	regB, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "userB@example.com",
		Password: "password456",
	})
	require.NoError(t, err)

	deviceID := uuid.New()
	_, err = uc.Login(context.Background(), usecase.LoginInput{
		Email:    "userA@example.com",
		Password: "password123",
		DeviceID: deviceID,
	})
	require.NoError(t, err)

	notifier := &recordingDevOpsNotifier{}
	uc.SetDevOpsNotifier(notifier)

	outB, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "userB@example.com",
		Password: "password456",
		DeviceID: deviceID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, outB.AccessToken)
	assert.Equal(t, regB.User.ID, outB.User.ID)

	device, err := deviceRepo.GetByID(context.Background(), deviceID)
	require.NoError(t, err)
	assert.Equal(t, regB.User.ID, device.UserID)
	assert.NotEqual(t, regA.User.ID, device.UserID)
	require.Len(t, notifier.events, 1)
	assert.Equal(t, "auth.device_reassigned", notifier.events[0].Type)
	assert.Equal(t, regB.User.ID, notifier.events[0].UserID)
	assert.Equal(t, regA.User.ID.String(), notifier.events[0].Fields["previous_user_id"])
	assert.Equal(t, deviceID.String(), notifier.events[0].Fields["device_id"])
}

func TestLogin_SameDeviceReturnsNewTokens(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	deviceID := uuid.New()
	out1, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: deviceID,
	})
	require.NoError(t, err)

	out2, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: deviceID,
	})
	require.NoError(t, err)

	assert.NotEqual(t, out1.RefreshToken, out2.RefreshToken)
	assert.NotEqual(t, out1.AccessToken, out2.AccessToken)
}

func TestRefresh_Success(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	loginOut, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	require.NoError(t, err)

	refreshOut, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, refreshOut.AccessToken)
	assert.NotEmpty(t, refreshOut.RefreshToken)
	assert.NotEqual(t, loginOut.RefreshToken, refreshOut.RefreshToken)
	assert.NotEqual(t, loginOut.AccessToken, refreshOut.AccessToken)
}

func TestRefresh_RotationChain(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	loginOut, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	require.NoError(t, err)

	refresh1, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})
	require.NoError(t, err)

	refresh2, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: refresh1.RefreshToken,
	})
	require.NoError(t, err)

	refresh3, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: refresh2.RefreshToken,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, refresh3.AccessToken)
}

func TestRefresh_ReuseDetection(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	loginOut, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})
	assert.ErrorIs(t, err, domain.ErrTokenReuseDetected)
}

func TestRefresh_ReuseDetectionRevokesEntireFamily(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	loginOut, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	require.NoError(t, err)

	refresh1, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})
	require.NoError(t, err)

	refresh2, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: refresh1.RefreshToken,
	})
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})
	assert.ErrorIs(t, err, domain.ErrTokenReuseDetected)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: refresh2.RefreshToken,
	})
	assert.ErrorIs(t, err, domain.ErrTokenReuseDetected)
}

func TestRefresh_InvalidToken(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: "totally-invalid-token",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestRefresh_EmptyToken(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: "",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestLogout_SingleDevice(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	deviceID := uuid.New()
	loginOut, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: deviceID,
	})
	require.NoError(t, err)

	err = uc.Logout(context.Background(), usecase.LogoutInput{
		UserID:   loginOut.User.ID,
		DeviceID: deviceID,
	})
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: loginOut.RefreshToken,
	})
	assert.ErrorIs(t, err, domain.ErrTokenReuseDetected)
}

func TestLogout_AllDevices(t *testing.T) {
	uc, _, _, _ := newTestAuthUsecase()

	_, err := uc.Register(context.Background(), usecase.RegisterInput{
		Email:    "test@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	login1, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	require.NoError(t, err)

	login2, err := uc.Login(context.Background(), usecase.LoginInput{
		Email:    "test@example.com",
		Password: "password123",
		DeviceID: uuid.New(),
	})
	require.NoError(t, err)

	err = uc.Logout(context.Background(), usecase.LogoutInput{
		UserID:     login1.User.ID,
		AllDevices: true,
	})
	require.NoError(t, err)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: login1.RefreshToken,
	})
	assert.ErrorIs(t, err, domain.ErrTokenReuseDetected)

	_, err = uc.Refresh(context.Background(), usecase.RefreshInput{
		RefreshToken: login2.RefreshToken,
	})
	assert.ErrorIs(t, err, domain.ErrTokenReuseDetected)
}
