package domain

import (
	"time"

	"github.com/google/uuid"
)

type SubscriptionStatus string

const (
	SubscriptionStatusActive    SubscriptionStatus = "active"
	SubscriptionStatusPending   SubscriptionStatus = "pending"
	SubscriptionStatusExpired   SubscriptionStatus = "expired"
	SubscriptionStatusCancelled SubscriptionStatus = "cancelled"
)

type Subscription struct {
	ID                     uuid.UUID
	UserID                 uuid.UUID
	ExternalCustomerID     string
	ExternalSubscriptionID string
	ExternalCheckoutID     string
	Status                 SubscriptionStatus
	CurrentPeriodStart     time.Time
	CurrentPeriodEnd       time.Time
	EntitlementExpiresAt   time.Time
	BillingCycle           string
	PlanProductID          string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (s *Subscription) IsActive() bool {
	return s.IsActiveAt(time.Now())
}

func (s *Subscription) IsActiveAt(now time.Time) bool {
	if s.Status != SubscriptionStatusActive {
		return false
	}
	expiresAt := s.EntitlementExpiresAt
	if expiresAt.IsZero() {
		expiresAt = s.CurrentPeriodEnd
	}
	return !expiresAt.IsZero() && now.Before(expiresAt)
}

func (s *Subscription) AccessError() error {
	switch s.Status {
	case SubscriptionStatusPending:
		return ErrSubscriptionRequired
	default:
		return ErrSubscriptionExpired
	}
}

type SubscriptionWebhookEvent struct {
	ID          string
	EventType   string
	ProcessedAt time.Time
	CreatedAt   time.Time
}
