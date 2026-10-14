CREATE INDEX IF NOT EXISTS idx_subscriptions_user_id ON subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_file_snapshots_vault_path_version ON file_snapshots(vault_id, file_path, version);
CREATE INDEX IF NOT EXISTS idx_file_snapshots_vault_path ON file_snapshots(vault_id, file_path);
DROP INDEX IF EXISTS idx_file_snapshots_device;
DROP INDEX IF EXISTS idx_file_snapshots_created_by;
DROP INDEX IF EXISTS idx_sync_events_created_at;
DROP TABLE IF EXISTS blob_deletions;
DROP TABLE IF EXISTS blob_uploads_pending;
DROP TABLE IF EXISTS device_vault_cursors;
DROP INDEX IF EXISTS idx_file_latest_vault_live;
DROP INDEX IF EXISTS idx_file_latest_vault_path_prefix;
ALTER TABLE file_latest
    DROP COLUMN IF EXISTS delta_bytes_since_snapshot,
    DROP COLUMN IF EXISTS deltas_since_snapshot,
    DROP COLUMN IF EXISTS created_by;
DROP INDEX IF EXISTS idx_vault_invites_pending;
ALTER TABLE vault_invites DROP COLUMN IF EXISTS grants;
DROP TABLE IF EXISTS vault_path_grants;
ALTER TABLE vaults
    DROP COLUMN IF EXISTS storage_quota_bytes,
    DROP COLUMN IF EXISTS storage_used_bytes,
    DROP COLUMN IF EXISTS allow_guests,
    DROP COLUMN IF EXISTS allowed_email_domains,
    DROP COLUMN IF EXISTS icon;
ALTER TABLE users DROP COLUMN IF EXISTS is_instance_admin;
