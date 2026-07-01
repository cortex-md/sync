package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeGateway struct {
	mu            sync.Mutex
	customerID    string
	checkoutURL   string
	checkoutID    string
	err           error
	customerErr   error
	checkoutErr   error
	customerCalls int
	checkoutCalls int
	customerInput port.SubscriptionCustomerInput
	input         port.SubscriptionCheckoutInput
}

func (g *fakeGateway) CreateCustomer(_ context.Context, input port.SubscriptionCustomerInput) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.customerCalls++
	g.customerInput = input
	if g.customerErr != nil {
		return "", g.customerErr
	}
	if g.err != nil {
		return "", g.err
	}
	return g.customerID, nil
}

func (g *fakeGateway) CreateSubscriptionCheckout(_ context.Context, input port.SubscriptionCheckoutInput) (*port.SubscriptionCheckout, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checkoutCalls++
	g.input = input
	if g.checkoutErr != nil {
		return nil, g.checkoutErr
	}
	if g.err != nil {
		return nil, g.err
	}
	return &port.SubscriptionCheckout{ID: g.checkoutID, URL: g.checkoutURL}, nil
}

func newTestSubscriptionUsecase(subRepo *fake.SubscriptionRepository, gateway *fakeGateway, users *fake.UserRepository) *usecase.SubscriptionUsecase {
	return usecase.NewSubscriptionUsecase(subRepo, gateway, users, "price_123", 48*time.Hour)
}

func webhookSignatureTime() time.Time {
	return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
}

func activeWebhookInput(eventID string, event string, externalSubID string, externalID string) usecase.SubscriptionWebhookInput {
	return usecase.SubscriptionWebhookInput{
		EventID:                eventID,
		Event:                  event,
		ExternalSubscriptionID: externalSubID,
		ExternalID:             externalID,
		Status:                 "ACTIVE",
		Frequency:              "MONTHLY",
		OccurredAt:             webhookSignatureTime(),
	}
}

type subscriptionTestSetup struct {
	uc      *usecase.SubscriptionUsecase
	subRepo *fake.SubscriptionRepository
	users   *fake.UserRepository
	gateway *fakeGateway
}

func newSubscriptionTestSetup() *subscriptionTestSetup {
	subRepo := fake.NewSubscriptionRepository()
	users := fake.NewUserRepository()
	gateway := &fakeGateway{
		customerID:  "cust_123",
		checkoutURL: "https://checkout.stripe.com/c/pay/cs_test_123",
		checkoutID:  "cs_test_123",
	}
	uc := newTestSubscriptionUsecase(subRepo, gateway, users)
	return &subscriptionTestSetup{uc: uc, subRepo: subRepo, users: users, gateway: gateway}
}

func createTestUser(t *testing.T, users *fake.UserRepository) *domain.User {
	t.Helper()
	user := &domain.User{
		ID:           uuid.New(),
		Email:        fmt.Sprintf("test-%s@example.com", uuid.New().String()[:8]),
		PasswordHash: "hash",
		DisplayName:  "Test User",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, users.Create(context.Background(), user))
	return user
}

func TestCheckActive_NoSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	err := s.uc.CheckActive(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrSubscriptionRequired)
}

func TestCheckActive_ActiveSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusActive,
		CurrentPeriodStart: now.AddDate(0, 0, -15),
		CurrentPeriodEnd:   now.AddDate(0, 0, 15),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	err := s.uc.CheckActive(context.Background(), userID)
	assert.NoError(t, err)
}

func TestCheckActive_ExpiredSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusActive,
		CurrentPeriodStart: now.AddDate(0, -2, 0),
		CurrentPeriodEnd:   now.AddDate(0, -1, 0),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	err := s.uc.CheckActive(context.Background(), userID)
	assert.ErrorIs(t, err, domain.ErrSubscriptionExpired)
}

func TestCheckActive_CancelledSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusCancelled,
		CurrentPeriodStart: now.AddDate(0, 0, -15),
		CurrentPeriodEnd:   now.AddDate(0, 0, 15),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	err := s.uc.CheckActive(context.Background(), userID)
	assert.ErrorIs(t, err, domain.ErrSubscriptionExpired)
}

func TestCreateCheckout_Success(t *testing.T) {
	s := newSubscriptionTestSetup()
	user := createTestUser(t, s.users)
	notifier := &recordingDevOpsNotifier{}
	s.uc.SetDevOpsNotifier(notifier)

	output, err := s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
		UserID:    user.ID,
		ReturnURL: "https://app.cortex.com/settings",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://checkout.stripe.com/c/pay/cs_test_123", output.CheckoutURL)

	sub, err := s.subRepo.GetByUserID(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusPending, sub.Status)
	assert.Equal(t, "cust_123", sub.ExternalCustomerID)
	assert.Equal(t, "cs_test_123", sub.ExternalCheckoutID)
	assert.Equal(t, "price_123", sub.PlanProductID)
	assert.Equal(t, user.ID, s.gateway.customerInput.UserID)
	assert.Equal(t, user.Email, s.gateway.customerInput.Email)
	assert.Equal(t, sub.ID.String(), s.gateway.input.ExternalID)
	assert.Equal(t, sub.ID.String(), s.gateway.input.Metadata["subscription_id"])
	assert.Equal(t, user.ID.String(), s.gateway.input.Metadata["user_id"])
	require.Len(t, notifier.events, 1)
	assert.Equal(t, "subscription.checkout_created", notifier.events[0].Type)
	assert.Equal(t, user.ID, notifier.events[0].UserID)
	assert.Equal(t, "cs_test_123", notifier.events[0].Fields["checkout_id"])
	assert.Equal(t, "price_123", notifier.events[0].Fields["plan_product_id"])
}

