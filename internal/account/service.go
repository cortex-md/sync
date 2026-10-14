package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/cortexnotes/cortex-sync/internal/platform/secret"
	"github.com/google/uuid"
)

const (
	maxDisplayNameRunes = 80
	emailTokenTTL       = 24 * time.Hour
	passwordResetTTL    = time.Hour
)

type Service struct {
	users         UserRepository
	members       MembershipRepository
	vaultKeys     VaultKeyRepository
	bus           bus.Bus
	tokens        UserTokenRepository
	refreshTokens RefreshTokenRepository
	outbox        EmailOutboxRepository
	hasher        PasswordHasher
	blobs         BlobStorage
	avatars       AvatarProcessor
	tx            Transactor
	publicURL     string
	blobQueue     BlobQueue
	verification  bool
}

type Deps struct {
	Users         UserRepository
	Members       MembershipRepository
	VaultKeys     VaultKeyRepository
	Bus           bus.Bus
	Tokens        UserTokenRepository
	RefreshTokens RefreshTokenRepository
	Outbox        EmailOutboxRepository
	Hasher        PasswordHasher
	Blobs         BlobStorage
	Avatars       AvatarProcessor
	Tx            Transactor
	BlobQueue     BlobQueue
	PublicURL     string
	Verification  bool
}

func NewService(deps Deps) *Service {
	return &Service{
		blobQueue:     deps.BlobQueue,
		verification:  deps.Verification,
		users:         deps.Users,
		members:       deps.Members,
		vaultKeys:     deps.VaultKeys,
		bus:           deps.Bus,
		tokens:        deps.Tokens,
		refreshTokens: deps.RefreshTokens,
		outbox:        deps.Outbox,
		hasher:        deps.Hasher,
		blobs:         deps.Blobs,
		avatars:       deps.Avatars,
		tx:            deps.Tx,
		publicURL:     deps.PublicURL,
	}
}

func (uc *Service) Me(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	return uc.users.GetByID(ctx, userID)
}

type UpdateProfileInput struct {
	DisplayName *string
	Locale      *string
}

func (uc *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, input UpdateProfileInput) (*domain.User, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if name == "" || utf8.RuneCountInString(name) > maxDisplayNameRunes {
			return nil, domain.ErrInvalidInput
		}
		user.DisplayName = name
	}
	if input.Locale != nil {
		user.Locale = domain.SupportedLocale(*input.Locale)
	}

	if err := uc.users.Update(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

type ChangePasswordInput struct {
	UserID              uuid.UUID
	DeviceID            uuid.UUID
	CurrentPassword     string
	NewPassword         string
	KDFSalt             []byte
	EncryptedPrivateKey []byte
}

func (uc *Service) ChangePassword(ctx context.Context, input ChangePasswordInput) error {
	if len(input.NewPassword) < domain.MinPasswordLength {
		return domain.ErrInvalidInput
	}

	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return err
	}
	rewrapped := len(input.KDFSalt) > 0 || len(input.EncryptedPrivateKey) > 0
	if user.KDFVersion == domain.KDFVersionArgon2 {
		if len(input.KDFSalt) != domain.KDFSaltSize || !domain.ValidWrappedKey(input.EncryptedPrivateKey) {
			return domain.ErrInvalidInput
		}
	} else if rewrapped {
		return domain.ErrInvalidInput
	}
	if err := uc.hasher.Compare(user.PasswordHash, input.CurrentPassword); err != nil {
		return domain.ErrInvalidCredentials
	}

	hash, err := uc.hasher.Hash(input.NewPassword)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	if user.KDFVersion == domain.KDFVersionArgon2 {
		user.KDFSalt = input.KDFSalt
		user.EncryptedPrivateKey = input.EncryptedPrivateKey
	}

	return uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.users.Update(ctx, user); err != nil {
			return err
		}
		if err := uc.refreshTokens.RevokeAllByUserExceptDevice(ctx, user.ID, input.DeviceID); err != nil {
			return err
		}
		if err := uc.tokens.DeleteByUserAndKind(ctx, user.ID, domain.UserTokenPasswordReset); err != nil {
			return err
		}
		return uc.enqueue(ctx, user.Email, domain.EmailTemplatePasswordChanged, user, nil)
	})
}

type RequestEmailChangeInput struct {
	UserID   uuid.UUID
	Password string
	NewEmail string
}

