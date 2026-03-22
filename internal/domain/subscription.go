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
	Status                 SubscriptionStatus
	CurrentPeriodStart     time.Time
	CurrentPeriodEnd       time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (s *Subscription) IsActive() bool {
	return s.Status == SubscriptionStatusActive && time.Now().Before(s.CurrentPeriodEnd)
}
