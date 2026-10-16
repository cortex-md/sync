-- name: EnqueueEmail :one
INSERT INTO email_outbox (to_address, template, locale, data) VALUES (@to_address, @template, @locale, @data)
RETURNING id, next_attempt_at, created_at;

-- name: ClaimEmail :one
UPDATE email_outbox
SET next_attempt_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int), attempts = attempts + 1
WHERE id = sqlc.arg(id) AND sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= now()
RETURNING id, to_address, template, locale, data, attempts, next_attempt_at, created_at;

-- name: ClaimDueEmails :many
UPDATE email_outbox
SET next_attempt_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int), attempts = attempts + 1
WHERE id IN (
    SELECT o.id FROM email_outbox o
    WHERE o.sent_at IS NULL AND o.failed_at IS NULL AND o.next_attempt_at <= now()
    ORDER BY o.next_attempt_at
    LIMIT sqlc.arg(row_limit)
    FOR UPDATE SKIP LOCKED
)
RETURNING id, to_address, template, locale, data, attempts, next_attempt_at, created_at;

-- name: MarkEmailSent :exec
UPDATE email_outbox SET sent_at = now(), last_error = '' WHERE id = $1;

-- name: MarkEmailRetry :exec
UPDATE email_outbox SET last_error = @last_error, next_attempt_at = @next_attempt_at WHERE id = @id;

-- name: MarkEmailDead :exec
UPDATE email_outbox SET last_error = @last_error, failed_at = now() WHERE id = @id;

-- name: DeleteFinishedEmails :execrows
DELETE FROM email_outbox WHERE id IN (
    SELECT o.id FROM email_outbox o
    WHERE (o.sent_at IS NOT NULL AND o.sent_at < sqlc.arg(sent_before)::timestamptz)
       OR (o.failed_at IS NOT NULL AND o.failed_at < sqlc.arg(dead_before)::timestamptz)
    LIMIT sqlc.arg(row_limit)
);

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
