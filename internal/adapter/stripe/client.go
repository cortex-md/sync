package stripeadapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	stripe "github.com/stripe/stripe-go/v86"
)

type stripeAPI interface {
	CreateCustomer(ctx context.Context, params *stripe.CustomerCreateParams) (*stripe.Customer, error)
	CreateCheckoutSession(ctx context.Context, params *stripe.CheckoutSessionCreateParams) (*stripe.CheckoutSession, error)
}

type sdkAPI struct {
	client *stripe.Client
}

func (api sdkAPI) CreateCustomer(ctx context.Context, params *stripe.CustomerCreateParams) (*stripe.Customer, error) {
	return api.client.V1Customers.Create(ctx, params)
}

func (api sdkAPI) CreateCheckoutSession(ctx context.Context, params *stripe.CheckoutSessionCreateParams) (*stripe.CheckoutSession, error) {
	return api.client.V1CheckoutSessions.Create(ctx, params)
}

type Client struct {
	priceID string
	api     stripeAPI
}

func NewClient(secretKey string, priceID string) *Client {
	return &Client{
		priceID: strings.TrimSpace(priceID),
		api:     sdkAPI{client: stripe.NewClient(strings.TrimSpace(secretKey))},
	}
}

func newClientWithAPI(priceID string, api stripeAPI) *Client {
	return &Client{priceID: strings.TrimSpace(priceID), api: api}
}

func (c *Client) CreateCustomer(ctx context.Context, input port.SubscriptionCustomerInput) (string, error) {
	email := strings.TrimSpace(input.Email)
	if email == "" {
		return "", domain.ErrInvalidInput
	}
	customer, err := c.api.CreateCustomer(ctx, &stripe.CustomerCreateParams{
		Email: stripe.String(email),
		Metadata: map[string]string{
			"cortex_user_id": input.UserID.String(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("create stripe customer: %w", err)
	}
	if customer.ID == "" {
		return "", fmt.Errorf("stripe customer response missing id")
	}
	return customer.ID, nil
}

func (c *Client) CreateSubscriptionCheckout(ctx context.Context, input port.SubscriptionCheckoutInput) (*port.SubscriptionCheckout, error) {
	if c.priceID == "" || strings.TrimSpace(input.CustomerID) == "" || strings.TrimSpace(input.ExternalID) == "" || strings.TrimSpace(input.ReturnURL) == "" {
		return nil, domain.ErrInvalidInput
	}

	successURL := strings.TrimSpace(input.CompletionURL)
	if successURL == "" {
		successURL = strings.TrimSpace(input.ReturnURL)
	}
	metadata := cloneMetadata(input.Metadata)
	metadata["subscription_id"] = strings.TrimSpace(input.ExternalID)

	session, err := c.api.CreateCheckoutSession(ctx, &stripe.CheckoutSessionCreateParams{
		CancelURL:         stripe.String(strings.TrimSpace(input.ReturnURL)),
		ClientReferenceID: stripe.String(strings.TrimSpace(input.ExternalID)),
		Customer:          stripe.String(strings.TrimSpace(input.CustomerID)),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{
				Price:    stripe.String(c.priceID),
				Quantity: stripe.Int64(1),
			},
		},
		Metadata:   metadata,
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(successURL),
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{
			Metadata: metadata,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create stripe checkout session: %w", err)
	}
	if session.ID == "" || session.URL == "" {
		return nil, fmt.Errorf("stripe checkout session response missing id or url")
	}
	return &port.SubscriptionCheckout{ID: session.ID, URL: session.URL}, nil
}

func cloneMetadata(metadata map[string]string) map[string]string {
	cloned := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" {
			continue
		}
		cloned[key] = value
	}
	return cloned
}
