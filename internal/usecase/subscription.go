package usecase

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type SubscriptionUsecase struct {
	subs    port.SubscriptionRepository
	gateway port.SubscriptionGateway
	users   port.UserRepository
}

func NewSubscriptionUsecase(subs port.SubscriptionRepository, gateway port.SubscriptionGateway, users port.UserRepository) *SubscriptionUsecase {
	return &SubscriptionUsecase{subs: subs, gateway: gateway, users: users}
}

type CreateCheckoutInput struct {
	UserID    uuid.UUID
	ReturnURL string
}

type CreateCheckoutOutput struct {
	CheckoutURL string
}

func (uc *SubscriptionUsecase) CheckActive(ctx context.Context, userID uuid.UUID) error {
	sub, err := uc.subs.GetByUserID(ctx, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrSubscriptionRequired
		}
		return err
	}
	if !sub.IsActive() {
		return domain.ErrSubscriptionExpired
	}
	return nil
}

func (uc *SubscriptionUsecase) CreateCheckout(ctx context.Context, input CreateCheckoutInput) (*CreateCheckoutOutput, error) {
	if input.ReturnURL == "" {
		return nil, domain.ErrInvalidInput
	}

	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return nil, err
	}

	existing, err := uc.subs.GetByUserID(ctx, input.UserID)
	if err != nil && err != domain.ErrNotFound {
		return nil, err
	}

	var customerID string
	if existing != nil {
		customerID = existing.ExternalCustomerID
	} else {
		customerID, err = uc.gateway.CreateCustomer(ctx, user.Email)
		if err != nil {
			return nil, err
		}
	}

	checkoutURL, err := uc.gateway.CreateSubscriptionCheckout(ctx, customerID, input.ReturnURL)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if existing == nil {
		sub := &domain.Subscription{
			ID:                 uuid.New(),
			UserID:             input.UserID,
			ExternalCustomerID: customerID,
			Status:             domain.SubscriptionStatusPending,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := uc.subs.Create(ctx, sub); err != nil {
			return nil, err
		}
	} else if existing.ExternalCustomerID == "" {
		existing.ExternalCustomerID = customerID
		existing.UpdatedAt = now
		if err := uc.subs.Update(ctx, existing); err != nil {
			return nil, err
		}
	}

	return &CreateCheckoutOutput{CheckoutURL: checkoutURL}, nil
}

func (uc *SubscriptionUsecase) HandleWebhook(ctx context.Context, externalSubscriptionID string) error {
	if externalSubscriptionID == "" {
		return domain.ErrInvalidInput
	}

	status, err := uc.gateway.GetSubscriptionStatus(ctx, externalSubscriptionID)
	if err != nil {
		return err
	}

	sub, err := uc.subs.GetByExternalSubscriptionID(ctx, externalSubscriptionID)
	if err == domain.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	now := time.Now()
	sub.ExternalSubscriptionID = externalSubscriptionID
	sub.UpdatedAt = now

	switch status {
	case "PAID", "COMPLETED":
		sub.Status = domain.SubscriptionStatusActive
		sub.CurrentPeriodStart = now
		sub.CurrentPeriodEnd = now.AddDate(0, 1, 0)
	case "EXPIRED":
		sub.Status = domain.SubscriptionStatusExpired
	case "CANCELLED", "REFUNDED":
		sub.Status = domain.SubscriptionStatusCancelled
	default:
		sub.Status = domain.SubscriptionStatusPending
	}

	return uc.subs.Update(ctx, sub)
}

func (uc *SubscriptionUsecase) GetStatus(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	sub, err := uc.subs.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return sub, nil
}
