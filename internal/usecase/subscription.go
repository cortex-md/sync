package usecase

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

const defaultSubscriptionRenewalGrace = 48 * time.Hour

type SubscriptionUsecase struct {
	subs          port.SubscriptionRepository
	gateway       port.SubscriptionGateway
	users         port.UserRepository
	devops        port.DevOpsNotifier
	planProduct   string
	renewalGrace  time.Duration
	checkoutLocks sync.Map
	now           func() time.Time
}

func NewSubscriptionUsecase(subs port.SubscriptionRepository, gateway port.SubscriptionGateway, users port.UserRepository, planProduct string, renewalGrace time.Duration) *SubscriptionUsecase {
	if renewalGrace <= 0 {
		renewalGrace = defaultSubscriptionRenewalGrace
	}
	return &SubscriptionUsecase{
		subs:         subs,
		gateway:      gateway,
		users:        users,
		planProduct:  planProduct,
		renewalGrace: renewalGrace,
		now:          time.Now,
	}
}

func (uc *SubscriptionUsecase) SetDevOpsNotifier(notifier port.DevOpsNotifier) {
	uc.devops = notifier
}

type CreateCheckoutInput struct {
	UserID        uuid.UUID
	ReturnURL     string
	CompletionURL string
}

type CreateCheckoutOutput struct {
	CheckoutURL string
}

type SubscriptionWebhookInput struct {
	EventID                string
	Event                  string
	ExternalSubscriptionID string
	ExternalCheckoutID     string
	ExternalID             string
	ExternalCustomerID     string
	Status                 string
	Frequency              string
	TrialEndsAt            time.Time
	OccurredAt             time.Time
}

type SubscriptionWebhookOutput struct {
	Processed bool
	UserID    uuid.UUID
}

func (uc *SubscriptionUsecase) CheckActive(ctx context.Context, userID uuid.UUID) error {
	sub, err := uc.subs.GetByUserID(ctx, userID)
	if err != nil {
		if err == domain.ErrNotFound {
			return domain.ErrSubscriptionRequired
		}
		return err
	}
	if !sub.IsActiveAt(uc.now()) {
		return sub.AccessError()
	}
	return nil
}

func (uc *SubscriptionUsecase) CreateCheckout(ctx context.Context, input CreateCheckoutInput) (*CreateCheckoutOutput, error) {
	if strings.TrimSpace(input.ReturnURL) == "" {
		return nil, domain.ErrInvalidInput
	}

	unlock := uc.lockCheckout(input.UserID)
	defer unlock()

	user, err := uc.users.GetByID(ctx, input.UserID)
	if err != nil {
		return nil, err
	}

	now := uc.now()
	sub, created, err := uc.prepareCheckoutSubscription(ctx, input.UserID, now)
	if err != nil {
		return nil, err
	}

	if sub.ExternalCustomerID == "" {
		customerID, err := uc.gateway.CreateCustomer(ctx, user.Email)
		if err != nil {
			return nil, err
		}
		sub.ExternalCustomerID = customerID
		sub.UpdatedAt = uc.now()
		if err := uc.subs.Update(ctx, sub); err != nil {
			return nil, err
		}
	}

	checkout, err := uc.gateway.CreateSubscriptionCheckout(ctx, port.SubscriptionCheckoutInput{
		CustomerID:    sub.ExternalCustomerID,
		ExternalID:    sub.ID.String(),
		ReturnURL:     strings.TrimSpace(input.ReturnURL),
		CompletionURL: strings.TrimSpace(input.CompletionURL),
		Metadata: map[string]string{
			"user_id": input.UserID.String(),
		},
	})
	if err != nil {
		return nil, err
	}

	sub.ExternalCheckoutID = checkout.ID
	sub.UpdatedAt = uc.now()
	if err := uc.subs.Update(ctx, sub); err != nil {
		return nil, err
	}

	uc.notifyDevOps(ctx, port.DevOpsEvent{
		Type:        "subscription.checkout_created",
		UserID:      user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		OccurredAt:  now,
		Fields: map[string]string{
			"checkout_id":      checkout.ID,
			"customer_id":      sub.ExternalCustomerID,
			"plan_product_id":  sub.PlanProductID,
			"status":           string(sub.Status),
			"subscription_id":  sub.ID.String(),
			"used_existing_id": boolDevOpsField(!created),
		},
	})

	return &CreateCheckoutOutput{CheckoutURL: checkout.URL}, nil
}

