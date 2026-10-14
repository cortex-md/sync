ALTER TABLE users
    ADD COLUMN email_verified_at TIMESTAMPTZ,
    ADD COLUMN avatar_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN locale TEXT NOT NULL DEFAULT 'en';

CREATE TABLE user_tokens (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    payload TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_user_tokens_user_kind ON user_tokens(user_id, kind);
CREATE INDEX idx_user_tokens_expires_at ON user_tokens(expires_at);

CREATE TABLE email_outbox (
    id BIGSERIAL PRIMARY KEY,
    to_address TEXT NOT NULL,
    template TEXT NOT NULL,
    locale TEXT NOT NULL DEFAULT 'en',
    data JSONB NOT NULL DEFAULT '{}',
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_email_outbox_pending ON email_outbox(next_attempt_at) WHERE sent_at IS NULL;

ALTER TABLE vault_invites ADD COLUMN token_hash TEXT;
CREATE UNIQUE INDEX idx_vault_invites_token_hash ON vault_invites(token_hash) WHERE token_hash IS NOT NULL;

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

CREATE TABLE realtime_relay (
    id BIGSERIAL PRIMARY KEY,
    payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_realtime_relay_created_at ON realtime_relay(created_at);
