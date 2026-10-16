CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    public_key BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    email_verified_at TIMESTAMPTZ,
    avatar_key TEXT NOT NULL DEFAULT '',
    locale TEXT NOT NULL DEFAULT 'en',
    is_instance_admin BOOLEAN NOT NULL DEFAULT false,
    kdf_version SMALLINT NOT NULL DEFAULT 0,
    kdf_salt BYTEA,
    encrypted_private_key BYTEA,
    recovery_wrapped_private_key BYTEA
);

CREATE INDEX idx_users_email_lower ON users(lower(email));

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

CREATE INDEX idx_user_tokens_expires_at ON user_tokens(expires_at);
CREATE INDEX idx_user_tokens_user_kind ON user_tokens(user_id, kind);
