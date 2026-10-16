package account

import (
	"context"
	"io"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type AvatarProcessor interface {
	Process(reader io.Reader) ([]byte, error)
}

type BlobStorage interface {
	Check(ctx context.Context) error
	Upload(ctx context.Context, key string, reader io.Reader, size int64, contentType string) (int64, error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

type EmailOutboxRepository interface {
	Enqueue(ctx context.Context, email *domain.OutboxEmail) error
}

type PasswordHasher interface {
	HashFor(kdfVersion int, password string) (string, error)
	Compare(hash string, password string) error
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

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Count(ctx context.Context) (int64, error)
	UpdatePublicKey(ctx context.Context, id uuid.UUID, publicKey []byte) error
	Update(ctx context.Context, user *domain.User) error
	ListByIDs(ctx context.Context, ids []uuid.UUID) ([]domain.User, error)
}

type UserTokenRepository interface {
	Create(ctx context.Context, token *domain.UserToken) error
	Consume(ctx context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error)
	Peek(ctx context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error)
	DeleteByUserAndKind(ctx context.Context, userID uuid.UUID, kind domain.UserTokenKind) error
	DeleteExpired(ctx context.Context) (int64, error)
}

type BlobQueue interface {
	EnqueueDeletion(ctx context.Context, keys ...string) error
}

type MembershipRepository interface {
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.VaultMember, error)
}

type VaultKeyRepository interface {
	DeleteForUser(ctx context.Context, userID uuid.UUID) error
}

type TokenRevoker interface {
	RevokeDevice(ctx context.Context, deviceID uuid.UUID)
	RevokeUser(ctx context.Context, userID uuid.UUID)
	RevokeUserExcept(ctx context.Context, userID uuid.UUID, keepDevice uuid.UUID)
}
