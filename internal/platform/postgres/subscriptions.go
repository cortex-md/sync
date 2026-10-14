package postgres

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SubscriptionRepository struct{ store }

func NewSubscriptionRepository(pool *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{store{pool}}
}

func toSubscription(s db.Subscription) *domain.Subscription {
	return &domain.Subscription{
		ID:                     s.ID,
		UserID:                 s.UserID,
		ExternalCustomerID:     s.ExternalCustomerID,
		ExternalSubscriptionID: s.ExternalSubscriptionID,
		ExternalCheckoutID:     s.ExternalCheckoutID,
		Status:                 domain.SubscriptionStatus(s.Status),
		CurrentPeriodStart:     timeOrZero(s.CurrentPeriodStart),
		CurrentPeriodEnd:       timeOrZero(s.CurrentPeriodEnd),
		EntitlementExpiresAt:   timeOrZero(s.EntitlementExpiresAt),
		BillingCycle:           s.BillingCycle,
		PlanProductID:          s.PlanProductID,
		CreatedAt:              s.CreatedAt,
		UpdatedAt:              s.UpdatedAt,
	}
}

func subscriptionResult(s db.Subscription, err error) (*domain.Subscription, error) {
	if err != nil {
		return nil, mapError(err)
	}
	return toSubscription(s), nil
}

func (r *SubscriptionRepository) Create(ctx context.Context, s *domain.Subscription) error {
	return mapError(r.q(ctx).CreateSubscription(ctx, db.CreateSubscriptionParams{
		ID:                     s.ID,
		UserID:                 s.UserID,
		ExternalCustomerID:     s.ExternalCustomerID,
		ExternalSubscriptionID: s.ExternalSubscriptionID,
		ExternalCheckoutID:     s.ExternalCheckoutID,
		Status:                 string(s.Status),
		CurrentPeriodStart:     optionalTime(s.CurrentPeriodStart),
		CurrentPeriodEnd:       optionalTime(s.CurrentPeriodEnd),
		EntitlementExpiresAt:   optionalTime(s.EntitlementExpiresAt),
		BillingCycle:           s.BillingCycle,
		PlanProductID:          s.PlanProductID,
		CreatedAt:              s.CreatedAt,
		UpdatedAt:              s.UpdatedAt,
	}))
}

func (r *SubscriptionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Subscription, error) {
	return subscriptionResult(r.q(ctx).GetSubscription(ctx, id))
}

func (r *SubscriptionRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	return subscriptionResult(r.q(ctx).GetSubscriptionByUser(ctx, userID))
}

func (r *SubscriptionRepository) GetByExternalSubscriptionID(ctx context.Context, externalID string) (*domain.Subscription, error) {
	return subscriptionResult(r.q(ctx).GetSubscriptionByExternalSubscription(ctx, externalID))
}

func (r *SubscriptionRepository) GetByExternalCheckoutID(ctx context.Context, externalID string) (*domain.Subscription, error) {
	return subscriptionResult(r.q(ctx).GetSubscriptionByExternalCheckout(ctx, externalID))
}

func (r *SubscriptionRepository) GetByExternalCustomerID(ctx context.Context, externalID string) (*domain.Subscription, error) {
	return subscriptionResult(r.q(ctx).GetSubscriptionByExternalCustomer(ctx, externalID))
}

func (r *SubscriptionRepository) Update(ctx context.Context, s *domain.Subscription) error {
	return expectRows(r.q(ctx).UpdateSubscription(ctx, db.UpdateSubscriptionParams{
		ID:                     s.ID,
		ExternalCustomerID:     s.ExternalCustomerID,
		ExternalSubscriptionID: s.ExternalSubscriptionID,
		ExternalCheckoutID:     s.ExternalCheckoutID,
		Status:                 string(s.Status),
		CurrentPeriodStart:     optionalTime(s.CurrentPeriodStart),
		CurrentPeriodEnd:       optionalTime(s.CurrentPeriodEnd),
		EntitlementExpiresAt:   optionalTime(s.EntitlementExpiresAt),
		BillingCycle:           s.BillingCycle,
		PlanProductID:          s.PlanProductID,
		UpdatedAt:              s.UpdatedAt,
	}))
}

func (r *SubscriptionRepository) CreateWebhookEvent(ctx context.Context, e *domain.SubscriptionWebhookEvent) error {
	return mapError(r.q(ctx).CreateWebhookEvent(ctx, db.CreateWebhookEventParams{
		ID: e.ID, EventType: e.EventType, ProcessedAt: optionalTime(e.ProcessedAt), CreatedAt: e.CreatedAt,
	}))
}

func (r *SubscriptionRepository) MarkWebhookEventProcessed(ctx context.Context, id string, processedAt time.Time) error {
	return expectRows(r.q(ctx).MarkWebhookEventProcessed(ctx, db.MarkWebhookEventProcessedParams{ID: id, ProcessedAt: &processedAt}))
}

func (r *SubscriptionRepository) DeleteWebhookEvent(ctx context.Context, id string) error {
	return mapError(r.q(ctx).DeleteWebhookEvent(ctx, id))
}
