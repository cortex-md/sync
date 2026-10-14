ALTER TABLE devices ADD COLUMN last_sync_event_id BIGINT NOT NULL DEFAULT 0;
DROP TABLE IF EXISTS note_shares;
ALTER TABLE vaults DROP COLUMN IF EXISTS allow_public_links;
