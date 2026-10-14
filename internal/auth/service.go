package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/devops"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Service struct {
	users            UserRepository
	tx               Transactor
	devices          DeviceRepository
	refreshTokens    RefreshTokenRepository
	hasher           PasswordHasher
	tokens           TokenGenerator
	devops           devops.Notifier
	refreshExpiry    time.Duration
	registrationMode string
	preloginSecret   []byte
	onRegistered     func(ctx context.Context, user *domain.User)
}

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Deps struct {
	Users            UserRepository
	Tx               Transactor
	Devices          DeviceRepository
	RefreshTokens    RefreshTokenRepository
	Hasher           PasswordHasher
	Tokens           TokenGenerator
	RefreshExpiry    time.Duration
	RegistrationMode string
	PreloginSecret   []byte
	OnRegistered     func(ctx context.Context, user *domain.User)
	DevOps           devops.Notifier
}

func NewService(d Deps) *Service {
	mode := d.RegistrationMode
	if mode == "" {
		mode = "open"
	}
	return &Service{
		users:            d.Users,
		tx:               d.Tx,
		devices:          d.Devices,
		refreshTokens:    d.RefreshTokens,
		hasher:           d.Hasher,
		tokens:           d.Tokens,
		refreshExpiry:    d.RefreshExpiry,
		registrationMode: mode,
		preloginSecret:   d.PreloginSecret,
		onRegistered:     d.OnRegistered,
		devops:           d.DevOps,
	}
}

type RegisterInput struct {
	Email                     string
	Password                  string
	DisplayName               string
	Locale                    string
	KDFSalt                   []byte
	PublicKey                 []byte
	EncryptedPrivateKey       []byte
	RecoveryWrappedPrivateKey []byte
}

func (in RegisterInput) keyMaterial() (int, error) {
	if len(in.KDFSalt) == 0 {
		if len(in.PublicKey) > 0 || len(in.EncryptedPrivateKey) > 0 || len(in.RecoveryWrappedPrivateKey) > 0 {
			return 0, domain.ErrInvalidInput
		}
		return domain.KDFVersionLegacy, nil
	}
	if len(in.KDFSalt) != domain.KDFSaltSize || len(in.PublicKey) != domain.X25519KeySize ||
		!domain.ValidWrappedKey(in.EncryptedPrivateKey) ||
		(len(in.RecoveryWrappedPrivateKey) > 0 && !domain.ValidWrappedKey(in.RecoveryWrappedPrivateKey)) {
		return 0, domain.ErrInvalidInput
	}
	return domain.KDFVersionArgon2, nil
}

type RegisterOutput struct {
	User *domain.User
}

func (uc *Service) checkFirstUserMode(ctx context.Context) error {
	if uc.registrationMode != "first-user" {
		return nil
	}
	userCount, err := uc.users.Count(ctx)
	if err != nil {
		return err
	}
	if userCount > 0 {
		return domain.ErrRegistrationClosed
	}
	return nil
}

func (uc *Service) createUser(ctx context.Context, user *domain.User) error {
	if uc.tx == nil {
		return uc.users.Create(ctx, user)
	}
	return uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.users.LockRegistrations(ctx); err != nil {
			return err
		}
		if err := uc.checkFirstUserMode(ctx); err != nil {
			return err
		}
		return uc.users.Create(ctx, user)
	})
}

func (uc *Service) Register(ctx context.Context, input RegisterInput) (*RegisterOutput, error) {
	input.Email = domain.NormalizeEmail(input.Email)
	if !domain.ValidEmail(input.Email) || input.Password == "" {
		return nil, domain.ErrInvalidInput
	}

	if len(input.Password) < domain.MinPasswordLength {
		return nil, domain.ErrInvalidInput
	}
	kdfVersion, err := input.keyMaterial()
	if err != nil {
		return nil, err
	}

	if uc.registrationMode == "closed" {
		return nil, domain.ErrRegistrationClosed
	}
	if err := uc.checkFirstUserMode(ctx); err != nil {
		return nil, err
	}

	_, err = uc.users.GetByEmail(ctx, input.Email)
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
		Locale:       domain.SupportedLocale(input.Locale),
		KDFVersion:   kdfVersion,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if kdfVersion == domain.KDFVersionArgon2 {
		user.KDFSalt = input.KDFSalt
		user.PublicKey = input.PublicKey
		user.EncryptedPrivateKey = input.EncryptedPrivateKey
		user.RecoveryWrappedPrivateKey = input.RecoveryWrappedPrivateKey
	}

	if err := uc.createUser(ctx, user); err != nil {
		return nil, err
	}

	if uc.onRegistered != nil {
		uc.onRegistered(ctx, user)
	}

	uc.notifyDevOps(ctx, devops.Event{
		Type:        "account.created",
		UserID:      user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		OccurredAt:  now,
		Fields: map[string]string{
			"registration_mode": uc.registrationMode,
		},
	})

	return &RegisterOutput{User: user}, nil
}

