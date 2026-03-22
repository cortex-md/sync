package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/auth"
	"github.com/cortexnotes/cortex-sync/internal/adapter/fake"
	"github.com/cortexnotes/cortex-sync/internal/adapter/handler"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/go-chi/chi/v5"
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

type subscriptionTestHarness struct {
	router   *chi.Mux
	tokenGen port.TokenGenerator
	subRepo  *fake.SubscriptionRepository
	gateway  *fakeGateway
}

func newSubscriptionTestHarness() *subscriptionTestHarness {
	userRepo := fake.NewUserRepository()
	deviceRepo := fake.NewDeviceRepository()
	refreshTokenRepo := fake.NewRefreshTokenRepository()
	subRepo := fake.NewSubscriptionRepository()
	hasher := auth.NewBcryptHasherWithCost(4)
	tokenGen := auth.NewJWTGenerator("test-secret", 15*time.Minute, "test")

	gateway := &fakeGateway{
		customerID:  "cust_test",
		checkoutURL: "https://pay.test.com/checkout/123",
		status:      "PAID",
	}

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	subscriptionUC := usecase.NewSubscriptionUsecase(subRepo, gateway, userRepo)

	authHandler := handler.NewAuthHandler(authUC)
	subscriptionHandler := handler.NewSubscriptionHandler(subscriptionUC)

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
	})
	r.Post("/webhooks/abacatepay", subscriptionHandler.HandleWebhook)
	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Route("/subscription/v1", func(r chi.Router) {
			r.Post("/checkout", subscriptionHandler.CreateCheckout)
			r.Get("/status", subscriptionHandler.GetStatus)
		})
		r.Group(func(r chi.Router) {
			r.Use(handler.SubscriptionMiddleware(subRepo, 1*time.Second))
			r.Get("/vaults/v1", func(w http.ResponseWriter, r *http.Request) {
				handler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			})
		})
	})

	return &subscriptionTestHarness{router: r, tokenGen: tokenGen, subRepo: subRepo, gateway: gateway}
}

func subRegisterAndLogin(t *testing.T, h *subscriptionTestHarness) (token string, userID uuid.UUID, deviceID string) {
	t.Helper()
	email := "test-" + uuid.New().String()[:8] + "@example.com"
	deviceID = uuid.New().String()

	body := `{"email":"` + email + `","password":"testpassword123","display_name":"Test"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/v1/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	body = `{"email":"` + email + `","password":"testpassword123","device_id":"` + deviceID + `","device_name":"test","device_type":"desktop"}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/auth/v1/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var loginResp struct {
		AccessToken string `json:"access_token"`
		UserID      string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &loginResp))
	uid, err := uuid.Parse(loginResp.UserID)
	require.NoError(t, err)
	return loginResp.AccessToken, uid, deviceID
}

func subDoRequest(h *subscriptionTestHarness, method, path, token, deviceID string, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if deviceID != "" {
		req.Header.Set("X-Device-ID", deviceID)
	}
	h.router.ServeHTTP(rec, req)
	return rec
}

func TestSubscriptionMiddleware_NoSubscription(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, _, deviceID := subRegisterAndLogin(t, h)

	rec := subDoRequest(h, "GET", "/vaults/v1", token, deviceID, "")
	assert.Equal(t, http.StatusPaymentRequired, rec.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "subscription_required", resp["code"])
}

func TestSubscriptionMiddleware_ActiveSubscription(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, userID, deviceID := subRegisterAndLogin(t, h)

	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_test",
		Status:             domain.SubscriptionStatusActive,
		CurrentPeriodStart: now.AddDate(0, 0, -15),
		CurrentPeriodEnd:   now.AddDate(0, 0, 15),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, h.subRepo.Create(context.Background(), sub))

	rec := subDoRequest(h, "GET", "/vaults/v1", token, deviceID, "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSubscriptionMiddleware_ExpiredSubscription(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, userID, deviceID := subRegisterAndLogin(t, h)

	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_test",
		Status:             domain.SubscriptionStatusActive,
		CurrentPeriodStart: now.AddDate(0, -2, 0),
		CurrentPeriodEnd:   now.AddDate(0, -1, 0),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, h.subRepo.Create(context.Background(), sub))

	rec := subDoRequest(h, "GET", "/vaults/v1", token, deviceID, "")
	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
}

func TestCreateCheckout_Handler(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, _, deviceID := subRegisterAndLogin(t, h)

	rec := subDoRequest(h, "POST", "/subscription/v1/checkout", token, deviceID, `{"return_url":"https://app.cortex.com"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "https://pay.test.com/checkout/123", resp["checkout_url"])
}

func TestGetStatus_Handler_NoSubscription(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, _, deviceID := subRegisterAndLogin(t, h)

	rec := subDoRequest(h, "GET", "/subscription/v1/status", token, deviceID, "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "none", resp["status"])
}

func TestGetStatus_Handler_ActiveSubscription(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, userID, deviceID := subRegisterAndLogin(t, h)

	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_test",
		Status:             domain.SubscriptionStatusActive,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   now.AddDate(0, 1, 0),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, h.subRepo.Create(context.Background(), sub))

	rec := subDoRequest(h, "GET", "/subscription/v1/status", token, deviceID, "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "active", resp["status"])
}

func TestWebhook_Handler(t *testing.T) {
	h := newSubscriptionTestHarness()
	_, userID, _ := subRegisterAndLogin(t, h)

	now := time.Now()
	externalSubID := "sub_webhook_test"
	sub := &domain.Subscription{
		ID:                     uuid.New(),
		UserID:                 userID,
		ExternalCustomerID:     "cust_test",
		ExternalSubscriptionID: externalSubID,
		Status:                 domain.SubscriptionStatusPending,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	require.NoError(t, h.subRepo.Create(context.Background(), sub))

	webhookBody := `{"data":{"subscription":{"id":"` + externalSubID + `"}},"event":"billing.paid"}`
	rec := subDoRequest(h, "POST", "/webhooks/abacatepay", "", "", webhookBody)
	assert.Equal(t, http.StatusOK, rec.Code)

	updated, err := h.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusActive, updated.Status)
}

func TestWebhook_Handler_InvalidPayload(t *testing.T) {
	h := newSubscriptionTestHarness()

	rec := subDoRequest(h, "POST", "/webhooks/abacatepay", "", "", `invalid json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhook_Handler_MissingID(t *testing.T) {
	h := newSubscriptionTestHarness()

	rec := subDoRequest(h, "POST", "/webhooks/abacatepay", "", "", `{"data":{},"event":"billing.paid"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
