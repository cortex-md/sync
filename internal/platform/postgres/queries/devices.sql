-- name: CreateDevice :exec
INSERT INTO devices (id, user_id, device_name, device_type, device_token, last_seen_at, created_at, revoked)
VALUES (@id, @user_id, @device_name, @device_type, @device_token, @last_seen_at, @created_at, @revoked);

-- name: GetDeviceByID :one
SELECT * FROM devices WHERE id = $1;

-- name: GetDeviceByToken :one
SELECT * FROM devices WHERE device_token = $1;

-- name: ListDevicesByUser :many
SELECT * FROM devices WHERE user_id = $1 ORDER BY created_at;

-- name: ListDevicesByIDs :many
SELECT * FROM devices WHERE id = ANY(@ids::uuid[]);

-- name: TouchDevice :exec
UPDATE devices SET last_seen_at = now()
WHERE id = @id AND last_seen_at < now() - interval '1 minute';

-- name: RevokeDevice :execrows
UPDATE devices SET revoked = true WHERE id = $1;

-- name: UpdateDevice :execrows
UPDATE devices SET user_id = @user_id, device_name = @device_name, device_type = @device_type,
    device_token = @device_token, last_seen_at = @last_seen_at, revoked = @revoked
WHERE id = @id;


-- name: UpsertDeviceVaultCursor :exec
INSERT INTO device_vault_cursors (device_id, vault_id, last_event_id, synced_at)
VALUES (@device_id, @vault_id, @last_event_id, now())
ON CONFLICT (device_id, vault_id) DO UPDATE
SET last_event_id = GREATEST(device_vault_cursors.last_event_id, EXCLUDED.last_event_id), synced_at = now();

-- name: ListDeviceVaultCursorsByUser :many
SELECT c.* FROM device_vault_cursors c
JOIN devices d ON d.id = c.device_id
WHERE d.user_id = $1;

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, device_id, token_hash, family_id, expires_at, revoked, created_at)
VALUES (@id, @user_id, @device_id, @token_hash, @family_id, @expires_at, @revoked, @created_at);

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens WHERE token_hash = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked = true WHERE id = $1;

-- name: RevokeRefreshTokenIfActive :execrows
UPDATE refresh_tokens SET revoked = true, rotated_at = now() WHERE id = $1 AND revoked = false;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens SET revoked = true WHERE family_id = $1;

-- name: RevokeRefreshTokensByUser :exec
UPDATE refresh_tokens SET revoked = true WHERE user_id = $1;

-- name: RevokeRefreshTokensByDevice :exec
UPDATE refresh_tokens SET revoked = true WHERE device_id = $1;

-- name: RevokeRefreshTokensByUserExceptDevice :exec
UPDATE refresh_tokens SET revoked = true WHERE user_id = @user_id AND device_id <> @device_id;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens WHERE id IN (
    SELECT t.id FROM refresh_tokens t WHERE t.expires_at < now() ORDER BY t.expires_at LIMIT sqlc.arg(row_limit)
);

-- name: ListDevicesByUsers :many
SELECT * FROM devices WHERE user_id = ANY(@user_ids::uuid[]) AND revoked = false ORDER BY user_id, created_at;

-- name: ListDeviceVaultCursorsByVault :many
SELECT * FROM device_vault_cursors WHERE vault_id = $1;
