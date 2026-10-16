CREATE TABLE subscriptions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    external_customer_id TEXT NOT NULL,
    external_subscription_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    current_period_start TIMESTAMPTZ,
    current_period_end TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    external_checkout_id TEXT NOT NULL DEFAULT '',
    entitlement_expires_at TIMESTAMPTZ,
    billing_cycle TEXT NOT NULL DEFAULT '',
    plan_product_id TEXT NOT NULL DEFAULT '',
    last_event_at TIMESTAMPTZ
);

CREATE INDEX idx_subscriptions_external_sub_id ON subscriptions(external_subscription_id);
CREATE INDEX idx_subscriptions_external_checkout_id ON subscriptions(external_checkout_id) WHERE external_checkout_id <> '';
CREATE INDEX idx_subscriptions_external_customer_id ON subscriptions(external_customer_id) WHERE external_customer_id <> '';
CREATE INDEX idx_subscriptions_entitlement_expires_at ON subscriptions(entitlement_expires_at);

CREATE FUNCTION notify_subscription_update() RETURNS TRIGGER AS $$
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

CREATE TABLE subscription_webhook_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    processed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
