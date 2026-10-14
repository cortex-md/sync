DROP INDEX IF EXISTS idx_template_shares_audience_status_updated;
DROP INDEX IF EXISTS idx_template_shares_vault_updated;
CREATE INDEX IF NOT EXISTS idx_template_shares_audience_status ON template_shares(audience, status);
CREATE INDEX IF NOT EXISTS idx_template_shares_vault ON template_shares(vault_id);

CREATE INDEX IF NOT EXISTS idx_sync_events_vault_created ON sync_events(vault_id, created_at);
CREATE INDEX IF NOT EXISTS idx_file_deltas_vault_path ON file_deltas(vault_id, file_path);