func (uc *Service) RequestEmailChange(ctx context.Context, input RequestEmailChangeInput) (bool, error) {
	newEmail := domain.NormalizeEmail(input.NewEmail)
	if !domain.ValidEmail(newEmail) {
		return false, domain.ErrInvalidInput
	}

	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return false, err
	}
	if err := uc.hasher.Compare(user.PasswordHash, input.Password); err != nil {
		return false, domain.ErrInvalidCredentials
	}
	if newEmail == domain.NormalizeEmail(user.Email) {
		return false, domain.ErrInvalidInput
	}
	if err := uc.ensureEmailAvailable(ctx, newEmail); err != nil {
		return false, err
	}

	if !uc.verification {
		oldEmail := user.Email
		user.Email = newEmail
		user.EmailVerifiedAt = nil
		return true, uc.tx.RunInTx(ctx, func(ctx context.Context) error {
			if err := uc.users.Update(ctx, user); err != nil {
				return err
			}
			return uc.enqueue(ctx, oldEmail, domain.EmailTemplateEmailChanged, user, map[string]string{
				"NewEmail": newEmail,
			})
		})
	}

	return false, uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tokens.DeleteByUserAndKind(ctx, user.ID, domain.UserTokenEmailChange); err != nil {
			return err
		}
		raw, err := uc.issueToken(ctx, user.ID, domain.UserTokenEmailChange, newEmail, emailTokenTTL)
		if err != nil {
			return err
		}
		if err := uc.enqueue(ctx, newEmail, domain.EmailTemplateEmailChange, user, map[string]string{
			"NewEmail": newEmail,
			"Link":     uc.link("/account/v1/email/confirm-change", raw),
		}); err != nil {
			return err
		}
		return uc.enqueue(ctx, user.Email, domain.EmailTemplateEmailChanged, user, map[string]string{
			"NewEmail": newEmail,
		})
	})
}

func (uc *Service) ConfirmEmailChange(ctx context.Context, rawToken string) (*domain.User, error) {
	var user *domain.User
	err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		token, err := uc.consume(ctx, domain.UserTokenEmailChange, rawToken)
		if err != nil {
			return err
		}
		if err := uc.ensureEmailAvailable(ctx, token.Payload); err != nil {
			return err
		}
		user, err = uc.users.GetByID(ctx, token.UserID)
		if err != nil {
			return err
		}
		now := time.Now()
		user.Email = token.Payload
		user.EmailVerifiedAt = &now
		return uc.users.Update(ctx, user)
	})
	return user, err
}

func (uc *Service) SendEmailVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.EmailVerifiedAt != nil {
		return nil
	}

	return uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tokens.DeleteByUserAndKind(ctx, user.ID, domain.UserTokenEmailVerify); err != nil {
			return err
		}
		raw, err := uc.issueToken(ctx, user.ID, domain.UserTokenEmailVerify, domain.NormalizeEmail(user.Email), emailTokenTTL)
		if err != nil {
			return err
		}
		return uc.enqueue(ctx, user.Email, domain.EmailTemplateVerifyEmail, user, map[string]string{
			"Link": uc.link("/account/v1/email/verify", raw),
		})
	})
}

func (uc *Service) VerifyEmail(ctx context.Context, rawToken string) (*domain.User, error) {
	var user *domain.User
	err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		token, err := uc.consume(ctx, domain.UserTokenEmailVerify, rawToken)
		if err != nil {
			return err
		}
		user, err = uc.users.GetByID(ctx, token.UserID)
		if err != nil {
			return err
		}
		if domain.NormalizeEmail(user.Email) != token.Payload {
			return domain.ErrNotFound
		}
		now := time.Now()
		user.EmailVerifiedAt = &now
		return uc.users.Update(ctx, user)
	})
	return user, err
}

func (uc *Service) ForgotPassword(ctx context.Context, email string) error {
	user, err := uc.users.GetByEmail(ctx, domain.NormalizeEmail(email))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}

	return uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tokens.DeleteByUserAndKind(ctx, user.ID, domain.UserTokenPasswordReset); err != nil {
			return err
		}
		raw, err := uc.issueToken(ctx, user.ID, domain.UserTokenPasswordReset, "", passwordResetTTL)
		if err != nil {
			return err
		}
		return uc.enqueue(ctx, user.Email, domain.EmailTemplatePasswordReset, user, map[string]string{
			"Link": uc.link("/account/v1/password/reset", raw),
		})
	})
}

type ResetPasswordInput struct {
	Token    string
	Password string
	Keys     *KeyMaterial
}

type ResetPreview struct {
	Email                     string
	PublicKey                 []byte
	RecoveryWrappedPrivateKey []byte
}

func (uc *Service) TokenUsable(ctx context.Context, kind domain.UserTokenKind, rawToken string) error {
	if rawToken == "" {
		return domain.ErrNotFound
	}
	_, err := uc.tokens.Peek(ctx, kind, secret.Hash(rawToken))
	return err
}

