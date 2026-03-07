CREATE TABLE collab_updates (
    id         BIGSERIAL PRIMARY KEY,
    vault_id   UUID        NOT NULL,
    file_path  TEXT        NOT NULL,
    data       BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX collab_updates_lookup ON collab_updates (vault_id, file_path, id);

CREATE TABLE collab_documents (
    vault_id        UUID        NOT NULL,
    file_path       TEXT        NOT NULL,
    compacted_state BYTEA,
    state_vector    BYTEA,
    update_count    INT         NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (vault_id, file_path)
);
