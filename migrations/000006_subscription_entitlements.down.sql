DROP TRIGGER IF EXISTS subscriptions_notify_update ON subscriptions;
DROP FUNCTION IF EXISTS notify_subscription_update();

DROP TABLE IF EXISTS subscription_webhook_events;

DROP INDEX IF EXISTS idx_subscriptions_entitlement_expires_at;
DROP INDEX IF EXISTS idx_subscriptions_external_customer_id;
DROP INDEX IF EXISTS idx_subscriptions_external_checkout_id;

ALTER TABLE subscriptions
    DROP COLUMN IF EXISTS plan_product_id,
    DROP COLUMN IF EXISTS billing_cycle,
    DROP COLUMN IF EXISTS entitlement_expires_at,
    DROP COLUMN IF EXISTS external_checkout_id;