func TestCreateCheckout_MissingReturnURL(t *testing.T) {
	s := newSubscriptionTestSetup()
	user := createTestUser(t, s.users)

	_, err := s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
		UserID:    user.ID,
		ReturnURL: "",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestCreateCheckout_ReusesCustomerAfterCheckoutFailure(t *testing.T) {
	s := newSubscriptionTestSetup()
	user := createTestUser(t, s.users)
	s.gateway.checkoutErr = errors.New("subscription checkout only accepts products with cycle defined")

	_, err := s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
		UserID:    user.ID,
		ReturnURL: "https://app.cortex.com/settings",
	})
	require.Error(t, err)

	_, err = s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
		UserID:    user.ID,
		ReturnURL: "https://app.cortex.com/settings",
	})
	require.Error(t, err)

	sub, err := s.subRepo.GetByUserID(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, "cust_123", sub.ExternalCustomerID)
	assert.Empty(t, sub.ExternalCheckoutID)
	assert.Equal(t, domain.SubscriptionStatusPending, sub.Status)
	assert.Equal(t, 1, s.gateway.customerCalls)
	assert.Equal(t, 2, s.gateway.checkoutCalls)
}

func TestCreateCheckout_SerializesConcurrentRequestsForUser(t *testing.T) {
	s := newSubscriptionTestSetup()
	user := createTestUser(t, s.users)
	s.gateway.checkoutErr = errors.New("subscription checkout only accepts products with cycle defined")

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
				UserID:    user.ID,
				ReturnURL: "https://app.cortex.com/settings",
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.Error(t, err)
	}

	sub, err := s.subRepo.GetByUserID(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, "cust_123", sub.ExternalCustomerID)
	assert.Empty(t, sub.ExternalCheckoutID)
	assert.Equal(t, 1, s.gateway.customerCalls)
	assert.Equal(t, 5, s.gateway.checkoutCalls)
}

func TestCreateCheckout_ExistingSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	user := createTestUser(t, s.users)
	now := time.Now()

	existing := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             user.ID,
		ExternalCustomerID: "cust_existing",
		Status:             domain.SubscriptionStatusExpired,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), existing))

	output, err := s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
		UserID:    user.ID,
		ReturnURL: "https://app.cortex.com/settings",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, output.CheckoutURL)
}

func TestHandleWebhook_ActivatesSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	subID := uuid.New()
	externalSubID := "sub_abc123"

	sub := &domain.Subscription{
		ID:                 subID,
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	output, err := s.uc.HandleWebhook(context.Background(), activeWebhookInput("evt_1", "subscription.completed", externalSubID, subID.String()))
	require.NoError(t, err)
	require.True(t, output.Processed)
	assert.Equal(t, userID, output.UserID)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusActive, updated.Status)
	assert.Equal(t, externalSubID, updated.ExternalSubscriptionID)
	assert.Equal(t, "MONTHLY", updated.BillingCycle)
	assert.Equal(t, webhookSignatureTime().AddDate(0, 1, 0), updated.CurrentPeriodEnd)
	assert.Equal(t, webhookSignatureTime().AddDate(0, 1, 0).Add(48*time.Hour), updated.EntitlementExpiresAt)
}

func TestHandleWebhook_ActivatesSubscriptionWithExplicitStripePeriod(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	subID := uuid.New()
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	sub := &domain.Subscription{
		ID:                 subID,
		UserID:             userID,
		ExternalCustomerID: "cus_123",
		Status:             domain.SubscriptionStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	output, err := s.uc.HandleWebhook(context.Background(), usecase.SubscriptionWebhookInput{
		EventID:                "evt_stripe_period",
		Event:                  "subscription.completed",
		ExternalSubscriptionID: "sub_stripe",
		ExternalID:             subID.String(),
		ExternalCustomerID:     "cus_123",
		Status:                 "active",
		Frequency:              "MONTHLY",
		CurrentPeriodStart:     periodStart,
		CurrentPeriodEnd:       periodEnd,
		PlanPriceID:            "price_123",
		OccurredAt:             webhookSignatureTime(),
	})
	require.NoError(t, err)
	require.True(t, output.Processed)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusActive, updated.Status)
	assert.Equal(t, periodStart, updated.CurrentPeriodStart)
	assert.Equal(t, periodEnd, updated.CurrentPeriodEnd)
	assert.Equal(t, periodEnd.Add(48*time.Hour), updated.EntitlementExpiresAt)
	assert.Equal(t, "price_123", updated.PlanProductID)
}

