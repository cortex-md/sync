package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type AuthUsecase struct {
	users         port.UserRepository
	devices       port.DeviceRepository
	refreshTokens port.RefreshTokenRepository
	hasher        port.PasswordHasher
	tokens        port.TokenGenerator
	refreshExpiry time.Duration
}

func NewAuthUsecase(
	users port.UserRepository,
	devices port.DeviceRepository,
	refreshTokens port.RefreshTokenRepository,
	hasher port.PasswordHasher,
	tokens port.TokenGenerator,
	refreshExpiry time.Duration,
) *AuthUsecase {
	return &AuthUsecase{
		users:         users,
		devices:       devices,
		refreshTokens: refreshTokens,
		hasher:        hasher,
		tokens:        tokens,
		refreshExpiry: refreshExpiry,
	}
}

type RegisterInput struct {
	Email       string
	Password    string
	DisplayName string
}

type RegisterOutput struct {
	User *domain.User
}

func (uc *AuthUsecase) Register(ctx context.Context, input RegisterInput) (*RegisterOutput, error) {
	if input.Email == "" || input.Password == "" {
		return nil, domain.ErrInvalidInput
	}

	if len(input.Password) < 8 {
		return nil, domain.ErrInvalidInput
	}

	_, err := uc.users.GetByEmail(ctx, input.Email)
	if err == nil {
		return nil, domain.ErrAlreadyExists
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	hash, err := uc.hasher.Hash(input.Password)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	user := &domain.User{
		ID:           uuid.New(),
		Email:        input.Email,
		PasswordHash: hash,
		DisplayName:  input.DisplayName,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := uc.users.Create(ctx, user); err != nil {
		return nil, err
	}

	return &RegisterOutput{User: user}, nil
}

type LoginInput struct {
	Email      string
	Password   string
	DeviceID   uuid.UUID
	DeviceName string
	DeviceType string
}

type LoginOutput struct {
	AccessToken  string
	RefreshToken string
	User         *domain.User
}

func (uc *AuthUsecase) Login(ctx context.Context, input LoginInput) (*LoginOutput, error) {
	if input.Email == "" || input.Password == "" {
		return nil, domain.ErrInvalidInput
	}

	user, err := uc.users.GetByEmail(ctx, input.Email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}

	if err := uc.hasher.Compare(user.PasswordHash, input.Password); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	device, err := uc.devices.GetByID(ctx, input.DeviceID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}

		deviceToken, err := uc.tokens.GenerateRefreshToken()
		if err != nil {
			return nil, err
		}

		device = &domain.Device{
			ID:          input.DeviceID,
			UserID:      user.ID,
			DeviceName:  input.DeviceName,
			DeviceType:  input.DeviceType,
			DeviceToken: uc.tokens.HashToken(deviceToken),
			LastSeenAt:  time.Now(),
			CreatedAt:   time.Now(),
		}

		if err := uc.devices.Create(ctx, device); err != nil {
			return nil, err
		}
	} else {
		if device.Revoked {
			device.Revoked = false
		}
		if device.UserID != user.ID {
			if err := uc.refreshTokens.RevokeAllByDeviceID(ctx, device.ID); err != nil {
				return nil, err
			}
			device.UserID = user.ID
		}
		device.DeviceName = input.DeviceName
		device.DeviceType = input.DeviceType
		device.LastSeenAt = time.Now()
		if err := uc.devices.Update(ctx, device); err != nil {
			return nil, err
		}
	}

	accessToken, err := uc.tokens.GenerateAccessToken(user.ID, user.Email)
	if err != nil {
		return nil, err
	}

	refreshToken, err := uc.tokens.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	familyID := uuid.New()
	rt := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		DeviceID:  device.ID,
		TokenHash: uc.tokens.HashToken(refreshToken),
		FamilyID:  familyID,
		ExpiresAt: time.Now().Add(uc.refreshExpiry),
		CreatedAt: time.Now(),
	}

	if err := uc.refreshTokens.Create(ctx, rt); err != nil {
		return nil, err
	}

	return &LoginOutput{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         user,
	}, nil
}

type RefreshInput struct {
	RefreshToken string
}

type RefreshOutput struct {
	AccessToken  string
	RefreshToken string
}

func (uc *AuthUsecase) Refresh(ctx context.Context, input RefreshInput) (*RefreshOutput, error) {
	if input.RefreshToken == "" {
		return nil, domain.ErrInvalidInput
	}

	tokenHash := uc.tokens.HashToken(input.RefreshToken)
	stored, err := uc.refreshTokens.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}

	if stored.Revoked {
		_ = uc.refreshTokens.RevokeByFamilyID(ctx, stored.FamilyID)
		return nil, domain.ErrTokenReuseDetected
	}

	if time.Now().After(stored.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	if err := uc.refreshTokens.RevokeByID(ctx, stored.ID); err != nil {
		return nil, err
	}

	user, err := uc.users.GetByID(ctx, stored.UserID)
	if err != nil {
		return nil, err
	}

	accessToken, err := uc.tokens.GenerateAccessToken(user.ID, user.Email)
	if err != nil {
		return nil, err
	}

	newRefreshToken, err := uc.tokens.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	newRT := &domain.RefreshToken{
		ID:        uuid.New(),
		UserID:    stored.UserID,
		DeviceID:  stored.DeviceID,
		TokenHash: uc.tokens.HashToken(newRefreshToken),
		FamilyID:  stored.FamilyID,
		ExpiresAt: time.Now().Add(uc.refreshExpiry),
		CreatedAt: time.Now(),
	}

	if err := uc.refreshTokens.Create(ctx, newRT); err != nil {
		return nil, err
	}

	return &RefreshOutput{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
	}, nil
}

type LogoutInput struct {
	UserID     uuid.UUID
	DeviceID   uuid.UUID
	AllDevices bool
}

func (uc *AuthUsecase) Logout(ctx context.Context, input LogoutInput) error {
	if input.AllDevices {
		return uc.refreshTokens.RevokeAllByUserID(ctx, input.UserID)
	}
	return uc.refreshTokens.RevokeAllByDeviceID(ctx, input.DeviceID)
}
