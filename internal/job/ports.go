package job

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type EmailOutboxRepository interface {
	Enqueue(ctx context.Context, email *domain.OutboxEmail) error
	Claim(ctx context.Context, id int64, lease time.Duration) (*domain.OutboxEmail, error)
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]domain.OutboxEmail, error)
	MarkSent(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, lastError string, nextAttemptAt time.Time) error
	MarkDead(ctx context.Context, id int64, lastError string) error
	DeleteFinished(ctx context.Context, sentBefore, deadBefore time.Time) (int64, error)
}

type Locker interface {
	TryLock(ctx context.Context, key int64) (bool, error)
}

type Mailer interface {
	Send(ctx context.Context, email domain.OutboxEmail) error
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

type SyncEventRepository interface {
	Create(ctx context.Context, event *domain.SyncEvent) error
	GetByID(ctx context.Context, id int64) (*domain.SyncEvent, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error)
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
}

type Transactor interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type VaultInviteRepository interface {
	Create(ctx context.Context, invite *domain.VaultInvite) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.VaultInvite, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*domain.VaultInvite, error)
	ListByVaultID(ctx context.Context, vaultID uuid.UUID) ([]domain.VaultInvite, error)
	ListByEmail(ctx context.Context, email string) ([]domain.VaultInvite, error)
	Accept(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}