func (uc *Service) PrepareReset(ctx context.Context, rawToken string) (*ResetPreview, error) {
	if rawToken == "" {
		return nil, domain.ErrNotFound
	}
	token, err := uc.tokens.Peek(ctx, domain.UserTokenPasswordReset, secret.Hash(rawToken))
	if err != nil {
		return nil, err
	}
	user, err := uc.users.GetByID(ctx, token.UserID)
	if err != nil {
		return nil, err
	}
	preview := &ResetPreview{Email: user.Email}
	if user.KDFVersion == domain.KDFVersionArgon2 {
		preview.PublicKey = user.PublicKey
		preview.RecoveryWrappedPrivateKey = user.RecoveryWrappedPrivateKey
	}
	return preview, nil
}

func (uc *Service) ResetPassword(ctx context.Context, input ResetPasswordInput) error {
	if len(input.Password) < domain.MinPasswordLength {
		return domain.ErrInvalidInput
	}
	if input.Keys != nil && !input.Keys.valid() {
		return domain.ErrInvalidInput
	}
	hash, err := uc.hasher.Hash(input.Password)
	if err != nil {
		return err
	}

	var userID uuid.UUID
	keyChanged := false
	err = uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		token, err := uc.consume(ctx, domain.UserTokenPasswordReset, input.Token)
		if err != nil {
			return err
		}
		user, err := uc.users.GetByID(ctx, token.UserID)
		if err != nil {
			return err
		}
		userID = user.ID
		now := time.Now()
		user.PasswordHash = hash
		if user.EmailVerifiedAt == nil {
			user.EmailVerifiedAt = &now
		}
		if input.Keys != nil {
			keyChanged = !bytes.Equal(user.PublicKey, input.Keys.PublicKey)
			input.Keys.apply(user, keyChanged)
		} else {
			keyChanged = true
			user.KDFVersion = domain.KDFVersionLegacy
			user.KDFSalt = nil
			user.PublicKey = nil
			user.EncryptedPrivateKey = nil
			user.RecoveryWrappedPrivateKey = nil
		}
		if err := uc.users.Update(ctx, user); err != nil {
			return err
		}
		if keyChanged {
			if err := uc.dropVaultKeys(ctx, user.ID); err != nil {
				return err
			}
		}
		if err := uc.refreshTokens.RevokeAllByUserID(ctx, user.ID); err != nil {
			return err
		}
		return uc.enqueue(ctx, user.Email, domain.EmailTemplatePasswordChanged, user, nil)
	})
	if err != nil {
		return err
	}
	if keyChanged {
		uc.publishKeyChanged(ctx, userID)
	}
	return nil
}

type KeyMaterial struct {
	KDFSalt                   []byte
	PublicKey                 []byte
	EncryptedPrivateKey       []byte
	RecoveryWrappedPrivateKey []byte
}

func (k *KeyMaterial) valid() bool {
	return len(k.KDFSalt) == domain.KDFSaltSize && len(k.PublicKey) == domain.X25519KeySize &&
		domain.ValidWrappedKey(k.EncryptedPrivateKey) &&
		(len(k.RecoveryWrappedPrivateKey) == 0 || domain.ValidWrappedKey(k.RecoveryWrappedPrivateKey))
}

func (k *KeyMaterial) apply(user *domain.User, keyChanged bool) {
	user.KDFVersion = domain.KDFVersionArgon2
	user.KDFSalt = k.KDFSalt
	user.PublicKey = k.PublicKey
	user.EncryptedPrivateKey = k.EncryptedPrivateKey
	if len(k.RecoveryWrappedPrivateKey) > 0 {
		user.RecoveryWrappedPrivateKey = k.RecoveryWrappedPrivateKey
	} else if keyChanged {
		user.RecoveryWrappedPrivateKey = nil
	}
}

type SetupKeysInput struct {
	UserID          uuid.UUID
	CurrentPassword string
	Password        string
	Keys            KeyMaterial
}

