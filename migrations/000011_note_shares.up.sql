CREATE TABLE note_shares (
    id TEXT PRIMARY KEY,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wrapped_key BYTEA NOT NULL,
    revision INTEGER NOT NULL DEFAULT 0,
    blob_key TEXT,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vault_id, file_path)
);

CREATE INDEX idx_note_shares_expires_at ON note_shares(expires_at) WHERE expires_at IS NOT NULL;
