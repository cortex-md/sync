CREATE TABLE vault_groups (
    id UUID PRIMARY KEY,
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_vault_groups_vault_name ON vault_groups(vault_id, lower(name));

CREATE TABLE vault_group_members (
    group_id UUID NOT NULL REFERENCES vault_groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vault_id UUID NOT NULL,
    PRIMARY KEY (group_id, user_id),
    FOREIGN KEY (vault_id, user_id) REFERENCES vault_members(vault_id, user_id) ON DELETE CASCADE
);

CREATE INDEX idx_vault_group_members_vault_user ON vault_group_members(vault_id, user_id);

CREATE TABLE vault_path_rules (
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    restricted BOOLEAN NOT NULL DEFAULT false,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, path)
);

CREATE INDEX idx_vault_path_rules_restricted ON vault_path_rules(vault_id) WHERE restricted;

CREATE TABLE vault_path_access (
    vault_id UUID NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    principal_type TEXT NOT NULL CHECK (principal_type IN ('user', 'group')),
    principal_id UUID NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('view', 'edit')),
    PRIMARY KEY (vault_id, path, principal_type, principal_id),
    FOREIGN KEY (vault_id, path) REFERENCES vault_path_rules(vault_id, path) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE INDEX idx_vault_path_access_principal ON vault_path_access(vault_id, principal_id);
