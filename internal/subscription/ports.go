package subscription

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type SubscriptionRepository interface {
	Create(ctx context.Context, sub *domain.Subscription) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Subscription, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error)
	Update(ctx context.Context, sub *domain.Subscription) error
	GetByExternalSubscriptionID(ctx context.Context, externalID string) (*domain.Subscription, error)
	GetByExternalCheckoutID(ctx context.Context, externalID string) (*domain.Subscription, error)
	GetByExternalCustomerID(ctx context.Context, externalID string) (*domain.Subscription, error)
	CreateWebhookEvent(ctx context.Context, event *domain.SubscriptionWebhookEvent) error
	MarkWebhookEventProcessed(ctx context.Context, id string, processedAt time.Time) error
	DeleteWebhookEvent(ctx context.Context, id string) error
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
