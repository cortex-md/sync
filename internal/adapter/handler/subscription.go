package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/usecase"
)

type SubscriptionHandler struct {
	uc *usecase.SubscriptionUsecase
}

func NewSubscriptionHandler(uc *usecase.SubscriptionUsecase) *SubscriptionHandler {
	return &SubscriptionHandler{uc: uc}
}

type createCheckoutRequest struct {
	ReturnURL string `json:"return_url"`
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
		UserID:    claims.UserID,
		ReturnURL: req.ReturnURL,
	})
	if err != nil {
		handleSubscriptionError(w, err)
		return
	}

	WriteJSON(w, http.StatusOK, createCheckoutResponse{CheckoutURL: output.CheckoutURL})
}

type subscriptionStatusResponse struct {
	Status             string `json:"status"`
	CurrentPeriodStart string `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   string `json:"current_period_end,omitempty"`
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
			WriteJSON(w, http.StatusOK, subscriptionStatusResponse{Status: "none"})
			return
		}
		handleSubscriptionError(w, err)
		return
	}

	resp := subscriptionStatusResponse{
		Status: string(sub.Status),
	}
	if !sub.CurrentPeriodStart.IsZero() {
		resp.CurrentPeriodStart = sub.CurrentPeriodStart.Format(time.RFC3339)
	}
	if !sub.CurrentPeriodEnd.IsZero() {
		resp.CurrentPeriodEnd = sub.CurrentPeriodEnd.Format(time.RFC3339)
	}

	WriteJSON(w, http.StatusOK, resp)
}

type webhookPayload struct {
	Data struct {
		Billing struct {
			ID string `json:"id"`
		} `json:"billing"`
		Subscription struct {
			ID string `json:"id"`
		} `json:"subscription"`
	} `json:"data"`
	Event string `json:"event"`
}

func (h *SubscriptionHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	var payload webhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}

	subscriptionID := payload.Data.Subscription.ID
	if subscriptionID == "" {
		subscriptionID = payload.Data.Billing.ID
	}

	if subscriptionID == "" {
		WriteError(w, http.StatusBadRequest, "missing subscription or billing id")
		return
	}

	if err := h.uc.HandleWebhook(r.Context(), subscriptionID); err != nil {
		WriteError(w, http.StatusInternalServerError, "webhook processing failed")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleSubscriptionError(w http.ResponseWriter, err error) {
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
		WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}
