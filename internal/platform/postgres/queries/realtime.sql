-- name: InsertCollabUpdate :one
INSERT INTO collab_updates (vault_id, file_path, data, created_at) VALUES (@vault_id, @file_path, @data, now())
RETURNING id, created_at;

-- name: BumpCollabDocument :exec
INSERT INTO collab_documents (vault_id, file_path, update_count, updated_at) VALUES (@vault_id, @file_path, @added, now())
ON CONFLICT (vault_id, file_path) DO UPDATE SET update_count = collab_documents.update_count + @added, updated_at = now();

-- name: GetCollabDocument :one
SELECT vault_id, file_path, coalesce(compacted_state, ''::bytea)::bytea AS compacted_state,
    coalesce(state_vector, ''::bytea)::bytea AS state_vector, update_count, updated_at
FROM collab_documents WHERE vault_id = @vault_id AND file_path = @file_path;

-- name: ListCollabUpdates :many
SELECT * FROM collab_updates WHERE vault_id = @vault_id AND file_path = @file_path ORDER BY id;

-- name: DeleteCollabUpdatesUpTo :execrows
DELETE FROM collab_updates WHERE vault_id = @vault_id AND file_path = @file_path AND id <= @up_to;

-- name: SaveCompactedCollabDocument :exec
INSERT INTO collab_documents (vault_id, file_path, compacted_state, state_vector, update_count, compacted_up_to, updated_at)
VALUES (@vault_id, @file_path, @compacted_state, NULL, 0, @up_to, now())
ON CONFLICT (vault_id, file_path) DO UPDATE SET
    compacted_state = EXCLUDED.compacted_state,
    compacted_up_to = EXCLUDED.compacted_up_to,
    update_count = GREATEST(collab_documents.update_count - @removed, 0),
    updated_at = now();

-- name: LockCollabCompaction :one
SELECT compacted_up_to FROM collab_documents WHERE vault_id = @vault_id AND file_path = @file_path FOR UPDATE;

-- name: DeleteCollabDocument :execrows
DELETE FROM collab_documents WHERE vault_id = @vault_id AND file_path = @file_path;

-- name: DeleteCollabUpdates :exec
DELETE FROM collab_updates WHERE vault_id = @vault_id AND file_path = @file_path;

-- name: MoveCollabDocument :exec
UPDATE collab_documents SET file_path = @new_path WHERE vault_id = @vault_id AND file_path = @old_path;

-- name: MoveCollabUpdates :exec
UPDATE collab_updates SET file_path = @new_path WHERE vault_id = @vault_id AND file_path = @old_path;

-- name: JoinPresence :exec
INSERT INTO collab_presence (client_id, vault_id, file_path, user_id, can_write, joined_at, last_seen_at)
VALUES (@client_id, @vault_id, @file_path, @user_id, @can_write, now(), now())
ON CONFLICT (client_id) DO UPDATE SET last_seen_at = now();

-- name: LeavePresence :exec
DELETE FROM collab_presence WHERE client_id = $1;

-- name: TouchPresence :exec
UPDATE collab_presence SET last_seen_at = now() WHERE client_id = ANY(@client_ids::text[]);

-- name: ListRoomPresence :many
SELECT * FROM collab_presence WHERE vault_id = @vault_id AND file_path = @file_path ORDER BY joined_at;

-- name: ListVaultPresence :many
SELECT * FROM collab_presence WHERE vault_id = $1 ORDER BY file_path, joined_at;

-- name: DeleteStalePresence :execrows
DELETE FROM collab_presence WHERE last_seen_at < $1;

-- name: InsertRelay :one
INSERT INTO realtime_relay (payload) VALUES ($1) RETURNING id;

-- name: GetRelay :one
SELECT payload FROM realtime_relay WHERE id = $1;

-- name: DeleteRelayBefore :execrows
DELETE FROM realtime_relay WHERE created_at < $1;
