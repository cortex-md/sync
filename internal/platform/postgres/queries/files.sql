-- name: UpsertFileLatest :exec
INSERT INTO file_latest (vault_id, file_path, current_version, latest_snapshot_version, checksum, size_bytes, content_type,
    deleted, last_modified_by, last_device_id, created_by, deltas_since_snapshot, delta_bytes_since_snapshot, updated_at, created_at)
VALUES (@vault_id, @file_path, @current_version, @latest_snapshot_version, @checksum, @size_bytes, @content_type,
    @deleted, @last_modified_by, @last_device_id, @created_by, @deltas_since_snapshot, @delta_bytes_since_snapshot, @updated_at, @created_at)
ON CONFLICT (vault_id, file_path) DO UPDATE SET
    current_version = EXCLUDED.current_version,
    latest_snapshot_version = EXCLUDED.latest_snapshot_version,
    checksum = EXCLUDED.checksum,
    size_bytes = EXCLUDED.size_bytes,
    content_type = EXCLUDED.content_type,
    deleted = EXCLUDED.deleted,
    last_modified_by = EXCLUDED.last_modified_by,
    last_device_id = EXCLUDED.last_device_id,
    created_by = EXCLUDED.created_by,
    deltas_since_snapshot = EXCLUDED.deltas_since_snapshot,
    delta_bytes_since_snapshot = EXCLUDED.delta_bytes_since_snapshot,
    updated_at = EXCLUDED.updated_at;

-- name: GetFileLatest :one
SELECT * FROM file_latest WHERE vault_id = @vault_id AND file_path = @file_path;

-- name: GetFileLatestForUpdate :one
SELECT * FROM file_latest WHERE vault_id = @vault_id AND file_path = @file_path FOR UPDATE;

-- name: ListFileLatestByPaths :many
SELECT * FROM file_latest WHERE vault_id = @vault_id AND file_path = ANY(@paths::text[]);

-- name: ListFiles :many
SELECT * FROM file_latest
WHERE vault_id = @vault_id
  AND (@include_deleted::bool OR deleted = false)
  AND file_path > @after_path::text
  AND (@all_paths::bool OR file_path = ANY(@exact_paths::text[]) OR file_path LIKE ANY(@path_patterns::text[]))
ORDER BY file_path
LIMIT @row_limit;

-- name: DeleteFileLatest :execrows
DELETE FROM file_latest WHERE vault_id = @vault_id AND file_path = @file_path;

-- name: CreateSnapshot :exec
INSERT INTO file_snapshots (id, vault_id, file_path, version, encrypted_blob_key, size_bytes, checksum, created_by, device_id, created_at)
VALUES (@id, @vault_id, @file_path, @version, @encrypted_blob_key, @size_bytes, @checksum, @created_by, @device_id, @created_at);

-- name: GetSnapshot :one
SELECT * FROM file_snapshots WHERE id = $1;

-- name: GetLatestSnapshot :one
SELECT * FROM file_snapshots WHERE vault_id = @vault_id AND file_path = @file_path ORDER BY version DESC LIMIT 1;

-- name: GetSnapshotAtOrBefore :one
SELECT * FROM file_snapshots WHERE vault_id = @vault_id AND file_path = @file_path AND version <= @version
ORDER BY version DESC LIMIT 1;

-- name: GetSnapshotByVersion :one
SELECT * FROM file_snapshots WHERE vault_id = @vault_id AND file_path = @file_path AND version = @version;

-- name: ListSnapshotsByPath :many
SELECT * FROM file_snapshots WHERE vault_id = @vault_id AND file_path = @file_path ORDER BY version;

-- name: ListLatestSnapshotsByPaths :many
SELECT DISTINCT ON (file_path) * FROM file_snapshots
WHERE vault_id = @vault_id AND file_path = ANY(@paths::text[])
ORDER BY file_path, version DESC;

-- name: ListSnapshotsAtOrBefore :many
SELECT (wanted.idx - 1)::int AS request_index, s.id, s.vault_id, s.file_path, s.version, s.encrypted_blob_key,
    s.size_bytes, s.checksum, s.created_by, s.device_id, s.created_at
FROM unnest(@paths::text[]) WITH ORDINALITY AS wanted(file_path, idx)
JOIN LATERAL (
    SELECT * FROM file_snapshots fs
    WHERE fs.vault_id = @vault_id AND fs.file_path = wanted.file_path
      AND fs.version <= (@versions::int[])[wanted.idx]
    ORDER BY fs.version DESC LIMIT 1
) s ON true;

-- name: ListSnapshotsByIDs :many
SELECT * FROM file_snapshots WHERE vault_id = @vault_id AND id = ANY(@ids::uuid[]);