func (uc *SubscriptionUsecase) prepareCheckoutSubscription(ctx context.Context, userID uuid.UUID, now time.Time) (*domain.Subscription, bool, error) {
	existing, err := uc.subs.GetByUserID(ctx, userID)
	if err != nil && err != domain.ErrNotFound {
		return nil, false, err
	}
	if existing == nil {
		sub := &domain.Subscription{
			ID:            uuid.New(),
			UserID:        userID,
			Status:        domain.SubscriptionStatusPending,
			PlanProductID: uc.planProduct,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := uc.subs.Create(ctx, sub); err != nil {
			if err != domain.ErrAlreadyExists {
				return nil, false, err
			}
			existing, err = uc.subs.GetByUserID(ctx, userID)
			if err != nil {
				return nil, false, err
			}
			return uc.checkoutSubscriptionFromExisting(existing, now), false, nil
		}
		return sub, true, nil
	}
	return uc.checkoutSubscriptionFromExisting(existing, now), false, nil
}

func (uc *SubscriptionUsecase) checkoutSubscriptionFromExisting(existing *domain.Subscription, now time.Time) *domain.Subscription {
	sub := *existing
	sub.UpdatedAt = now
	if uc.planProduct != "" {
		sub.PlanProductID = uc.planProduct
	}
	if existing.Status != domain.SubscriptionStatusActive || !existing.IsActiveAt(now) {
		sub.Status = domain.SubscriptionStatusPending
	}
	return &sub
}

func (uc *SubscriptionUsecase) lockCheckout(userID uuid.UUID) func() {
	value, _ := uc.checkoutLocks.LoadOrStore(userID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (uc *SubscriptionUsecase) HandleWebhook(ctx context.Context, input SubscriptionWebhookInput) (*SubscriptionWebhookOutput, error) {
	if strings.TrimSpace(input.EventID) == "" || strings.TrimSpace(input.Event) == "" {
		return nil, domain.ErrInvalidInput
	}

	now := uc.now()
	event := &domain.SubscriptionWebhookEvent{
		ID:        input.EventID,
		EventType: input.Event,
		CreatedAt: now,
	}
	if err := uc.subs.CreateWebhookEvent(ctx, event); err != nil {
		if err == domain.ErrAlreadyExists {
			return &SubscriptionWebhookOutput{Processed: false}, nil
		}
		return nil, err
	}

	sub, err := uc.findSubscriptionForWebhook(ctx, input)
	if err != nil {
		if err == domain.ErrNotFound {
			if err := uc.subs.MarkWebhookEventProcessed(ctx, input.EventID, now); err != nil {
				return nil, err
			}
			return &SubscriptionWebhookOutput{Processed: true}, nil
		}
		_ = uc.subs.DeleteWebhookEvent(ctx, input.EventID)
		return nil, err
	}

	if err := uc.applyWebhook(sub, input); err != nil {
		_ = uc.subs.DeleteWebhookEvent(ctx, input.EventID)
		return nil, err
	}
	sub.UpdatedAt = now

	if err := uc.subs.Update(ctx, sub); err != nil {
		_ = uc.subs.DeleteWebhookEvent(ctx, input.EventID)
		return nil, err
	}
	if err := uc.subs.MarkWebhookEventProcessed(ctx, input.EventID, now); err != nil {
		return nil, err
	}

	uc.notifySubscriptionWebhook(ctx, sub, input, now)

	return &SubscriptionWebhookOutput{Processed: true, UserID: sub.UserID}, nil
}

func (uc *SubscriptionUsecase) GetStatus(ctx context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	sub, err := uc.subs.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (uc *SubscriptionUsecase) findSubscriptionForWebhook(ctx context.Context, input SubscriptionWebhookInput) (*domain.Subscription, error) {
	if id, err := uuid.Parse(input.ExternalID); err == nil {
		if sub, err := uc.subs.GetByID(ctx, id); err == nil {
			return sub, nil
		} else if err != domain.ErrNotFound {
			return nil, err
		}
	}
	if input.ExternalSubscriptionID != "" {
		if sub, err := uc.subs.GetByExternalSubscriptionID(ctx, input.ExternalSubscriptionID); err == nil {
			return sub, nil
		} else if err != domain.ErrNotFound {
			return nil, err
		}
	}
	if input.ExternalCheckoutID != "" {
		if sub, err := uc.subs.GetByExternalCheckoutID(ctx, input.ExternalCheckoutID); err == nil {
			return sub, nil
		} else if err != domain.ErrNotFound {
			return nil, err
		}
	}
	if input.ExternalCustomerID != "" {
		return uc.subs.GetByExternalCustomerID(ctx, input.ExternalCustomerID)
	}
	return nil, domain.ErrNotFound
}

func (uc *SubscriptionUsecase) applyWebhook(sub *domain.Subscription, input SubscriptionWebhookInput) error {
	if input.ExternalSubscriptionID != "" {
		sub.ExternalSubscriptionID = input.ExternalSubscriptionID
	}
	if input.ExternalCheckoutID != "" {
		sub.ExternalCheckoutID = input.ExternalCheckoutID
	}
	if input.ExternalCustomerID != "" {
		sub.ExternalCustomerID = input.ExternalCustomerID
	}
	if uc.planProduct != "" {
		sub.PlanProductID = uc.planProduct
	}

	switch input.Event {
	case "subscription.completed", "subscription.renewed":
		return uc.activateSubscription(sub, input)
	case "subscription.trial_started":
		return uc.activateTrial(sub, input)
	case "subscription.cancelled":
		effectiveAt := webhookEffectiveTime(input, uc.now())
		sub.Status = domain.SubscriptionStatusCancelled
		sub.CurrentPeriodEnd = effectiveAt
		sub.EntitlementExpiresAt = effectiveAt
		return nil
	default:
		return nil
	}
}

func (uc *SubscriptionUsecase) activateSubscription(sub *domain.Subscription, input SubscriptionWebhookInput) error {
	effectiveAt := webhookEffectiveTime(input, uc.now())
	periodEnd, err := periodEndForFrequency(effectiveAt, input.Frequency)
	if err != nil {
		return err
	}
	sub.Status = domain.SubscriptionStatusActive
	sub.CurrentPeriodStart = effectiveAt
	sub.CurrentPeriodEnd = periodEnd
	sub.EntitlementExpiresAt = periodEnd.Add(uc.renewalGrace)
	sub.BillingCycle = strings.ToUpper(strings.TrimSpace(input.Frequency))
	return nil
}

func (uc *SubscriptionUsecase) activateTrial(sub *domain.Subscription, input SubscriptionWebhookInput) error {
	effectiveAt := webhookEffectiveTime(input, uc.now())
	expiresAt := input.TrialEndsAt
	if expiresAt.IsZero() {
		var err error
		expiresAt, err = periodEndForFrequency(effectiveAt, input.Frequency)
		if err != nil {
			return err
		}
	}
	sub.Status = domain.SubscriptionStatusActive
	sub.CurrentPeriodStart = effectiveAt
	sub.CurrentPeriodEnd = expiresAt
	sub.EntitlementExpiresAt = expiresAt.Add(uc.renewalGrace)
	sub.BillingCycle = strings.ToUpper(strings.TrimSpace(input.Frequency))
	return nil
}

func webhookEffectiveTime(input SubscriptionWebhookInput, fallback time.Time) time.Time {
	if !input.OccurredAt.IsZero() {
		return input.OccurredAt
	}
	return fallback
}

func periodEndForFrequency(start time.Time, frequency string) (time.Time, error) {
	switch strings.ToUpper(strings.TrimSpace(frequency)) {
	case "WEEKLY":
		return start.AddDate(0, 0, 7), nil
	case "MONTHLY":
		return start.AddDate(0, 1, 0), nil
	case "QUARTERLY":
		return start.AddDate(0, 3, 0), nil
	case "SEMIANNUALLY":
		return start.AddDate(0, 6, 0), nil
	case "ANNUALLY":
		return start.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, domain.ErrInvalidInput
	}
}

func (uc *SubscriptionUsecase) notifySubscriptionWebhook(ctx context.Context, sub *domain.Subscription, input SubscriptionWebhookInput, fallback time.Time) {
	if !shouldNotifySubscriptionWebhook(input.Event) {
		return
	}

	user, err := uc.users.GetByID(ctx, sub.UserID)
	if err != nil {
		user = &domain.User{ID: sub.UserID}
	}

	fields := map[string]string{
		"billing_cycle":            sub.BillingCycle,
		"checkout_id":              sub.ExternalCheckoutID,
		"customer_id":              sub.ExternalCustomerID,
		"external_subscription_id": sub.ExternalSubscriptionID,
		"frequency":                input.Frequency,
		"plan_product_id":          sub.PlanProductID,
		"status":                   string(sub.Status),
		"webhook_event_id":         input.EventID,
	}
	setDevOpsField(fields, "current_period_end", formatDevOpsTime(sub.CurrentPeriodEnd))
	setDevOpsField(fields, "entitlement_expires_at", formatDevOpsTime(sub.EntitlementExpiresAt))
	setDevOpsField(fields, "occurred_at", formatDevOpsTime(webhookEffectiveTime(input, fallback)))

	uc.notifyDevOps(ctx, port.DevOpsEvent{
		Type:        input.Event,
		UserID:      sub.UserID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		OccurredAt:  webhookEffectiveTime(input, fallback),
		Fields:      fields,
	})
}

func shouldNotifySubscriptionWebhook(event string) bool {
	switch event {
	case "subscription.completed", "subscription.renewed", "subscription.cancelled", "subscription.trial_started":
		return true
	default:
		return false
	}
}

func (uc *SubscriptionUsecase) notifyDevOps(ctx context.Context, event port.DevOpsEvent) {
	if uc.devops == nil {
		return
	}
	_ = uc.devops.Notify(ctx, event)
}

func setDevOpsField(fields map[string]string, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fields[key] = value
}

func formatDevOpsTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func boolDevOpsField(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
