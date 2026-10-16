CREATE TABLE collab_documents (
    vault_id UUID NOT NULL,
    file_path TEXT NOT NULL,
    compacted_state BYTEA,
    state_vector BYTEA,
    update_count INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    compacted_up_to BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (vault_id, file_path)
);

CREATE TABLE collab_updates (
    id BIGSERIAL PRIMARY KEY,
    vault_id UUID NOT NULL,
    file_path TEXT NOT NULL,
    data BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX collab_updates_lookup ON collab_updates(vault_id, file_path, id);

CREATE TABLE collab_presence (
    client_id TEXT PRIMARY KEY,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    can_write BOOLEAN NOT NULL DEFAULT false,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_collab_presence_room ON collab_presence(vault_id, file_path);
CREATE INDEX idx_collab_presence_last_seen ON collab_presence(last_seen_at);
