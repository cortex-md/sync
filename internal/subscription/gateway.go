package subscription

import (
	"context"

	"github.com/google/uuid"
)

type CustomerInput struct {
	UserID uuid.UUID
	Email  string
}

type CheckoutInput struct {
	CustomerID    string
	ExternalID    string
	ReturnURL     string
	CompletionURL string
	Metadata      map[string]string
}

type Checkout struct {
	ID  string
	URL string
}

type Gateway interface {
	CreateCustomer(ctx context.Context, input CustomerInput) (customerID string, err error)
	CreateSubscriptionCheckout(ctx context.Context, input CheckoutInput) (*Checkout, error)
}
