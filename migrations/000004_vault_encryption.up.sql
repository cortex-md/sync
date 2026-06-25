CREATE TABLE IF NOT EXISTS vault_encryption (
    vault_id UUID PRIMARY KEY REFERENCES vaults(id) ON DELETE CASCADE,
    salt BYTEA NOT NULL,
    encrypted_vek BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
