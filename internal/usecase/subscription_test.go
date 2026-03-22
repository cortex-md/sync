package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeGateway struct {
	customerID  string
	checkoutURL string
	status      string
	err         error
}

func (g *fakeGateway) CreateCustomer(_ context.Context, _ string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return g.customerID, nil
}

func (g *fakeGateway) CreateSubscriptionCheckout(_ context.Context, _ string, _ string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return g.checkoutURL, nil
}

func (g *fakeGateway) GetSubscriptionStatus(_ context.Context, _ string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return g.status, nil
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
		checkoutURL: "https://pay.abacatepay.com/checkout/abc",
		status:      "PAID",
	}
	uc := usecase.NewSubscriptionUsecase(subRepo, gateway, users)
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

	output, err := s.uc.CreateCheckout(context.Background(), usecase.CreateCheckoutInput{
		UserID:    user.ID,
		ReturnURL: "https://app.cortex.com/settings",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://pay.abacatepay.com/checkout/abc", output.CheckoutURL)

	sub, err := s.subRepo.GetByUserID(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusPending, sub.Status)
	assert.Equal(t, "cust_123", sub.ExternalCustomerID)
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
	externalSubID := "sub_abc123"

	sub := &domain.Subscription{
		ID:                     uuid.New(),
		UserID:                 userID,
		ExternalCustomerID:     "cust_123",
		ExternalSubscriptionID: externalSubID,
		Status:                 domain.SubscriptionStatusPending,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	require.NoError(t, s.subRepo.Create(context.Background(), sub))

	s.gateway.status = "PAID"
	err := s.uc.HandleWebhook(context.Background(), externalSubID)
	require.NoError(t, err)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusActive, updated.Status)
	assert.True(t, updated.CurrentPeriodEnd.After(time.Now()))
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

	s.gateway.status = "CANCELLED"
	err := s.uc.HandleWebhook(context.Background(), externalSubID)
	require.NoError(t, err)

	updated, err := s.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusCancelled, updated.Status)
}

func TestHandleWebhook_EmptyID(t *testing.T) {
	s := newSubscriptionTestSetup()
	err := s.uc.HandleWebhook(context.Background(), "")
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestHandleWebhook_UnknownSubscription(t *testing.T) {
	s := newSubscriptionTestSetup()
	err := s.uc.HandleWebhook(context.Background(), "unknown_sub_id")
	assert.NoError(t, err)
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
