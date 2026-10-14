CREATE TABLE vault_path_grants (
    vault_id UUID NOT NULL,
    user_id UUID NOT NULL,
    path_prefix TEXT NOT NULL,
    can_write BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, user_id, path_prefix),
    FOREIGN KEY (vault_id, user_id) REFERENCES vault_members(vault_id, user_id) ON DELETE CASCADE
);
CREATE INDEX idx_vault_path_grants_user ON vault_path_grants(user_id);

INSERT INTO vault_path_grants (vault_id, user_id, path_prefix, can_write, created_by, created_at)
SELECT a.vault_id, a.principal_id, a.path, a.level = 'edit', v.owner_id, r.updated_at
FROM vault_path_access a
JOIN vault_path_rules r ON r.vault_id = a.vault_id AND r.path = a.path
JOIN vaults v ON v.id = a.vault_id
JOIN vault_members m ON m.vault_id = a.vault_id AND m.user_id = a.principal_id AND m.role = 'guest'
WHERE a.principal_type = 'user';

DROP TABLE IF EXISTS vault_path_access;
DROP TABLE IF EXISTS vault_path_rules;
DROP TABLE IF EXISTS vault_group_members;
DROP TABLE IF EXISTS vault_groups;

ALTER TABLE users
    DROP COLUMN IF EXISTS recovery_wrapped_private_key,
    DROP COLUMN IF EXISTS encrypted_private_key,
    DROP COLUMN IF EXISTS kdf_salt,
    DROP COLUMN IF EXISTS kdf_version;
