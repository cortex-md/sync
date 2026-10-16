DROP TABLE IF EXISTS device_vault_cursors;
DROP TABLE IF EXISTS sync_event_watermark;
DROP TRIGGER IF EXISTS sync_event_notify ON sync_events;
DROP FUNCTION IF EXISTS notify_sync_event();
DROP TABLE IF EXISTS sync_events;
