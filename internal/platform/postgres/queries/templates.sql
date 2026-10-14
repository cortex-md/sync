-- name: CreateTemplateShare :one
INSERT INTO template_shares (
    id, vault_id, owner_id, kind, audience, group_id, name, description, include_samples,
    payload, encrypted, file_count, version, status, slug, review_note
) VALUES (
    @id, @vault_id, @owner_id, @kind, @audience, @group_id, @name, @description, @include_samples,
    @payload, @encrypted, @file_count, @version, @status, @slug, @review_note
)
RETURNING created_at, updated_at;

-- name: GetTemplateShare :one
SELECT * FROM template_shares WHERE id = $1;

-- name: GetTemplateShareBySlug :one
SELECT * FROM template_shares WHERE slug = $1;

-- name: ListTemplateSharesByVault :many
SELECT id, vault_id, owner_id, kind, audience, group_id, name, description, include_samples,
    encrypted, file_count, version, status, slug, review_note, created_at, updated_at
FROM template_shares
WHERE vault_id = $1
ORDER BY updated_at DESC, id;

-- name: ListMarketTemplateShares :many
SELECT id, vault_id, owner_id, kind, audience, group_id, name, description, include_samples,
    encrypted, file_count, version, status, slug, review_note, created_at, updated_at
FROM template_shares
WHERE audience = 'market' AND status = 'published'
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(pattern)::text IS NULL OR name ILIKE sqlc.narg(pattern)::text OR description ILIKE sqlc.narg(pattern)::text)
ORDER BY updated_at DESC, id
LIMIT @row_limit;

-- name: UpdateTemplateShare :one
UPDATE template_shares SET
    audience = @audience, group_id = @group_id, name = @name, description = @description,
    include_samples = @include_samples, payload = @payload, encrypted = @encrypted,
    file_count = @file_count, version = @version, status = @status, review_note = @review_note,
    updated_at = now()
WHERE id = @id
RETURNING updated_at;

-- name: SetTemplateShareReview :execrows
UPDATE template_shares SET status = @status, review_note = @review_note, file_count = @file_count, updated_at = now()
WHERE id = @id AND version = @version AND status = 'pending_review';

-- name: ListPendingTemplateShares :many
SELECT * FROM template_shares
WHERE status = 'pending_review'
ORDER BY updated_at
LIMIT @row_limit
FOR UPDATE SKIP LOCKED;

-- name: DeleteTemplateShare :execrows
DELETE FROM template_shares WHERE id = $1;

-- name: GetTemplateShareForUpdate :one
SELECT * FROM template_shares WHERE id = $1 FOR UPDATE;
