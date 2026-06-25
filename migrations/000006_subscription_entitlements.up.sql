ALTER TABLE subscriptions
    ADD COLUMN external_checkout_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN entitlement_expires_at TIMESTAMPTZ,
    ADD COLUMN billing_cycle TEXT NOT NULL DEFAULT '',
    ADD COLUMN plan_product_id TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_subscriptions_external_checkout_id
    ON subscriptions(external_checkout_id)
    WHERE external_checkout_id <> '';

CREATE INDEX idx_subscriptions_external_customer_id
    ON subscriptions(external_customer_id)
    WHERE external_customer_id <> '';

CREATE INDEX idx_subscriptions_entitlement_expires_at
    ON subscriptions(entitlement_expires_at);

CREATE TABLE subscription_webhook_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION notify_subscription_update()
RETURNS TRIGGER AS $$
DECLARE
    payload JSON;
BEGIN
    payload = json_build_object('user_id', COALESCE(NEW.user_id, OLD.user_id)::TEXT);
    PERFORM pg_notify('subscription_updates', payload::TEXT);
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER subscriptions_notify_update
AFTER INSERT OR UPDATE OR DELETE ON subscriptions
FOR EACH ROW EXECUTE FUNCTION notify_subscription_update();
