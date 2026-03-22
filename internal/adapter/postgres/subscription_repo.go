package postgres

import (
	"context"

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
		`INSERT INTO subscriptions (id, user_id, external_customer_id, external_subscription_id, status, current_period_start, current_period_end, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		sub.ID, sub.UserID, sub.ExternalCustomerID, sub.ExternalSubscriptionID, string(sub.Status),
		sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.CreatedAt, sub.UpdatedAt,
	)
	return mapError(err)
}

func (r *SubscriptionRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	var s domain.Subscription
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, status, current_period_start, current_period_end, created_at, updated_at
		 FROM subscriptions WHERE user_id = $1`, userID,
	).Scan(&s.ID, &s.UserID, &s.ExternalCustomerID, &s.ExternalSubscriptionID, &status,
		&s.CurrentPeriodStart, &s.CurrentPeriodEnd, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	s.Status = domain.SubscriptionStatus(status)
	return &s, nil
}

func (r *SubscriptionRepository) Update(ctx context.Context, sub *domain.Subscription) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE subscriptions SET external_customer_id = $1, external_subscription_id = $2, status = $3, current_period_start = $4, current_period_end = $5, updated_at = $6
		 WHERE id = $7`,
		sub.ExternalCustomerID, sub.ExternalSubscriptionID, string(sub.Status),
		sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.UpdatedAt, sub.ID,
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
	var s domain.Subscription
	var status string
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, external_customer_id, external_subscription_id, status, current_period_start, current_period_end, created_at, updated_at
		 FROM subscriptions WHERE external_subscription_id = $1`, externalID,
	).Scan(&s.ID, &s.UserID, &s.ExternalCustomerID, &s.ExternalSubscriptionID, &status,
		&s.CurrentPeriodStart, &s.CurrentPeriodEnd, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	s.Status = domain.SubscriptionStatus(status)
	return &s, nil
}
