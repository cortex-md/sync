package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SubscriptionRepository struct {
	pool *pgxpool.Pool
}

func NewSubscriptionRepository(pool *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{pool: pool}
}

func (r *SubscriptionRepository) Create(ctx context.Context, sub *domain.Subscription) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO subscriptions (
			id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
			status, current_period_start, current_period_end, entitlement_expires_at,
			billing_cycle, plan_product_id, created_at, updated_at
		)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		sub.ID, sub.UserID, sub.ExternalCustomerID, sub.ExternalSubscriptionID, sub.ExternalCheckoutID,
		string(sub.Status), nullTime(sub.CurrentPeriodStart), nullTime(sub.CurrentPeriodEnd),
		nullTime(sub.EntitlementExpiresAt), sub.BillingCycle, sub.PlanProductID,
		sub.CreatedAt, sub.UpdatedAt,
	)
	return mapError(err)
}

func (r *SubscriptionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Subscription, error) {
	return r.getOne(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
			status, current_period_start, current_period_end, entitlement_expires_at,
			billing_cycle, plan_product_id, created_at, updated_at
		 FROM subscriptions WHERE id = $1`, id,
	)
}

func (r *SubscriptionRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	return r.getOne(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
			status, current_period_start, current_period_end, entitlement_expires_at,
			billing_cycle, plan_product_id, created_at, updated_at
		 FROM subscriptions WHERE user_id = $1`, userID,
	)
}

func (r *SubscriptionRepository) Update(ctx context.Context, sub *domain.Subscription) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE subscriptions SET
			external_customer_id = $1,
			external_subscription_id = $2,
			external_checkout_id = $3,
			status = $4,
			current_period_start = $5,
			current_period_end = $6,
			entitlement_expires_at = $7,
			billing_cycle = $8,
			plan_product_id = $9,
			updated_at = $10
		 WHERE id = $11`,
		sub.ExternalCustomerID, sub.ExternalSubscriptionID, sub.ExternalCheckoutID, string(sub.Status),
		nullTime(sub.CurrentPeriodStart), nullTime(sub.CurrentPeriodEnd),
		nullTime(sub.EntitlementExpiresAt), sub.BillingCycle, sub.PlanProductID,
		sub.UpdatedAt, sub.ID,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SubscriptionRepository) GetByExternalSubscriptionID(ctx context.Context, externalID string) (*domain.Subscription, error) {
	return r.getOne(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
			status, current_period_start, current_period_end, entitlement_expires_at,
			billing_cycle, plan_product_id, created_at, updated_at
		 FROM subscriptions WHERE external_subscription_id = $1`, externalID,
	)
}

func (r *SubscriptionRepository) GetByExternalCheckoutID(ctx context.Context, externalID string) (*domain.Subscription, error) {
	return r.getOne(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
			status, current_period_start, current_period_end, entitlement_expires_at,
			billing_cycle, plan_product_id, created_at, updated_at
		 FROM subscriptions WHERE external_checkout_id = $1`, externalID,
	)
}

func (r *SubscriptionRepository) GetByExternalCustomerID(ctx context.Context, externalID string) (*domain.Subscription, error) {
	return r.getOne(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
			status, current_period_start, current_period_end, entitlement_expires_at,
			billing_cycle, plan_product_id, created_at, updated_at
		 FROM subscriptions WHERE external_customer_id = $1`, externalID,
	)
}

func (r *SubscriptionRepository) CreateWebhookEvent(ctx context.Context, event *domain.SubscriptionWebhookEvent) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO subscription_webhook_events (id, event_type, processed_at, created_at)
		 VALUES ($1, $2, $3, $4)`,
		event.ID, event.EventType, nullTime(event.ProcessedAt), event.CreatedAt,
	)
	return mapError(err)
}

func (r *SubscriptionRepository) MarkWebhookEventProcessed(ctx context.Context, id string, processedAt time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE subscription_webhook_events SET processed_at = $1 WHERE id = $2`,
		processedAt, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SubscriptionRepository) DeleteWebhookEvent(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM subscription_webhook_events WHERE id = $1`, id)
	return mapError(err)
}

func (r *SubscriptionRepository) getOne(ctx context.Context, query string, args ...any) (*domain.Subscription, error) {
	var s domain.Subscription
	var status string
	var currentPeriodStart, currentPeriodEnd, entitlementExpiresAt sql.NullTime
	err := r.pool.QueryRow(ctx,
		query, args...,
	).Scan(&s.ID, &s.UserID, &s.ExternalCustomerID, &s.ExternalSubscriptionID, &s.ExternalCheckoutID,
		&status, &currentPeriodStart, &currentPeriodEnd, &entitlementExpiresAt,
		&s.BillingCycle, &s.PlanProductID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	s.Status = domain.SubscriptionStatus(status)
	if currentPeriodStart.Valid {
		s.CurrentPeriodStart = currentPeriodStart.Time
	}
	if currentPeriodEnd.Valid {
		s.CurrentPeriodEnd = currentPeriodEnd.Time
	}
	if entitlementExpiresAt.Valid {
		s.EntitlementExpiresAt = entitlementExpiresAt.Time
	}
	return &s, nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
