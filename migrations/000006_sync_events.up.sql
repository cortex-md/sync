CREATE TABLE sync_events (
    id BIGSERIAL PRIMARY KEY,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    file_path TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    actor_id UUID NOT NULL REFERENCES users(id),
    device_id UUID NOT NULL REFERENCES devices(id),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sync_events_vault_id_seq ON sync_events(vault_id, id);
CREATE INDEX idx_sync_events_created_at ON sync_events(created_at);

CREATE FUNCTION notify_sync_event() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('sync_events', json_build_object(
        'id', NEW.id,
        'vault_id', NEW.vault_id
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sync_event_notify
    AFTER INSERT ON sync_events
    FOR EACH ROW EXECUTE FUNCTION notify_sync_event();

CREATE TABLE sync_event_watermark (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    pruned_through BIGINT NOT NULL DEFAULT 0
);

INSERT INTO sync_event_watermark (id, pruned_through) VALUES (1, 0);

CREATE TABLE device_vault_cursors (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    last_event_id BIGINT NOT NULL DEFAULT 0,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, vault_id)
);

CREATE INDEX idx_device_vault_cursors_vault ON device_vault_cursors(vault_id);
