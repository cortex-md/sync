package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	stripe "github.com/stripe/stripe-go/v86"
)

type fakeGateway struct {
	customerID    string
	checkoutURL   string
	checkoutID    string
	err           error
	customerInput port.SubscriptionCustomerInput
	input         port.SubscriptionCheckoutInput
}

func (g *fakeGateway) CreateCustomer(_ context.Context, input port.SubscriptionCustomerInput) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	g.customerInput = input
	return g.customerID, nil
}

func (g *fakeGateway) CreateSubscriptionCheckout(_ context.Context, input port.SubscriptionCheckoutInput) (*port.SubscriptionCheckout, error) {
	if g.err != nil {
		return nil, g.err
	}
	g.input = input
	return &port.SubscriptionCheckout{ID: g.checkoutID, URL: g.checkoutURL}, nil
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
		checkoutID:  "bill_test",
	}

	authUC := usecase.NewAuthUsecase(userRepo, deviceRepo, refreshTokenRepo, hasher, tokenGen, 90*24*time.Hour)
	entitlementChecker := handler.NewEntitlementChecker(subRepo, time.Second)
	subscriptionUC := usecase.NewSubscriptionUsecase(subRepo, gateway, userRepo, "price_test", 48*time.Hour)

	authHandler := handler.NewAuthHandler(authUC)
	subscriptionHandler := handler.NewSubscriptionHandler(
		subscriptionUC,
		handler.WebhookSecurity{StripeWebhookSecret: "whsec_test"},
		entitlementChecker,
	)

	r := chi.NewRouter()
	r.Route("/auth/v1", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
	})
	r.Post("/webhooks/stripe", subscriptionHandler.HandleWebhook)
	r.Group(func(r chi.Router) {
		r.Use(handler.AuthMiddleware(tokenGen))
		r.Use(handler.DeviceMiddleware)
		r.Route("/subscription/v1", func(r chi.Router) {
			r.Post("/checkout", subscriptionHandler.CreateCheckout)
			r.Get("/status", subscriptionHandler.GetStatus)
		})
		r.Get("/vaults/v1", func(w http.ResponseWriter, r *http.Request) {
			handler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		r.Group(func(r chi.Router) {
			r.Use(entitlementChecker.Middleware)
			r.Get("/sync/v1/vaults/{vaultID}/files/list", func(w http.ResponseWriter, r *http.Request) {
				handler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			})
		})
	})

	return &subscriptionTestHarness{router: r, tokenGen: tokenGen, subRepo: subRepo, gateway: gateway}
}

func signedWebhookRequestBody(body string) (path string, signature string) {
	payload := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
		Payload: []byte(body),
		Secret:  "whsec_test",
	})
	return "/webhooks/stripe", payload.Header
}

func subDoWebhookRequest(h *subscriptionTestHarness, body string) *httptest.ResponseRecorder {
	path, signature := signedWebhookRequestBody(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", signature)
	h.router.ServeHTTP(rec, req)
	return rec
}

func stripeEventBody(eventID string, eventType string, object string) string {
	return fmt.Sprintf(
		`{"id":%q,"object":"event","api_version":%q,"created":1768478400,"type":%q,"data":{"object":%s}}`,
		eventID,
		stripe.APIVersion,
		eventType,
		object,
	)
}

func stripeSubscriptionObject(subID string, customerID string, localSubscriptionID string, status string, priceID string, periodStart int64, periodEnd int64) string {
	return fmt.Sprintf(
		`{"id":%q,"object":"subscription","customer":%q,"status":%q,"metadata":{"subscription_id":%q},"items":{"object":"list","data":[{"id":"si_test","object":"subscription_item","current_period_start":%d,"current_period_end":%d,"price":{"id":%q,"object":"price","recurring":{"interval":"month","interval_count":1}}}]}}`,
		subID,
		customerID,
		status,
		localSubscriptionID,
		periodStart,
		periodEnd,
		priceID,
	)
}

func stripeCheckoutSessionObject(checkoutID string, customerID string, externalSubscriptionID string, localSubscriptionID string) string {
	return fmt.Sprintf(
		`{"id":%q,"object":"checkout.session","client_reference_id":%q,"customer":%q,"mode":"subscription","subscription":%q,"metadata":{"subscription_id":%q}}`,
		checkoutID,
		localSubscriptionID,
		customerID,
		externalSubscriptionID,
		localSubscriptionID,
	)
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

	rec := subDoRequest(h, "GET", "/sync/v1/vaults/"+uuid.NewString()+"/files/list", token, deviceID, "")
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

	rec := subDoRequest(h, "GET", "/sync/v1/vaults/"+uuid.NewString()+"/files/list", token, deviceID, "")
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

	rec := subDoRequest(h, "GET", "/sync/v1/vaults/"+uuid.NewString()+"/files/list", token, deviceID, "")
	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
}

func TestSubscriptionMiddleware_AllowsVaultListWithoutSubscription(t *testing.T) {
	h := newSubscriptionTestHarness()
	token, _, deviceID := subRegisterAndLogin(t, h)

	rec := subDoRequest(h, "GET", "/vaults/v1", token, deviceID, "")
	assert.Equal(t, http.StatusOK, rec.Code)
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

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "none", resp["status"])
	assert.Equal(t, false, resp["entitled"])
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

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "active", resp["status"])
	assert.Equal(t, true, resp["entitled"])
}