-- name: ListHistory :many
SELECT * FROM (
    SELECT s.id, s.vault_id, s.file_path, s.version, s.encrypted_blob_key, s.size_bytes, s.checksum,
        s.created_by, s.device_id, s.created_at,
        coalesce(u.display_name, '')::text AS author_name,
        coalesce(u.avatar_key, '')::text AS author_avatar_key,
        coalesce(d.device_name, '')::text AS device_name
    FROM file_snapshots s
    LEFT JOIN users u ON u.id = s.created_by
    LEFT JOIN devices d ON d.id = s.device_id
    WHERE s.vault_id = @vault_id AND s.file_path = @file_path
    ORDER BY s.version DESC
    LIMIT @row_limit
) h ORDER BY h.version;

-- name: DeleteOlderSnapshots :many
DELETE FROM file_snapshots AS target
WHERE target.vault_id = @vault_id AND target.file_path = @file_path AND target.id NOT IN (
    SELECT kept.id FROM file_snapshots AS kept WHERE kept.vault_id = @vault_id AND kept.file_path = @file_path
    ORDER BY kept.version DESC LIMIT @keep_count
)
RETURNING target.*;

-- name: DeleteSnapshotsByPath :many
DELETE FROM file_snapshots WHERE vault_id = @vault_id AND file_path = @file_path RETURNING *;

-- name: MoveSnapshots :exec
UPDATE file_snapshots SET file_path = @new_path WHERE vault_id = @vault_id AND file_path = @old_path;

-- name: ListBlobKeysByVault :many
SELECT encrypted_blob_key FROM file_snapshots WHERE vault_id = $1;

-- name: CreateDelta :exec
INSERT INTO file_deltas (id, vault_id, file_path, base_version, target_version, encrypted_delta, size_bytes, created_by, device_id, created_at)
VALUES (@id, @vault_id, @file_path, @base_version, @target_version, @encrypted_delta, @size_bytes, @created_by, @device_id, @created_at);

-- name: ListDeltas :many
SELECT * FROM file_deltas
WHERE vault_id = @vault_id AND file_path = @file_path AND base_version >= @since_version
ORDER BY base_version
LIMIT @row_limit;

-- name: DeleteDeltasBefore :one
WITH deleted AS (
    DELETE FROM file_deltas
    WHERE vault_id = @vault_id AND file_path = @file_path AND target_version < @before_version
    RETURNING size_bytes
)
SELECT coalesce(sum(size_bytes), 0)::bigint FROM deleted;

-- name: MoveDeltas :exec
UPDATE file_deltas SET file_path = @new_path WHERE vault_id = @vault_id AND file_path = @old_path;

-- name: CreateSyncEvent :one
INSERT INTO sync_events (vault_id, event_type, file_path, version, actor_id, device_id, metadata, created_at)
VALUES (@vault_id, @event_type, @file_path, @version, @actor_id, @device_id, @metadata, @created_at)
RETURNING id;

-- name: GetSyncEvent :one
SELECT * FROM sync_events WHERE id = $1;

-- name: ListSyncEvents :many
SELECT * FROM sync_events
WHERE vault_id = @vault_id AND id > @since_id
  AND (@all_paths::bool
       OR file_path = ANY(@exact_paths::text[]) OR file_path LIKE ANY(@path_patterns::text[])
       OR coalesce(metadata->>'old_path', '') = ANY(@exact_paths::text[])
       OR coalesce(metadata->>'old_path', '') LIKE ANY(@path_patterns::text[]))
ORDER BY id
LIMIT @row_limit;

-- name: LatestSyncEventID :one
SELECT coalesce(max(id), 0)::bigint FROM sync_events WHERE vault_id = $1;

-- name: DeleteSyncEventsBefore :one
WITH deleted AS (
    DELETE FROM sync_events WHERE id IN (
        SELECT e.id FROM sync_events e WHERE e.created_at < sqlc.arg(before)::timestamptz ORDER BY e.created_at LIMIT sqlc.arg(row_limit)
    )
    RETURNING id
)
SELECT count(*)::bigint AS deleted, coalesce(max(id), 0)::bigint AS max_id FROM deleted;

-- name: AdvanceSyncEventWatermark :exec
INSERT INTO sync_event_watermark (id, pruned_through) VALUES (1, @pruned_through)
ON CONFLICT (id) DO UPDATE SET pruned_through = GREATEST(sync_event_watermark.pruned_through, EXCLUDED.pruned_through);

-- name: GetSyncEventWatermark :one
SELECT coalesce((SELECT w.pruned_through FROM sync_event_watermark w WHERE w.id = 1), 0)::bigint;

-- name: ListSyncEventsAfter :many
SELECT * FROM sync_events WHERE id > @after_id ORDER BY id LIMIT @row_limit;
