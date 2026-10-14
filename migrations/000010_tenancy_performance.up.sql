ALTER TABLE users ADD COLUMN is_instance_admin BOOLEAN NOT NULL DEFAULT false;
UPDATE users SET is_instance_admin = true
WHERE id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1);

ALTER TABLE vaults
    ADD COLUMN icon TEXT NOT NULL DEFAULT '',
    ADD COLUMN allowed_email_domains TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN allow_guests BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN storage_used_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN storage_quota_bytes BIGINT;

UPDATE vaults v SET storage_used_bytes = s.total
FROM (SELECT vault_id, SUM(size_bytes) AS total FROM file_snapshots GROUP BY vault_id) s
WHERE s.vault_id = v.id;

CREATE TABLE vault_path_grants (
    vault_id UUID NOT NULL,
    user_id UUID NOT NULL,
    path_prefix TEXT NOT NULL,
    can_write BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, user_id, path_prefix),
    FOREIGN KEY (vault_id, user_id) REFERENCES vault_members(vault_id, user_id) ON DELETE CASCADE
);
CREATE INDEX idx_vault_path_grants_user ON vault_path_grants(user_id);

ALTER TABLE vault_invites ADD COLUMN grants JSONB NOT NULL DEFAULT '[]';
CREATE INDEX idx_vault_invites_pending ON vault_invites(vault_id) WHERE accepted = false;

ALTER TABLE file_latest
    ADD COLUMN created_by UUID REFERENCES users(id),
    ADD COLUMN deltas_since_snapshot INT NOT NULL DEFAULT 0,
    ADD COLUMN delta_bytes_since_snapshot BIGINT NOT NULL DEFAULT 0;

UPDATE file_latest fl SET created_by = first.created_by
FROM (
    SELECT DISTINCT ON (vault_id, file_path) vault_id, file_path, created_by
    FROM file_snapshots
    ORDER BY vault_id, file_path, version
) first
WHERE first.vault_id = fl.vault_id AND first.file_path = fl.file_path;
UPDATE file_latest SET created_by = last_modified_by WHERE created_by IS NULL;
ALTER TABLE file_latest ALTER COLUMN created_by SET NOT NULL;

UPDATE file_latest fl SET deltas_since_snapshot = d.cnt, delta_bytes_since_snapshot = d.bytes
FROM (
    SELECT d.vault_id, d.file_path, COUNT(*) AS cnt, SUM(d.size_bytes) AS bytes
    FROM file_deltas d
    JOIN file_latest l ON l.vault_id = d.vault_id AND l.file_path = d.file_path
    WHERE d.base_version >= l.latest_snapshot_version
    GROUP BY d.vault_id, d.file_path
) d
WHERE d.vault_id = fl.vault_id AND d.file_path = fl.file_path;

CREATE INDEX idx_file_latest_vault_path_prefix ON file_latest (vault_id, file_path text_pattern_ops);
CREATE INDEX idx_file_latest_vault_live ON file_latest (vault_id, file_path text_pattern_ops) WHERE deleted = false;

CREATE TABLE device_vault_cursors (
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    last_event_id BIGINT NOT NULL DEFAULT 0,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, vault_id)
);
CREATE INDEX idx_device_vault_cursors_vault ON device_vault_cursors(vault_id);

CREATE TABLE blob_uploads_pending (
    blob_key TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blob_deletions (
    id BIGSERIAL PRIMARY KEY,
    blob_key TEXT NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_blob_deletions_due ON blob_deletions(next_attempt_at);

CREATE INDEX idx_sync_events_created_at ON sync_events(created_at);
CREATE INDEX idx_file_snapshots_created_by ON file_snapshots(created_by);
CREATE INDEX idx_file_snapshots_device ON file_snapshots(device_id);
DROP INDEX IF EXISTS idx_file_snapshots_vault_path;
DROP INDEX IF EXISTS idx_file_snapshots_vault_path_version;
DROP INDEX IF EXISTS idx_subscriptions_user_id;
