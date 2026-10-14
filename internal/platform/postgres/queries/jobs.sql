-- name: EnqueueEmail :one
INSERT INTO email_outbox (to_address, template, locale, data) VALUES (@to_address, @template, @locale, @data)
RETURNING id, next_attempt_at, created_at;

-- name: ClaimDueEmails :many
SELECT id, to_address, template, locale, data, attempts, next_attempt_at, created_at
FROM email_outbox
WHERE sent_at IS NULL AND next_attempt_at <= now()
ORDER BY next_attempt_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkEmailSent :exec
UPDATE email_outbox SET sent_at = now(), attempts = attempts + 1, last_error = '' WHERE id = $1;

-- name: MarkEmailFailed :exec
UPDATE email_outbox SET attempts = attempts + 1, last_error = @last_error, next_attempt_at = @next_attempt_at WHERE id = @id;

-- name: TryAdvisoryLock :one
SELECT pg_try_advisory_xact_lock($1);

-- name: RegisterPendingBlob :exec
INSERT INTO blob_uploads_pending (blob_key) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: ResolvePendingBlob :exec
DELETE FROM blob_uploads_pending WHERE blob_key = $1;

-- name: ClaimStalePendingBlobs :many
DELETE FROM blob_uploads_pending WHERE blob_key IN (
    SELECT p.blob_key FROM blob_uploads_pending p
    WHERE p.created_at < @before
    ORDER BY p.created_at LIMIT @row_limit
    FOR UPDATE SKIP LOCKED
)
RETURNING blob_key;

-- name: EnqueueBlobDeletion :exec
INSERT INTO blob_deletions (blob_key) VALUES ($1);

-- name: ClaimDueBlobDeletions :many
UPDATE blob_deletions SET next_attempt_at = now() + interval '5 minutes'
WHERE id IN (
    SELECT d.id FROM blob_deletions d
    WHERE d.next_attempt_at <= now()
    ORDER BY d.id LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteBlobDeletion :exec
DELETE FROM blob_deletions WHERE id = $1;

-- name: RetryBlobDeletion :exec
UPDATE blob_deletions SET attempts = attempts + 1, last_error = @last_error, next_attempt_at = @next_attempt_at WHERE id = @id;
