-- name: ListUserRuleAccess :many
WITH my_groups AS (
    SELECT group_id FROM vault_group_members WHERE vault_group_members.vault_id = @vault_id AND vault_group_members.user_id = @user_id
), mine AS (
    SELECT a.path, bool_or(a.level = 'edit') AS can_edit
    FROM vault_path_access a
    WHERE a.vault_id = @vault_id
      AND ((a.principal_type = 'user' AND a.principal_id = @user_id)
        OR (a.principal_type = 'group' AND a.principal_id IN (SELECT group_id FROM my_groups)))
    GROUP BY a.path
)
SELECT r.path, r.restricted, (m.path IS NOT NULL)::bool AS has_entry, coalesce(m.can_edit, false)::bool AS can_edit
FROM vault_path_rules r
LEFT JOIN mine m ON m.path = r.path
WHERE r.vault_id = @vault_id AND (r.restricted OR m.path IS NOT NULL);

-- name: CreateGroup :exec
INSERT INTO vault_groups (id, vault_id, name, created_by, created_at, updated_at)
VALUES (@id, @vault_id, @name, @created_by, @created_at, @updated_at);

-- name: GetGroup :one
SELECT * FROM vault_groups WHERE vault_id = @vault_id AND id = @id;

-- name: ListGroups :many
SELECT g.id, g.vault_id, g.name, g.created_by, g.created_at, g.updated_at,
    coalesce(array_agg(m.user_id ORDER BY m.user_id) FILTER (WHERE m.user_id IS NOT NULL), '{}')::uuid[] AS member_ids
FROM vault_groups g
LEFT JOIN vault_group_members m ON m.group_id = g.id
WHERE g.vault_id = $1
GROUP BY g.id
ORDER BY lower(g.name), g.id;

-- name: RenameGroup :execrows
UPDATE vault_groups SET name = @name, updated_at = now() WHERE vault_id = @vault_id AND id = @id;

-- name: TouchGroup :execrows
UPDATE vault_groups SET updated_at = now() WHERE vault_id = @vault_id AND id = @id;

-- name: DeleteGroup :execrows
DELETE FROM vault_groups WHERE vault_id = @vault_id AND id = @id;

-- name: DeleteGroupMembers :exec
DELETE FROM vault_group_members WHERE group_id = $1;

-- name: AddGroupMembers :exec
INSERT INTO vault_group_members (group_id, user_id, vault_id)
SELECT @group_id, unnest(@user_ids::uuid[]), @vault_id
ON CONFLICT DO NOTHING;

-- name: DeleteUserGroupMemberships :exec
DELETE FROM vault_group_members WHERE vault_id = @vault_id AND user_id = @user_id;

-- name: DeletePrincipalEntries :exec
DELETE FROM vault_path_access
WHERE vault_id = @vault_id AND principal_type = @principal_type AND principal_id = @principal_id;

-- name: DeleteOrphanRules :exec
DELETE FROM vault_path_rules r
WHERE r.vault_id = $1 AND NOT r.restricted
  AND NOT EXISTS (SELECT 1 FROM vault_path_access a WHERE a.vault_id = r.vault_id AND a.path = r.path);

-- name: ListRules :many
SELECT * FROM vault_path_rules WHERE vault_id = $1 ORDER BY path;

-- name: ListEntries :many
SELECT * FROM vault_path_access WHERE vault_id = $1 ORDER BY path, principal_type, principal_id;

-- name: UpsertRule :exec
INSERT INTO vault_path_rules (vault_id, path, restricted, updated_by, updated_at)
VALUES (@vault_id, @path, @restricted, @updated_by, now())
ON CONFLICT (vault_id, path) DO UPDATE
SET restricted = EXCLUDED.restricted, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: EnsureRule :exec
INSERT INTO vault_path_rules (vault_id, path, restricted, updated_by, updated_at)
VALUES (@vault_id, @path, false, @updated_by, now())
ON CONFLICT (vault_id, path) DO NOTHING;

-- name: DeleteRule :execrows
DELETE FROM vault_path_rules WHERE vault_id = @vault_id AND path = @path;

-- name: DeleteRuleEntries :exec
DELETE FROM vault_path_access WHERE vault_id = @vault_id AND path = @path;

-- name: InsertEntries :exec
INSERT INTO vault_path_access (vault_id, path, principal_type, principal_id, level)
SELECT @vault_id, @path, unnest(@principal_types::text[]), unnest(@principal_ids::uuid[]), unnest(@levels::text[]);

-- name: UpsertEntry :exec
INSERT INTO vault_path_access (vault_id, path, principal_type, principal_id, level)
VALUES (@vault_id, @path, @principal_type, @principal_id, @level)
ON CONFLICT (vault_id, path, principal_type, principal_id) DO UPDATE SET level = EXCLUDED.level;

-- name: ListRulesCovering :many
SELECT path FROM vault_path_rules
WHERE vault_id = @vault_id AND (path = @path::text OR (right(path, 1) = '/' AND starts_with(@path::text, path)))
ORDER BY length(path);

-- name: RuleExists :one
SELECT EXISTS (SELECT 1 FROM vault_path_rules WHERE vault_id = @vault_id AND path = @path);

-- name: LiveFileExistsUnder :one
SELECT EXISTS (
    SELECT 1 FROM file_latest
    WHERE vault_id = @vault_id AND deleted = false AND file_path LIKE @pattern::text AND file_path <> @exclude::text
);

-- name: CopyRule :exec
INSERT INTO vault_path_rules (vault_id, path, restricted, updated_by, updated_at)
SELECT src.vault_id, @to_path::text, src.restricted, src.updated_by, now()
FROM vault_path_rules src WHERE src.vault_id = @vault_id AND src.path = @from_path::text
ON CONFLICT (vault_id, path) DO NOTHING;

-- name: CopyRuleEntries :exec
INSERT INTO vault_path_access (vault_id, path, principal_type, principal_id, level)
SELECT src.vault_id, @to_path::text, src.principal_type, src.principal_id, src.level
FROM vault_path_access src WHERE src.vault_id = @vault_id AND src.path = @from_path::text
ON CONFLICT (vault_id, path, principal_type, principal_id) DO NOTHING;

-- name: MoveRule :execrows
UPDATE vault_path_rules SET path = @to_path::text, updated_at = now()
WHERE vault_id = @vault_id AND path = @from_path::text;
