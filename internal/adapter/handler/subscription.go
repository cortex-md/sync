package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

type SubscriptionInvalidator interface {
	Invalidate(userID uuid.UUID)
}

type SubscriptionHandler struct {
	uc          *usecase.SubscriptionUsecase
	webhook     WebhookSecurity
	invalidator SubscriptionInvalidator
}

type WebhookSecurity struct {
	Secret  string
	HMACKey string
}

func NewSubscriptionHandler(uc *usecase.SubscriptionUsecase, webhook WebhookSecurity, invalidator SubscriptionInvalidator) *SubscriptionHandler {
	return &SubscriptionHandler{uc: uc, webhook: webhook, invalidator: invalidator}
}

type createCheckoutRequest struct {
	ReturnURL     string `json:"return_url"`
	CompletionURL string `json:"completion_url"`
}

type createCheckoutResponse struct {
	CheckoutURL string `json:"checkout_url"`
}

func (h *SubscriptionHandler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	output, err := h.uc.CreateCheckout(r.Context(), usecase.CreateCheckoutInput{
		UserID:        claims.UserID,
		ReturnURL:     req.ReturnURL,
		CompletionURL: req.CompletionURL,
	})
	if err != nil {
		handleSubscriptionError(r.Context(), w, err)
		return
	}

	WriteJSON(w, http.StatusOK, createCheckoutResponse{CheckoutURL: output.CheckoutURL})
}

type subscriptionStatusResponse struct {
	Status               string `json:"status"`
	Entitled             bool   `json:"entitled"`
	CurrentPeriodStart   string `json:"current_period_start,omitempty"`
	CurrentPeriodEnd     string `json:"current_period_end,omitempty"`
	EntitlementExpiresAt string `json:"entitlement_expires_at,omitempty"`
	BillingCycle         string `json:"billing_cycle,omitempty"`
	PlanProductID        string `json:"plan_product_id,omitempty"`
}

func (h *SubscriptionHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r.Context())
	if claims == nil {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sub, err := h.uc.GetStatus(r.Context(), claims.UserID)
	if err != nil {
		if err == domain.ErrNotFound {
			WriteJSON(w, http.StatusOK, subscriptionStatusResponse{Status: "none", Entitled: false})
			return
		}
		handleSubscriptionError(r.Context(), w, err)
		return
	}

	resp := subscriptionStatusResponse{
		Status:        string(sub.Status),
		Entitled:      sub.IsActive(),
		BillingCycle:  sub.BillingCycle,
		PlanProductID: sub.PlanProductID,
	}
	if !sub.CurrentPeriodStart.IsZero() {
		resp.CurrentPeriodStart = sub.CurrentPeriodStart.Format(time.RFC3339)
	}
	if !sub.CurrentPeriodEnd.IsZero() {
		resp.CurrentPeriodEnd = sub.CurrentPeriodEnd.Format(time.RFC3339)
	}
	if !sub.EntitlementExpiresAt.IsZero() {
		resp.EntitlementExpiresAt = sub.EntitlementExpiresAt.Format(time.RFC3339)
	}

	WriteJSON(w, http.StatusOK, resp)
}

type webhookPayload struct {
	ID      string `json:"id"`
	Event   string `json:"event"`
	DevMode bool   `json:"devMode"`
	Data    struct {
		Subscription struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			Frequency   string `json:"frequency"`
			TrialEndsAt string `json:"trialEndsAt"`
			CreatedAt   string `json:"createdAt"`
			UpdatedAt   string `json:"updatedAt"`
		} `json:"subscription"`
		Customer *struct {
			ID string `json:"id"`
		} `json:"customer"`
		Checkout *struct {
			ID         string `json:"id"`
			ExternalID string `json:"externalId"`
			CustomerID string `json:"customerId"`
			UpdatedAt  string `json:"updatedAt"`
			CreatedAt  string `json:"createdAt"`
		} `json:"checkout"`
	} `json:"data"`
}

func (h *SubscriptionHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	if !h.webhook.Valid(r, rawBody) {
		WriteError(w, http.StatusUnauthorized, "unauthorized webhook")
		return
	}

	var payload webhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}

	input, err := payload.toUsecaseInput()
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}

	output, err := h.uc.HandleWebhook(r.Context(), input)
	if err != nil {
		handleSubscriptionError(r.Context(), w, err)
		return
	}
	if output.UserID != uuid.Nil && h.invalidator != nil {
		h.invalidator.Invalidate(output.UserID)
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (p webhookPayload) toUsecaseInput() (usecase.SubscriptionWebhookInput, error) {
	if p.ID == "" || p.Event == "" {
		return usecase.SubscriptionWebhookInput{}, domain.ErrInvalidInput
	}
	customerID := ""
	if p.Data.Customer != nil {
		customerID = p.Data.Customer.ID
	}
	externalCheckoutID := ""
	externalID := ""
	if p.Data.Checkout != nil {
		externalCheckoutID = p.Data.Checkout.ID
		externalID = p.Data.Checkout.ExternalID
		if customerID == "" {
			customerID = p.Data.Checkout.CustomerID
		}
	}
	occurredAt := parseWebhookTime(p.Data.Subscription.UpdatedAt)
	if occurredAt.IsZero() {
		occurredAt = parseWebhookTime(p.Data.Subscription.CreatedAt)
	}
	if occurredAt.IsZero() && p.Data.Checkout != nil {
		occurredAt = parseWebhookTime(p.Data.Checkout.UpdatedAt)
		if occurredAt.IsZero() {
			occurredAt = parseWebhookTime(p.Data.Checkout.CreatedAt)
		}
	}
	if p.Data.Subscription.ID == "" && externalCheckoutID == "" && externalID == "" && customerID == "" {
		return usecase.SubscriptionWebhookInput{}, domain.ErrInvalidInput
	}
	return usecase.SubscriptionWebhookInput{
		EventID:                p.ID,
		Event:                  p.Event,
		ExternalSubscriptionID: p.Data.Subscription.ID,
		ExternalCheckoutID:     externalCheckoutID,
		ExternalID:             externalID,
		ExternalCustomerID:     customerID,
		Status:                 p.Data.Subscription.Status,
		Frequency:              p.Data.Subscription.Frequency,
		TrialEndsAt:            parseWebhookTime(p.Data.Subscription.TrialEndsAt),
		OccurredAt:             occurredAt,
	}, nil
}

func parseWebhookTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func (s WebhookSecurity) Valid(r *http.Request, rawBody []byte) bool {
	if s.Secret == "" || s.HMACKey == "" {
		return false
	}
	if !constantStringEqual(r.URL.Query().Get("webhookSecret"), s.Secret) {
		return false
	}
	signature := r.Header.Get("X-Webhook-Signature")
	if signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.HMACKey))
	mac.Write(rawBody)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return constantStringEqual(expected, signature)
}

func constantStringEqual(expected string, actual string) bool {
	if expected == "" || actual == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(actual))
}

func handleSubscriptionError(ctx context.Context, w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrInvalidInput:
		WriteError(w, http.StatusBadRequest, err.Error())
	case domain.ErrNotFound:
		WriteError(w, http.StatusNotFound, "user not found")
	case domain.ErrSubscriptionRequired:
		WriteErrorWithCode(w, http.StatusPaymentRequired, "subscription required", "subscription_required")
	case domain.ErrSubscriptionExpired:
		WriteErrorWithCode(w, http.StatusPaymentRequired, "subscription expired", "subscription_expired")
	default:
		zerolog.Ctx(ctx).Error().Err(err).Msg("subscription request failed")
		WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