type PreloginOutput struct {
	KDFVersion int
	KDFSalt    []byte
}

func (uc *Service) Prelogin(ctx context.Context, email string) (*PreloginOutput, error) {
	email = domain.NormalizeEmail(email)
	if email == "" {
		return nil, domain.ErrInvalidInput
	}
	user, err := uc.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &PreloginOutput{KDFVersion: domain.KDFVersionArgon2, KDFSalt: FakePreloginSalt(uc.preloginSecret, email)}, nil
		}
		return nil, err
	}
	if user.KDFVersion != domain.KDFVersionArgon2 {
		return &PreloginOutput{KDFVersion: domain.KDFVersionLegacy}, nil
	}
	return &PreloginOutput{KDFVersion: domain.KDFVersionArgon2, KDFSalt: user.KDFSalt}, nil
}

func FakePreloginSalt(secret []byte, email string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("prelogin:" + strings.ToLower(strings.TrimSpace(email))))
	return mac.Sum(nil)[:domain.KDFSaltSize]
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

func (uc *Service) Login(ctx context.Context, input LoginInput) (*LoginOutput, error) {
	input.Email = domain.NormalizeEmail(input.Email)
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
	revokedDeviceReactivated := false
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
		if device.UserID != user.ID {
			uc.notifyDevOps(ctx, devops.Event{
				Type:        "auth.device_conflict",
				UserID:      user.ID,
				Email:       user.Email,
				DisplayName: user.DisplayName,
				OccurredAt:  time.Now(),
				Fields: map[string]string{
					"device_id":     device.ID.String(),
					"owner_user_id": device.UserID.String(),
				},
			})
			return nil, domain.ErrDeviceConflict
		}
		if device.Revoked {
			revokedDeviceReactivated = true
			device.Revoked = false
		}
		device.DeviceName = input.DeviceName
		device.DeviceType = input.DeviceType
		device.LastSeenAt = time.Now()
		if err := uc.devices.Update(ctx, device); err != nil {
			return nil, err
		}
	}

	accessToken, err := uc.tokens.GenerateAccessToken(user.ID, user.Email, device.ID)
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

	if revokedDeviceReactivated {
		uc.notifyDevOps(ctx, devops.Event{
			Type:        "auth.revoked_device_reactivated",
			UserID:      user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			OccurredAt:  time.Now(),
			Fields: map[string]string{
				"device_id":   device.ID.String(),
				"device_name": device.DeviceName,
				"device_type": device.DeviceType,
			},
		})
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

func (uc *Service) Refresh(ctx context.Context, input RefreshInput) (*RefreshOutput, error) {
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

	device, err := uc.devices.GetByID(ctx, stored.DeviceID)
	if err != nil {
		return nil, err
	}
	if device.Revoked || device.UserID != stored.UserID {
		_ = uc.refreshTokens.RevokeByFamilyID(ctx, stored.FamilyID)
		return nil, domain.ErrDeviceRevoked
	}

	consumed, err := uc.refreshTokens.RevokeIfActive(ctx, stored.ID)
	if err != nil {
		return nil, err
	}
	if !consumed {
		_ = uc.refreshTokens.RevokeByFamilyID(ctx, stored.FamilyID)
		return nil, domain.ErrTokenReuseDetected
	}
	_ = uc.devices.UpdateLastSeen(ctx, device.ID)

	user, err := uc.users.GetByID(ctx, stored.UserID)
	if err != nil {
		return nil, err
	}

	accessToken, err := uc.tokens.GenerateAccessToken(user.ID, user.Email, stored.DeviceID)
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

func (uc *Service) Logout(ctx context.Context, input LogoutInput) error {
	if input.AllDevices {
		return uc.refreshTokens.RevokeAllByUserID(ctx, input.UserID)
	}
	return uc.refreshTokens.RevokeAllByDeviceID(ctx, input.DeviceID)
}

func (uc *Service) notifyDevOps(ctx context.Context, event devops.Event) {
	if uc.devops == nil {
		return
	}
	_ = uc.devops.Notify(ctx, event)
}
