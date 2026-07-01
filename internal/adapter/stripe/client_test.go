package stripeadapter

import (
	"context"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
)

type fakeStripeAPI struct {
	customerParams *stripe.CustomerCreateParams
	checkoutParams *stripe.CheckoutSessionCreateParams
	customer       *stripe.Customer
	session        *stripe.CheckoutSession
	err            error
}

func (api *fakeStripeAPI) CreateCustomer(_ context.Context, params *stripe.CustomerCreateParams) (*stripe.Customer, error) {
	api.customerParams = params
	if api.err != nil {
		return nil, api.err
	}
	return api.customer, nil
}

func (api *fakeStripeAPI) CreateCheckoutSession(_ context.Context, params *stripe.CheckoutSessionCreateParams) (*stripe.CheckoutSession, error) {
	api.checkoutParams = params
	if api.err != nil {
		return nil, api.err
	}
	return api.session, nil
}

func TestCreateCustomer(t *testing.T) {
	userID := uuid.New()
	api := &fakeStripeAPI{
		customer: &stripe.Customer{ID: "cus_123"},
	}
	client := newClientWithAPI("price_123", api)

	customerID, err := client.CreateCustomer(context.Background(), port.SubscriptionCustomerInput{
		UserID: userID,
		Email:  "user@example.com",
	})

	require.NoError(t, err)
	assert.Equal(t, "cus_123", customerID)
	require.NotNil(t, api.customerParams)
	assert.Equal(t, "user@example.com", *api.customerParams.Email)
	assert.Equal(t, userID.String(), api.customerParams.Metadata["cortex_user_id"])
}

func TestCreateCustomerRejectsMissingEmail(t *testing.T) {
	client := newClientWithAPI("price_123", &fakeStripeAPI{})

	_, err := client.CreateCustomer(context.Background(), port.SubscriptionCustomerInput{
		UserID: uuid.New(),
	})

	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestCreateSubscriptionCheckout(t *testing.T) {
	api := &fakeStripeAPI{
		session: &stripe.CheckoutSession{
			ID:  "cs_123",
			URL: "https://checkout.stripe.com/c/pay/cs_123",
		},
	}
	client := newClientWithAPI("price_123", api)

	checkout, err := client.CreateSubscriptionCheckout(context.Background(), port.SubscriptionCheckoutInput{
		CustomerID:    "cus_123",
		ExternalID:    "local-subscription-id",
		ReturnURL:     "https://app.cortex.com/settings",
		CompletionURL: "https://app.cortex.com/settings?checkout=success",
		Metadata: map[string]string{
			"user_id": "user-id",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "cs_123", checkout.ID)
	assert.Equal(t, "https://checkout.stripe.com/c/pay/cs_123", checkout.URL)
	require.NotNil(t, api.checkoutParams)
	assert.Equal(t, "https://app.cortex.com/settings", *api.checkoutParams.CancelURL)
	assert.Equal(t, "https://app.cortex.com/settings?checkout=success", *api.checkoutParams.SuccessURL)
	assert.Equal(t, "local-subscription-id", *api.checkoutParams.ClientReferenceID)
	assert.Equal(t, "cus_123", *api.checkoutParams.Customer)
	assert.Equal(t, string(stripe.CheckoutSessionModeSubscription), *api.checkoutParams.Mode)
	require.Len(t, api.checkoutParams.LineItems, 1)
	assert.Equal(t, "price_123", *api.checkoutParams.LineItems[0].Price)
	assert.Equal(t, int64(1), *api.checkoutParams.LineItems[0].Quantity)
	assert.Equal(t, "user-id", api.checkoutParams.Metadata["user_id"])
	assert.Equal(t, "local-subscription-id", api.checkoutParams.Metadata["subscription_id"])
	require.NotNil(t, api.checkoutParams.SubscriptionData)
	assert.Equal(t, "local-subscription-id", api.checkoutParams.SubscriptionData.Metadata["subscription_id"])
}

func TestCreateSubscriptionCheckoutUsesReturnURLAsSuccessFallback(t *testing.T) {
	api := &fakeStripeAPI{
		session: &stripe.CheckoutSession{
			ID:  "cs_123",
			URL: "https://checkout.stripe.com/c/pay/cs_123",
		},
	}
	client := newClientWithAPI("price_123", api)

	_, err := client.CreateSubscriptionCheckout(context.Background(), port.SubscriptionCheckoutInput{
		CustomerID: "cus_123",
		ExternalID: "local-subscription-id",
		ReturnURL:  "https://app.cortex.com/settings",
	})

	require.NoError(t, err)
	assert.Equal(t, "https://app.cortex.com/settings", *api.checkoutParams.SuccessURL)
}