func (uc *Service) SetupKeys(ctx context.Context, input SetupKeysInput) (*domain.User, error) {
	if len(input.Password) < domain.MinPasswordLength || !input.Keys.valid() {
		return nil, domain.ErrInvalidInput
	}
	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if err := uc.hasher.Compare(user.PasswordHash, input.CurrentPassword); err != nil {
		return nil, domain.ErrInvalidCredentials
	}
	hash, err := uc.hasher.Hash(input.Password)
	if err != nil {
		return nil, err
	}

	keyChanged := !bytes.Equal(user.PublicKey, input.Keys.PublicKey)
	user.PasswordHash = hash
	input.Keys.apply(user, keyChanged)
	if err := uc.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.users.Update(ctx, user); err != nil {
			return err
		}
		if keyChanged {
			return uc.dropVaultKeys(ctx, user.ID)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if keyChanged {
		uc.publishKeyChanged(ctx, user.ID)
	}
	return user, nil
}

func (uc *Service) SetRecoveryKey(ctx context.Context, userID uuid.UUID, wrapped []byte) (*domain.User, error) {
	if !domain.ValidWrappedKey(wrapped) {
		return nil, domain.ErrInvalidInput
	}
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.KDFVersion != domain.KDFVersionArgon2 {
		return nil, domain.ErrKeysNotSetUp
	}
	user.RecoveryWrappedPrivateKey = wrapped
	if err := uc.users.Update(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (uc *Service) dropVaultKeys(ctx context.Context, userID uuid.UUID) error {
	if uc.vaultKeys == nil {
		return nil
	}
	return uc.vaultKeys.DeleteForUser(ctx, userID)
}

func (uc *Service) publishKeyChanged(ctx context.Context, userID uuid.UUID) {
	if uc.members == nil || uc.bus == nil {
		return
	}
	memberships, err := uc.members.ListByUserID(ctx, userID)
	if err != nil {
		return
	}
	for _, m := range memberships {
		bus.PublishVaultEvent(ctx, uc.bus, m.VaultID, bus.VaultEventMemberKeyChanged, map[string]any{
			"user_id": userID.String(),
		})
	}
}

func (uc *Service) SetAvatar(ctx context.Context, userID uuid.UUID, image io.Reader) (*domain.User, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	encoded, err := uc.avatars.Process(image)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(encoded)
	key := domain.AvatarKeyPrefix + userID.String() + "/" + hex.EncodeToString(sum[:8]) + ".jpg"
	if key == user.AvatarKey {
		return user, nil
	}

	if _, err := uc.blobs.Upload(ctx, key, bytes.NewReader(encoded), int64(len(encoded)), "image/jpeg"); err != nil {
		return nil, err
	}

	previous := user.AvatarKey
	user.AvatarKey = key
	if err := uc.users.Update(ctx, user); err != nil {
		_ = uc.blobs.Delete(context.WithoutCancel(ctx), key)
		return nil, err
	}
	uc.releaseBlob(ctx, previous)
	return user, nil
}

func (uc *Service) DeleteAvatar(ctx context.Context, userID uuid.UUID) error {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.AvatarKey == "" {
		return nil
	}
	previous := user.AvatarKey
	user.AvatarKey = ""
	if err := uc.users.Update(ctx, user); err != nil {
		return err
	}
	uc.releaseBlob(ctx, previous)
	return nil
}

func (uc *Service) OpenAvatar(ctx context.Context, userID uuid.UUID, file string) (io.ReadCloser, error) {
	if !validAvatarFile(file) {
		return nil, domain.ErrNotFound
	}
	return uc.blobs.Download(ctx, domain.AvatarKeyPrefix+userID.String()+"/"+file)
}

func validAvatarFile(file string) bool {
	name, ok := strings.CutSuffix(file, ".jpg")
	if !ok || len(name) != 16 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}

func (uc *Service) ensureEmailAvailable(ctx context.Context, email string) error {
	_, err := uc.users.GetByEmail(ctx, email)
	if err == nil {
		return domain.ErrAlreadyExists
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

func (uc *Service) issueToken(ctx context.Context, userID uuid.UUID, kind domain.UserTokenKind, payload string, ttl time.Duration) (string, error) {
	raw, hash, err := secret.New()
	if err != nil {
		return "", err
	}
	now := time.Now()
	err = uc.tokens.Create(ctx, &domain.UserToken{
		ID:        uuid.New(),
		UserID:    userID,
		Kind:      kind,
		TokenHash: hash,
		Payload:   payload,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	})
	return raw, err
}

func (uc *Service) consume(ctx context.Context, kind domain.UserTokenKind, rawToken string) (*domain.UserToken, error) {
	if rawToken == "" {
		return nil, domain.ErrNotFound
	}
	return uc.tokens.Consume(ctx, kind, secret.Hash(rawToken))
}

func (uc *Service) enqueue(ctx context.Context, to string, template string, user *domain.User, data map[string]string) error {
	if uc.outbox == nil {
		return nil
	}
	payload := map[string]string{"DisplayName": user.DisplayNameOrEmail()}
	for k, v := range data {
		payload[k] = v
	}
	return uc.outbox.Enqueue(ctx, &domain.OutboxEmail{
		To:       to,
		Template: template,
		Locale:   user.Locale,
		Data:     payload,
	})
}

func (uc *Service) link(path string, rawToken string) string {
	return uc.publicURL + path + "?token=" + url.QueryEscape(rawToken)
}

func (uc *Service) releaseBlob(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if uc.blobQueue != nil && uc.blobQueue.EnqueueDeletion(ctx, key) == nil {
		return
	}
	_ = uc.blobs.Delete(context.WithoutCancel(ctx), key)
}
