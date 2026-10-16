-- name: CreateSubscription :exec
INSERT INTO subscriptions (id, user_id, external_customer_id, external_subscription_id, external_checkout_id,
    status, current_period_start, current_period_end, entitlement_expires_at, billing_cycle, plan_product_id, created_at, updated_at)
VALUES (@id, @user_id, @external_customer_id, @external_subscription_id, @external_checkout_id,
    @status, @current_period_start, @current_period_end, @entitlement_expires_at, @billing_cycle, @plan_product_id, @created_at, @updated_at);

-- name: GetSubscription :one
SELECT * FROM subscriptions WHERE id = $1;

-- name: GetSubscriptionByUser :one
SELECT * FROM subscriptions WHERE user_id = $1;

-- name: GetSubscriptionByExternalSubscription :one
SELECT * FROM subscriptions WHERE external_subscription_id = $1 AND external_subscription_id <> '';

-- name: GetSubscriptionByExternalCheckout :one
SELECT * FROM subscriptions WHERE external_checkout_id = $1 AND external_checkout_id <> '';

-- name: GetSubscriptionByExternalCustomer :one
SELECT * FROM subscriptions WHERE external_customer_id = $1 AND external_customer_id <> '';

-- name: UpdateSubscription :execrows
UPDATE subscriptions SET external_customer_id = @external_customer_id, external_subscription_id = @external_subscription_id,
    external_checkout_id = @external_checkout_id, status = @status, current_period_start = @current_period_start,
    current_period_end = @current_period_end, entitlement_expires_at = @entitlement_expires_at,
    billing_cycle = @billing_cycle, plan_product_id = @plan_product_id, updated_at = @updated_at,
    last_event_at = @last_event_at
WHERE id = @id;

-- name: CreateWebhookEvent :exec
INSERT INTO subscription_webhook_events (id, event_type, processed_at, created_at) VALUES (@id, @event_type, @processed_at, @created_at);

-- name: MarkWebhookEventProcessed :execrows
UPDATE subscription_webhook_events SET processed_at = @processed_at WHERE id = @id;

-- name: DeleteWebhookEvent :exec
DELETE FROM subscription_webhook_events WHERE id = $1;
