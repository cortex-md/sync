-- name: CreateShare :one
INSERT INTO note_shares (id, vault_id, file_path, created_by, wrapped_key, expires_at)
VALUES (@id, @vault_id, @file_path, @created_by, @wrapped_key, @expires_at)
ON CONFLICT (vault_id, file_path) DO NOTHING
RETURNING *;

-- name: GetShare :one
SELECT * FROM note_shares WHERE id = $1;

-- name: GetShareForUpdate :one
SELECT * FROM note_shares WHERE id = $1 FOR UPDATE;

-- name: GetShareByPath :one
SELECT * FROM note_shares WHERE vault_id = @vault_id AND file_path = @file_path;

-- name: ListSharesByVault :many
SELECT * FROM note_shares WHERE vault_id = $1 ORDER BY file_path;

-- name: UpdateShareContent :exec
UPDATE note_shares SET revision = @revision, blob_key = @blob_key, size_bytes = @size_bytes, updated_at = now()
WHERE id = @id;

-- name: UpdateShareExpiry :exec
UPDATE note_shares SET expires_at = @expires_at, updated_at = now() WHERE id = @id;

-- name: DeleteShare :one
DELETE FROM note_shares WHERE id = $1 RETURNING vault_id, blob_key, size_bytes;

-- name: DeleteShareByPath :many
DELETE FROM note_shares WHERE vault_id = @vault_id AND file_path = @file_path
RETURNING vault_id, blob_key, size_bytes;

-- name: MoveShare :exec
UPDATE note_shares SET file_path = @new_path, updated_at = now()
WHERE vault_id = @vault_id AND file_path = @old_path;

-- name: ListShareBlobsByVault :many
SELECT blob_key FROM note_shares WHERE vault_id = $1 AND blob_key IS NOT NULL;

-- name: DeleteExpiredShares :many
DELETE FROM note_shares WHERE id IN (
    SELECT s.id FROM note_shares s
    WHERE s.expires_at IS NOT NULL AND s.expires_at < now()
    ORDER BY s.expires_at LIMIT @row_limit
    FOR UPDATE SKIP LOCKED
)
RETURNING vault_id, blob_key, size_bytes;
