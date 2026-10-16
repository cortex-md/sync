-- name: CreateVault :execrows
INSERT INTO vaults (id, name, description, owner_id, icon, allowed_email_domains, allow_guests, allow_public_links, storage_quota_bytes, created_at, updated_at)
VALUES (@id, @name, @description, @owner_id, @icon, @allowed_email_domains, @allow_guests, @allow_public_links, @storage_quota_bytes, @created_at, @updated_at)
ON CONFLICT (id) DO NOTHING;

-- name: GetVault :one
SELECT * FROM vaults WHERE id = $1;

-- name: UpdateVault :execrows
UPDATE vaults SET name = @name, description = @description, icon = @icon,
    allowed_email_domains = @allowed_email_domains, allow_guests = @allow_guests,
    allow_public_links = @allow_public_links,
    storage_quota_bytes = @storage_quota_bytes, updated_at = now()
WHERE id = @id;

-- name: DeleteVault :execrows
DELETE FROM vaults WHERE id = $1;

-- name: AddVaultStorage :exec
UPDATE vaults SET storage_used_bytes = GREATEST(storage_used_bytes + @delta, 0) WHERE id = @id;

-- name: AddVaultStorageWithinQuota :execrows
UPDATE vaults SET storage_used_bytes = GREATEST(storage_used_bytes + @delta, 0)
WHERE id = @id AND (storage_quota_bytes IS NULL OR storage_used_bytes + @delta <= storage_quota_bytes);

-- name: ListVaultSummariesForUser :many
SELECT v.*, m.role,
    (SELECT count(*) FROM vault_members c WHERE c.vault_id = v.id)::int AS member_count,
    EXISTS (SELECT 1 FROM vault_keys k WHERE k.vault_id = v.id AND k.user_id = m.user_id) AS has_key
FROM vault_members m
JOIN vaults v ON v.id = m.vault_id
WHERE m.user_id = $1
ORDER BY m.joined_at;

-- name: GetVaultSummaryForUser :one
SELECT v.*, m.role,
    (SELECT count(*) FROM vault_members c WHERE c.vault_id = v.id)::int AS member_count,
    EXISTS (SELECT 1 FROM vault_keys k WHERE k.vault_id = v.id AND k.user_id = m.user_id) AS has_key
FROM vault_members m
JOIN vaults v ON v.id = m.vault_id
WHERE m.vault_id = @vault_id AND m.user_id = @user_id;

-- name: LockVault :exec
SELECT 1 FROM vaults WHERE id = $1 FOR NO KEY UPDATE;

-- name: AddMember :exec
INSERT INTO vault_members (vault_id, user_id, role, joined_at) VALUES (@vault_id, @user_id, @role, @joined_at);

-- name: GetMember :one
SELECT * FROM vault_members WHERE vault_id = @vault_id AND user_id = @user_id;

-- name: ListMembers :many
SELECT * FROM vault_members WHERE vault_id = $1 ORDER BY joined_at;

-- name: ListMembershipsByUser :many
SELECT * FROM vault_members WHERE user_id = $1 ORDER BY joined_at;

-- name: UpdateMemberRole :execrows
UPDATE vault_members SET role = @role WHERE vault_id = @vault_id AND user_id = @user_id;

-- name: RemoveMember :execrows
DELETE FROM vault_members WHERE vault_id = @vault_id AND user_id = @user_id;

-- name: CreateInvite :execrows
INSERT INTO vault_invites (id, vault_id, inviter_id, invitee_email, role, encrypted_vault_key, token_hash, grants, accepted, expires_at, created_at)
VALUES (@id, @vault_id, @inviter_id, @invitee_email, @role, @encrypted_vault_key, NULLIF(@token_hash::text, ''), @grants, false, @expires_at, @created_at)
ON CONFLICT (id) DO NOTHING;

-- name: GetInvite :one
SELECT * FROM vault_invites WHERE id = $1;

-- name: GetInviteByTokenHash :one
SELECT * FROM vault_invites WHERE token_hash = $1;

