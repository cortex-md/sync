CREATE TABLE file_latest (
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    current_version INTEGER NOT NULL,
    latest_snapshot_version INTEGER NOT NULL,
    checksum TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT 'text/markdown',
    deleted BOOLEAN NOT NULL DEFAULT false,
    last_modified_by UUID NOT NULL REFERENCES users(id),
    last_device_id UUID NOT NULL REFERENCES devices(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID NOT NULL REFERENCES users(id),
    deltas_since_snapshot INTEGER NOT NULL DEFAULT 0,
    delta_bytes_since_snapshot BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (vault_id, file_path)
);

CREATE INDEX idx_file_latest_vault_path_prefix ON file_latest(vault_id, file_path text_pattern_ops);
CREATE INDEX idx_file_latest_vault_live ON file_latest(vault_id, file_path text_pattern_ops) WHERE deleted = false;

CREATE TABLE file_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    version INTEGER NOT NULL,
    encrypted_blob_key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    checksum TEXT NOT NULL DEFAULT '',
    created_by UUID NOT NULL REFERENCES users(id),
    device_id UUID NOT NULL REFERENCES devices(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vault_id, file_path, version)
);

CREATE INDEX idx_file_snapshots_created_by ON file_snapshots(created_by);
CREATE INDEX idx_file_snapshots_device ON file_snapshots(device_id);

CREATE TABLE file_deltas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    base_version INTEGER NOT NULL,
    target_version INTEGER NOT NULL,
    encrypted_delta BYTEA NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    created_by UUID NOT NULL REFERENCES users(id),
    device_id UUID NOT NULL REFERENCES devices(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vault_id, file_path, base_version, target_version)
);
