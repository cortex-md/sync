DROP TABLE IF EXISTS subscription_webhook_events;
DROP TRIGGER IF EXISTS subscriptions_notify_update ON subscriptions;
DROP FUNCTION IF EXISTS notify_subscription_update();
DROP TABLE IF EXISTS subscriptions;