-- name: RotateInviteToken :execrows
UPDATE vault_invites SET token_hash = @token_hash WHERE id = @id AND accepted = false;

-- name: ListInvitesByVault :many
SELECT * FROM vault_invites WHERE vault_id = $1 ORDER BY created_at;

-- name: ListPendingInvitesByVault :many
SELECT * FROM vault_invites WHERE vault_id = $1 AND accepted = false AND expires_at > now() ORDER BY created_at;

-- name: DeleteExpiredPendingInvite :exec
DELETE FROM vault_invites
WHERE vault_id = @vault_id AND lower(invitee_email) = lower(@email::text) AND accepted = false AND expires_at <= now();

-- name: PendingInviteExists :one
SELECT EXISTS (
    SELECT 1 FROM vault_invites
    WHERE vault_id = @vault_id AND lower(invitee_email) = lower(@email::text) AND accepted = false AND expires_at > now()
);

-- name: ListPendingInvitesForEmail :many
SELECT i.*, v.name AS vault_name, u.display_name AS inviter_name, u.email AS inviter_email
FROM vault_invites i
JOIN vaults v ON v.id = i.vault_id
JOIN users u ON u.id = i.inviter_id
WHERE lower(i.invitee_email) = lower(@email::text) AND i.accepted = false AND i.expires_at > now()
ORDER BY i.created_at;

-- name: AcceptInvite :execrows
UPDATE vault_invites SET accepted = true WHERE id = $1 AND accepted = false;

-- name: DeleteInvite :execrows
DELETE FROM vault_invites WHERE id = $1;

-- name: DeleteExpiredInvites :execrows
DELETE FROM vault_invites WHERE expires_at < now() AND accepted = false;

-- name: UpsertVaultKey :exec
INSERT INTO vault_keys (vault_id, user_id, encrypted_key, created_at, updated_at)
VALUES (@vault_id, @user_id, @encrypted_key, @created_at, @updated_at)
ON CONFLICT (vault_id, user_id) DO UPDATE SET encrypted_key = EXCLUDED.encrypted_key, updated_at = EXCLUDED.updated_at;

-- name: GetVaultKey :one
SELECT * FROM vault_keys WHERE vault_id = @vault_id AND user_id = @user_id;

-- name: ListVaultKeys :many
SELECT * FROM vault_keys WHERE vault_id = $1 ORDER BY created_at;

-- name: DeleteVaultKey :exec
DELETE FROM vault_keys WHERE vault_id = @vault_id AND user_id = @user_id;

-- name: DeleteVaultKeysForUser :exec
DELETE FROM vault_keys WHERE user_id = $1;

-- name: VaultHasKeys :one
SELECT EXISTS (SELECT 1 FROM vault_keys WHERE vault_id = $1);

-- name: UpsertVaultEncryption :exec
INSERT INTO vault_encryption (vault_id, salt, encrypted_vek, created_at)
VALUES (@vault_id, @salt, @encrypted_vek, @created_at)
ON CONFLICT (vault_id) DO UPDATE SET salt = EXCLUDED.salt, encrypted_vek = EXCLUDED.encrypted_vek;

-- name: GetVaultEncryption :one
SELECT * FROM vault_encryption WHERE vault_id = $1;

-- name: GetVaultForUpdate :one
SELECT * FROM vaults WHERE id = $1 FOR UPDATE;

-- name: ReconcileVaultStorage :execrows
UPDATE vaults v SET storage_used_bytes = usage.total
FROM (
    SELECT x.id,
        (coalesce((SELECT sum(s.size_bytes) FROM file_snapshots s WHERE s.vault_id = x.id), 0)
        + coalesce((SELECT sum(d.size_bytes) FROM file_deltas d WHERE d.vault_id = x.id), 0)
        + coalesce((SELECT sum(n.size_bytes) FROM note_shares n WHERE n.vault_id = x.id), 0))::bigint AS total
    FROM vaults x
) usage
WHERE v.id = usage.id AND v.storage_used_bytes <> usage.total;
