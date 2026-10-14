CREATE TABLE template_shares (
    id UUID PRIMARY KEY,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    owner_id UUID REFERENCES users(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('note', 'database')),
    audience TEXT NOT NULL CHECK (audience IN ('team', 'link', 'market')),
    group_id UUID REFERENCES vault_groups(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    include_samples BOOLEAN NOT NULL DEFAULT false,
    payload BYTEA NOT NULL,
    encrypted BOOLEAN NOT NULL DEFAULT false,
    file_count INT NOT NULL DEFAULT 0,
    version INT NOT NULL DEFAULT 1,
    status TEXT NOT NULL CHECK (status IN ('published', 'pending_review', 'rejected')),
    slug TEXT NOT NULL UNIQUE,
    review_note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_template_shares_vault ON template_shares(vault_id);
CREATE INDEX idx_template_shares_audience_status ON template_shares(audience, status);
