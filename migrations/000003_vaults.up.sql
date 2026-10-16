CREATE TYPE vault_role AS ENUM ('owner', 'admin', 'editor', 'viewer', 'guest');

CREATE TABLE vaults (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    icon TEXT NOT NULL DEFAULT '',
    allowed_email_domains TEXT[] NOT NULL DEFAULT '{}',
    allow_guests BOOLEAN NOT NULL DEFAULT true,
    storage_used_bytes BIGINT NOT NULL DEFAULT 0,
    storage_quota_bytes BIGINT,
    allow_public_links BOOLEAN NOT NULL DEFAULT true
);

CREATE INDEX idx_vaults_owner_id ON vaults(owner_id);

CREATE TABLE vault_members (
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role vault_role NOT NULL DEFAULT 'viewer',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, user_id)
);

CREATE INDEX idx_vault_members_user_id ON vault_members(user_id);

CREATE TABLE vault_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    inviter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invitee_email TEXT NOT NULL,
    role vault_role NOT NULL DEFAULT 'viewer',
    encrypted_vault_key BYTEA,
    accepted BOOLEAN NOT NULL DEFAULT false,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    token_hash TEXT,
    grants JSONB NOT NULL DEFAULT '[]'
);

CREATE INDEX idx_vault_invites_vault_id ON vault_invites(vault_id);
CREATE INDEX idx_vault_invites_invitee_email_lower ON vault_invites(lower(invitee_email));
CREATE INDEX idx_vault_invites_pending ON vault_invites(vault_id) WHERE accepted = false;
CREATE UNIQUE INDEX idx_vault_invites_pending_email ON vault_invites(vault_id, lower(invitee_email)) WHERE accepted = false;
CREATE UNIQUE INDEX idx_vault_invites_token_hash ON vault_invites(token_hash) WHERE token_hash IS NOT NULL;

CREATE TABLE vault_keys (
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    encrypted_key BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, user_id)
);

CREATE TABLE vault_encryption (
    vault_id UUID PRIMARY KEY REFERENCES vaults(id) ON DELETE CASCADE,
    salt BYTEA NOT NULL,
    encrypted_vek BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
