DROP INDEX IF EXISTS idx_file_deltas_vault_path;
DROP INDEX IF EXISTS idx_sync_events_vault_created;

DROP INDEX IF EXISTS idx_template_shares_vault;
DROP INDEX IF EXISTS idx_template_shares_audience_status;
CREATE INDEX IF NOT EXISTS idx_template_shares_vault_updated ON template_shares(vault_id, updated_at DESC, id);
CREATE INDEX IF NOT EXISTS idx_template_shares_audience_status_updated ON template_shares(audience, status, updated_at DESC, id);
