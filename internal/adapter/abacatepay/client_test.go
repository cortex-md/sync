package abacatepay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/adapter/abacatepay"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateCustomer_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/customers/create", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "user@example.com", body["email"])

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"id": "cust_abc"},
		})
	}))
	defer srv.Close()

	client := abacatepay.NewClient("test-key", "prod_123")
	client.SetBaseURL(srv.URL)

	customerID, err := client.CreateCustomer(context.Background(), "user@example.com")
	require.NoError(t, err)
	assert.Equal(t, "cust_abc", customerID)
}

func TestCreateSubscriptionCheckout_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/subscriptions/create", r.URL.Path)

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		items, ok := body["items"].([]any)
		require.True(t, ok)
		require.Len(t, items, 1)
		item, ok := items[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "prod_123", item["id"])
		assert.Equal(t, float64(1), item["quantity"])
		assert.Equal(t, "cust_abc", body["customerId"])
		assert.Equal(t, "sub_internal", body["externalId"])
		assert.Equal(t, "https://return.url", body["returnUrl"])
		assert.Equal(t, "https://complete.url", body["completionUrl"])
		assert.Equal(t, []any{"CARD"}, body["methods"])

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"id": "bill_123", "url": "https://checkout.url/pay"},
		})
	}))
	defer srv.Close()

	client := abacatepay.NewClient("test-key", "prod_123")
	client.SetBaseURL(srv.URL)

	checkout, err := client.CreateSubscriptionCheckout(context.Background(), port.SubscriptionCheckoutInput{
		CustomerID:    "cust_abc",
		ExternalID:    "sub_internal",
		ReturnURL:     "https://return.url",
		CompletionURL: "https://complete.url",
	})
	require.NoError(t, err)
	assert.Equal(t, "bill_123", checkout.ID)
	assert.Equal(t, "https://checkout.url/pay", checkout.URL)
}

func TestCreateCustomer_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid email"}`))
	}))
	defer srv.Close()

	client := abacatepay.NewClient("test-key", "prod_123")
	client.SetBaseURL(srv.URL)

	_, err := client.CreateCustomer(context.Background(), "bad-email")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api error")
}

func TestCreateCustomer_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer srv.Close()

	client := abacatepay.NewClient("test-key", "prod_123")
	client.SetBaseURL(srv.URL)

	_, err := client.CreateCustomer(context.Background(), "user@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}
