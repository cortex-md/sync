package subscription

import (
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/apidocs/apidocstest"
)

func TestOpenAPIContract(t *testing.T) {
	apidocstest.AssertMatches(t, "CheckoutRequest", createCheckoutRequest{})
	apidocstest.AssertMatches(t, "CheckoutResponse", createCheckoutResponse{})
	apidocstest.AssertMatches(t, "SubscriptionStatus", StatusResponse{})
}
