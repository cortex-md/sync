package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	stripe "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
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
	StripeWebhookSecret string
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

func (h *SubscriptionHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	event, err := h.webhook.ConstructEvent(r, rawBody)
	if err != nil {
		if isStripeSignatureError(err) {
			WriteError(w, http.StatusUnauthorized, "unauthorized webhook")
			return
		}
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}

	input, handled, err := stripeEventToUsecaseInput(event)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	if !handled {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

func (s WebhookSecurity) ConstructEvent(r *http.Request, rawBody []byte) (stripe.Event, error) {
	if strings.TrimSpace(s.StripeWebhookSecret) == "" {
		return stripe.Event{}, webhook.ErrNotSigned
	}
	return webhook.ConstructEventWithOptions(
		rawBody,
		r.Header.Get("Stripe-Signature"),
		s.StripeWebhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true},
	)
}

func isStripeSignatureError(err error) bool {
	if errors.Is(err, webhook.ErrNotSigned) || errors.Is(err, webhook.ErrNoValidSignature) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "signature")
}

func stripeEventToUsecaseInput(event stripe.Event) (usecase.SubscriptionWebhookInput, bool, error) {
	if event.ID == "" || event.Type == "" {
		return usecase.SubscriptionWebhookInput{}, false, domain.ErrInvalidInput
	}
	switch event.Type {
	case "checkout.session.completed":
		return checkoutSessionEventToUsecaseInput(event)
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		return subscriptionEventToUsecaseInput(event)
	default:
		return usecase.SubscriptionWebhookInput{}, false, nil
	}
}

func checkoutSessionEventToUsecaseInput(event stripe.Event) (usecase.SubscriptionWebhookInput, bool, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return usecase.SubscriptionWebhookInput{}, false, err
	}
	input := usecase.SubscriptionWebhookInput{
		EventID:                event.ID,
		Event:                  "subscription.checkout_completed",
		ExternalCheckoutID:     session.ID,
		ExternalID:             firstNonEmpty(session.ClientReferenceID, session.Metadata["subscription_id"]),
		ExternalCustomerID:     stripeCustomerID(session.Customer),
		ExternalSubscriptionID: stripeSubscriptionID(session.Subscription),
		OccurredAt:             stripeEventTime(event),
	}
	if input.ExternalSubscriptionID == "" && input.ExternalCheckoutID == "" && input.ExternalID == "" && input.ExternalCustomerID == "" {
		return usecase.SubscriptionWebhookInput{}, false, domain.ErrInvalidInput
	}
	return input, true, nil
}

func subscriptionEventToUsecaseInput(event stripe.Event) (usecase.SubscriptionWebhookInput, bool, error) {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return usecase.SubscriptionWebhookInput{}, false, err
	}
	periodStart, periodEnd, frequency, priceID := stripeSubscriptionPeriod(&subscription)
	input := usecase.SubscriptionWebhookInput{
		EventID:                event.ID,
		Event:                  normalizedStripeSubscriptionEvent(string(event.Type), subscription.Status),
		ExternalSubscriptionID: subscription.ID,
		ExternalID:             subscription.Metadata["subscription_id"],
		ExternalCustomerID:     stripeCustomerID(subscription.Customer),
		Status:                 string(subscription.Status),
		Frequency:              frequency,
		TrialEndsAt:            stripeUnixTime(subscription.TrialEnd),
		CurrentPeriodStart:     periodStart,
		CurrentPeriodEnd:       periodEnd,
		PlanPriceID:            priceID,
		OccurredAt:             stripeEventTime(event),
	}
	if input.ExternalSubscriptionID == "" && input.ExternalID == "" && input.ExternalCustomerID == "" {
		return usecase.SubscriptionWebhookInput{}, false, domain.ErrInvalidInput
	}
	return input, true, nil
}

func normalizedStripeSubscriptionEvent(eventType string, status stripe.SubscriptionStatus) string {
	if eventType == "customer.subscription.deleted" {
		return "subscription.cancelled"
	}
	switch status {
	case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusPastDue:
		if eventType == "customer.subscription.created" {
			return "subscription.completed"
		}
		return "subscription.renewed"
	case stripe.SubscriptionStatusTrialing:
		return "subscription.trial_started"
	case stripe.SubscriptionStatusCanceled, stripe.SubscriptionStatusIncompleteExpired, stripe.SubscriptionStatusUnpaid:
		return "subscription.cancelled"
	case stripe.SubscriptionStatusIncomplete, stripe.SubscriptionStatusPaused:
		return "subscription.pending"
	default:
		return "subscription.pending"
	}
}

func stripeSubscriptionPeriod(subscription *stripe.Subscription) (time.Time, time.Time, string, string) {
	if subscription.Items == nil {
		return time.Time{}, time.Time{}, "", ""
	}
	for _, item := range subscription.Items.Data {
		if item == nil {
			continue
		}
		frequency := ""
		priceID := ""
		if item.Price != nil {
			priceID = item.Price.ID
			if item.Price.Recurring != nil {
				frequency = stripeRecurringFrequency(item.Price.Recurring.Interval, item.Price.Recurring.IntervalCount)
			}
		}
		return stripeUnixTime(item.CurrentPeriodStart), stripeUnixTime(item.CurrentPeriodEnd), frequency, priceID
	}
	return time.Time{}, time.Time{}, "", ""
}

func stripeRecurringFrequency(interval stripe.PriceRecurringInterval, count int64) string {
	switch interval {
	case stripe.PriceRecurringIntervalWeek:
		if count == 1 {
			return "WEEKLY"
		}
	case stripe.PriceRecurringIntervalMonth:
		switch count {
		case 1:
			return "MONTHLY"
		case 3:
			return "QUARTERLY"
		case 6:
			return "SEMIANNUALLY"
		}
	case stripe.PriceRecurringIntervalYear:
		if count == 1 {
			return "ANNUALLY"
		}
	}
	return ""
}

func stripeEventTime(event stripe.Event) time.Time {
	return stripeUnixTime(event.Created)
}

func stripeUnixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func stripeCustomerID(customer *stripe.Customer) string {
	if customer == nil {
		return ""
	}
	return customer.ID
}

func stripeSubscriptionID(subscription *stripe.Subscription) string {
	if subscription == nil {
		return ""
	}
	return subscription.ID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
