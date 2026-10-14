CREATE OR REPLACE FUNCTION notify_sync_event()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('sync_events', json_build_object(
        'id', NEW.id,
        'vault_id', NEW.vault_id
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

UPDATE users u SET email = lower(trim(u.email))
WHERE u.email <> lower(trim(u.email))
  AND NOT EXISTS (
      SELECT 1 FROM users o WHERE o.id <> u.id AND lower(trim(o.email)) = lower(trim(u.email))
  );

UPDATE vault_invites SET invitee_email = lower(trim(invitee_email))
WHERE invitee_email <> lower(trim(invitee_email));

CREATE INDEX IF NOT EXISTS idx_users_email_lower ON users (lower(email));
DROP INDEX IF EXISTS idx_vault_invites_invitee_email;
CREATE INDEX IF NOT EXISTS idx_vault_invites_invitee_email_lower ON vault_invites (lower(invitee_email));

DROP INDEX IF EXISTS idx_refresh_tokens_token_hash;
DROP INDEX IF EXISTS idx_devices_device_token;
DROP INDEX IF EXISTS idx_sync_events_vault_id;
