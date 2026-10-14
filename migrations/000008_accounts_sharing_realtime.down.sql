DROP TABLE IF EXISTS realtime_relay;
DROP TABLE IF EXISTS collab_presence;
DROP INDEX IF EXISTS idx_vault_invites_token_hash;
ALTER TABLE vault_invites DROP COLUMN IF EXISTS token_hash;
DROP TABLE IF EXISTS email_outbox;
DROP TABLE IF EXISTS user_tokens;
ALTER TABLE users
    DROP COLUMN IF EXISTS locale,
    DROP COLUMN IF EXISTS avatar_key,
    DROP COLUMN IF EXISTS email_verified_at;
