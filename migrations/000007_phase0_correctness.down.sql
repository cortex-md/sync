CREATE INDEX IF NOT EXISTS idx_sync_events_vault_id ON sync_events(vault_id);
CREATE INDEX IF NOT EXISTS idx_devices_device_token ON devices(device_token);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token_hash ON refresh_tokens(token_hash);

DROP INDEX IF EXISTS idx_vault_invites_invitee_email_lower;
CREATE INDEX IF NOT EXISTS idx_vault_invites_invitee_email ON vault_invites(invitee_email);
DROP INDEX IF EXISTS idx_users_email_lower;

CREATE OR REPLACE FUNCTION notify_sync_event()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('sync_events', json_build_object(
        'id', NEW.id,
        'vault_id', NEW.vault_id,
        'event_type', NEW.event_type,
        'file_path', NEW.file_path,
        'version', NEW.version,
        'actor_id', NEW.actor_id,
        'device_id', NEW.device_id
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