func TestHandleWebhook_IncompleteSubscriptionDoesNotGrantAccess(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	subID := uuid.New()

	sub := &domain.Subscription{
		ID:                 subID,
		UserID:             userID,
		ExternalCustomerID: "cus_123",
		Status:             domain.SubscriptionStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	output, err := s.uc.HandleWebhook(context.Background(), usecase.SubscriptionWebhookInput{
		EventID:                "evt_incomplete",
		Event:                  "subscription.pending",
		ExternalSubscriptionID: "sub_incomplete",
		ExternalID:             subID.String(),
		ExternalCustomerID:     "cus_123",
		Status:                 "incomplete",
		OccurredAt:             webhookSignatureTime(),
	})
	require.NoError(t, err)
	require.True(t, output.Processed)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusPending, updated.Status)
	assert.False(t, updated.IsActiveAt(webhookSignatureTime()))
}

func TestHandleWebhook_CancelledSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()
	externalSubID := "sub_cancel"

	sub := &domain.Subscription{
		ID:                     uuid.New(),
		UserID:                 userID,
		ExternalCustomerID:     "cust_123",
		ExternalSubscriptionID: externalSubID,
		Status:                 domain.SubscriptionStatusActive,
		CurrentPeriodStart:     now.AddDate(0, 0, -15),
		CurrentPeriodEnd:       now.AddDate(0, 0, 15),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	output, err := s.uc.HandleWebhook(context.Background(), usecase.SubscriptionWebhookInput{
		EventID:                "evt_cancel",
		Event:                  "subscription.cancelled",
		ExternalSubscriptionID: externalSubID,
		Status:                 "CANCELLED",
		OccurredAt:             webhookSignatureTime(),
	})
	require.NoError(t, err)
	require.True(t, output.Processed)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusCancelled, updated.Status)
	assert.Equal(t, webhookSignatureTime(), updated.EntitlementExpiresAt)
}

func TestHandleWebhook_EmptyID(t *testing.T) {
	s := newSubscriptionTestSetup()
	_, err := s.uc.HandleWebhook(context.Background(), usecase.SubscriptionWebhookInput{})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestHandleWebhook_UnknownSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	output, err := s.uc.HandleWebhook(context.Background(), activeWebhookInput("evt_unknown", "subscription.completed", "unknown_sub_id", ""))
	assert.NoError(t, err)
	assert.True(t, output.Processed)
}

func TestHandleWebhook_DuplicateEventIsIgnored(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	subID := uuid.New()
	notifier := &recordingDevOpsNotifier{}
	s.uc.SetDevOpsNotifier(notifier)
	now := time.Now()
	sub := &domain.Subscription{
		ID:                 subID,
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	input := activeWebhookInput("evt_duplicate", "subscription.completed", "sub_duplicate", subID.String())
	first, err := s.uc.HandleWebhook(context.Background(), input)
	require.NoError(t, err)
	require.True(t, first.Processed)

	second, err := s.uc.HandleWebhook(context.Background(), input)
	require.NoError(t, err)
	require.False(t, second.Processed)
	require.Len(t, notifier.events, 1)
	assert.Equal(t, "subscription.completed", notifier.events[0].Type)
}

func TestHandleWebhook_TrialStartedUsesTrialEnd(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	subID := uuid.New()
	now := time.Now()
	trialEndsAt := webhookSignatureTime().AddDate(0, 0, 7)
	sub := &domain.Subscription{
		ID:                 subID,
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	output, err := s.uc.HandleWebhook(context.Background(), usecase.SubscriptionWebhookInput{
		EventID:                "evt_trial",
		Event:                  "subscription.trial_started",
		ExternalSubscriptionID: "sub_trial",
		ExternalID:             subID.String(),
		Status:                 "ACTIVE",
		Frequency:              "MONTHLY",
		TrialEndsAt:            trialEndsAt,
		OccurredAt:             webhookSignatureTime(),
	})
	require.NoError(t, err)
	require.True(t, output.Processed)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, trialEndsAt, updated.CurrentPeriodEnd)
	assert.Equal(t, trialEndsAt.Add(48*time.Hour), updated.EntitlementExpiresAt)
}

func TestGetStatus_Exists(t *testing.T) {
	s := newSubscriptionTestSetup()
	userID := uuid.New()
	now := time.Now()

	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_123",
		Status:             domain.SubscriptionStatusActive,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   now.AddDate(0, 1, 0),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	result, err := s.uc.GetStatus(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusActive, result.Status)
}

func TestGetStatus_NotFound(t *testing.T) {
	s := newSubscriptionTestSetup()
	_, err := s.uc.GetStatus(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