func TestWebhook_Handler(t *testing.T) {
	h := newSubscriptionTestHarness()
	_, userID, _ := subRegisterAndLogin(t, h)

	now := time.Now()
	externalSubID := "sub_webhook_test"
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
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

	webhookBody := stripeEventBody(
		"evt_webhook",
		"customer.subscription.updated",
		stripeSubscriptionObject(externalSubID, "cust_test", sub.ID.String(), "active", "price_test", periodStart.Unix(), periodEnd.Unix()),
	)
	rec := subDoWebhookRequest(h, webhookBody)
	assert.Equal(t, http.StatusOK, rec.Code)

	updated, err := h.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusActive, updated.Status)
	assert.Equal(t, periodStart, updated.CurrentPeriodStart)
	assert.Equal(t, periodEnd, updated.CurrentPeriodEnd)
	assert.Equal(t, "price_test", updated.PlanProductID)
}

func TestWebhook_Handler_CheckoutSessionCompletedLinksStripeIDs(t *testing.T) {
	h := newSubscriptionTestHarness()
	_, userID, _ := subRegisterAndLogin(t, h)

	now := time.Now()
	sub := &domain.Subscription{
		ID:                 uuid.New(),
		UserID:             userID,
		ExternalCustomerID: "cust_test",
		Status:             domain.SubscriptionStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	require.NoError(t, h.subRepo.Create(context.Background(), sub))

	webhookBody := stripeEventBody(
		"evt_checkout_completed",
		"checkout.session.completed",
		stripeCheckoutSessionObject("cs_test", "cust_test", "sub_test", sub.ID.String()),
	)
	rec := subDoWebhookRequest(h, webhookBody)
	assert.Equal(t, http.StatusOK, rec.Code)

	updated, err := h.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusPending, updated.Status)
	assert.Equal(t, "cs_test", updated.ExternalCheckoutID)
	assert.Equal(t, "sub_test", updated.ExternalSubscriptionID)
}

func TestWebhook_Handler_CancelledSubscriptionRemovesEntitlement(t *testing.T) {
	h := newSubscriptionTestHarness()
	_, userID, _ := subRegisterAndLogin(t, h)

	now := time.Now()
	externalSubID := "sub_cancelled_test"
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	sub := &domain.Subscription{
		ID:                     uuid.New(),
		UserID:                 userID,
		ExternalCustomerID:     "cust_test",
		ExternalSubscriptionID: externalSubID,
		Status:                 domain.SubscriptionStatusActive,
		CurrentPeriodStart:     periodStart,
		CurrentPeriodEnd:       periodEnd.AddDate(0, 1, 0),
		EntitlementExpiresAt:   periodEnd.AddDate(0, 1, 0).Add(48 * time.Hour),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	require.NoError(t, h.subRepo.Create(context.Background(), sub))

	webhookBody := stripeEventBody(
		"evt_cancelled",
		"customer.subscription.deleted",
		stripeSubscriptionObject(externalSubID, "cust_test", sub.ID.String(), "canceled", "price_test", periodStart.Unix(), periodEnd.Unix()),
	)
	rec := subDoWebhookRequest(h, webhookBody)
	assert.Equal(t, http.StatusOK, rec.Code)

	updated, err := h.subRepo.GetByUserID(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusCancelled, updated.Status)
	assert.Equal(t, periodEnd, updated.CurrentPeriodEnd)
	assert.Equal(t, periodEnd, updated.EntitlementExpiresAt)
}

func TestWebhook_Handler_InvalidPayload(t *testing.T) {
	h := newSubscriptionTestHarness()

	rec := subDoWebhookRequest(h, `invalid json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhook_Handler_MissingID(t *testing.T) {
	h := newSubscriptionTestHarness()

	rec := subDoWebhookRequest(h, `{"object":"event","data":{},"type":"customer.subscription.updated"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhook_Handler_MissingSubscriptionIdentifiers(t *testing.T) {
	h := newSubscriptionTestHarness()

	rec := subDoWebhookRequest(h, stripeEventBody("evt_missing_ids", "customer.subscription.updated", `{"object":"subscription","status":"active"}`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWebhook_Handler_MissingSignature(t *testing.T) {
	h := newSubscriptionTestHarness()
	body := stripeEventBody("evt_missing_signature", "customer.subscription.updated", `{"id":"sub_test","object":"subscription"}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/webhooks/stripe", strings.NewReader(body))
	h.router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWebhook_Handler_InvalidSignature(t *testing.T) {
	h := newSubscriptionTestHarness()
	body := stripeEventBody("evt_signature", "customer.subscription.updated", `{"id":"sub_test","object":"subscription"}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/webhooks/stripe", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", "bad")
	h.router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWebhook_Handler_DuplicateEvent(t *testing.T) {
	h := newSubscriptionTestHarness()
	_, userID, _ := subRegisterAndLogin(t, h)

	now := time.Now()
	externalSubID := "sub_duplicate"
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

	webhookBody := stripeEventBody(
		"evt_duplicate",
		"customer.subscription.updated",
		stripeSubscriptionObject(externalSubID, "cust_test", sub.ID.String(), "active", "price_test", 1767225600, 1769904000),
	)
	first := subDoWebhookRequest(h, webhookBody)
	second := subDoWebhookRequest(h, webhookBody)

	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, http.StatusOK, second.Code)
}
