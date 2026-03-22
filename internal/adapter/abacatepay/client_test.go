package abacatepay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cortexnotes/cortex-sync/internal/adapter/abacatepay"
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
		assert.Equal(t, "prod_123", body["productId"])
		assert.Equal(t, "cust_abc", body["customer"])
		assert.Equal(t, "https://return.url", body["returnUrl"])

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"url": "https://checkout.url/pay"},
		})
	}))
	defer srv.Close()

	client := abacatepay.NewClient("test-key", "prod_123")
	client.SetBaseURL(srv.URL)

	url, err := client.CreateSubscriptionCheckout(context.Background(), "cust_abc", "https://return.url")
	require.NoError(t, err)
	assert.Equal(t, "https://checkout.url/pay", url)
}

func TestGetSubscriptionStatus_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/subscriptions/get")
		assert.Equal(t, "sub_123", r.URL.Query().Get("id"))

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]string{"id": "sub_123", "status": "PAID"},
		})
	}))
	defer srv.Close()

	client := abacatepay.NewClient("test-key", "prod_123")
	client.SetBaseURL(srv.URL)

	status, err := client.GetSubscriptionStatus(context.Background(), "sub_123")
	require.NoError(t, err)
	assert.Equal(t, "PAID", status)
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
