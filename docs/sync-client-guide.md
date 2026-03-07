# Cortex Sync — Client Implementation Guide

This document describes the **business rules** of the Cortex sync system, targeting AI agents and developers implementing the sync client (Rust engine in `src-tauri/src/sync/`). This is not an endpoint reference — see `docs/api.md` for that. This document answers "what to do, when, and why."

---

## 1. Mental Model

The server is **opaque encrypted blob storage**. It never sees plaintext file content. The client is responsible for all merge logic, conflict detection, and encryption. The server only:

- Stores file versions and metadata.
- Notifies devices about changes via SSE.
- Retains version history.
- Serializes conflicts via versioning (rejects uploads with a wrong `base_version` with 409).

There are two distinct layers:

- **Layer 1 — File sync**: snapshot and delta uploads/downloads, SSE events, E2EE. This is the durable persistence layer.
- **Layer 2 — Real-time collab**: Yjs CRDT over WebSocket for simultaneous co-editing. E2EE is relaxed during active sessions; the final state is encrypted and persisted through Layer 1 when the session ends.

---

## 2. Authentication and Identity

### Device ID

Each device has a persistent UUID generated on first run and stored in the OS keychain. This UUID is the `device_id` and must be sent on **every authenticated request** via the `X-Device-ID` header.

The device is registered implicitly on login (`POST /auth/v1/login`) with `device_id`, `device_name`, and `device_type`.

### Tokens

- **Access token**: JWT, 15-minute lifetime. Sent as `Authorization: Bearer <token>`.
- **Refresh token**: Opaque, valid for 90 days. Rotated on every use — each use issues a new pair. Reuse detection revokes the entire token family (theft protection).

**Rule:** the sync engine must renew the access token **proactively** before it expires, not reactively after receiving a 401. On receiving a 401, attempt refresh once. If the refresh also fails with `token_revoked` or `token_reuse`, emit a `SyncAuthRequired` event to the frontend — the user must log in again.

---

## 3. Sync Cursor per Device

The server maintains a `last_sync_event_id` per device. After each sync session, the client must persist the ID of the last SSE event received and update it on the server:

```
PUT /devices/v1/{deviceID}/sync-cursor
{ "last_sync_event_id": 4821 }
```

This allows the client to know exactly where to resume on reconnect. Use the `Last-Event-ID` header in SSE for replay, and update the cursor on the server **after confirming** that the events were successfully processed (not before).

---

## 4. File Upload Flow

### When to upload

The file watcher detects a local change → waits for a debounce of **5 seconds** after the last modification → verifies the Note Cache has flushed to disk (`dirty === false`) → enqueues upload.

The debounce prevents uploads of intermediate states during active editing. Without it, an actively-edited note would generate dozens of uploads per minute.

### Hash-based deduplication

Before any upload, compute the blake3 hash of the local plaintext content. If the hash equals the `remote_hash` recorded in the local `sync.db`, **there is nothing to do** — the file is already in sync.

If the hash differs, proceed with the upload. The server also deduplicates on its side: if the `X-Local-Hash` sent matches the checksum of the current stored version, the server returns the existing version without creating a new entry. This behavior is transparent to the client.

### How to upload

```
POST /sync/v1/vaults/{vaultID}/files
Content-Type: application/octet-stream
X-File-Path: notes/diary.md
X-Local-Hash: <blake3 of plaintext>
X-Content-Type: text/markdown
Body: <encrypted content: IV || ciphertext || auth_tag>
```

The body is the **encrypted** file content (see section 8). `X-Local-Hash` is the hash of the **plaintext**, computed before encryption — this allows hash comparison on the server without exposing the content.

### After a successful upload

Update local `sync.db`:
- `ancestor_hash = local_hash`
- `remote_hash = local_hash`
- `sync_status = 'synced'`
- `server_version_id = <snapshot_id from the response>`

---

## 5. Delta-First Strategy

For frequently-changing files (notes under active editing), the server supports small delta uploads instead of full snapshots. This drastically reduces bandwidth usage and storage cost.

### When to use delta vs. snapshot

- **Delta**: for incremental changes to text files (`.md`, `.json`). The client computes the diff from `ancestor` to the current version and sends the encrypted delta.
- **Snapshot**: for the first version of a file, after the server signals `needs_snapshot: true`, and for binary files (images, PDFs).

### Delta upload

```
POST /sync/v1/vaults/{vaultID}/files/deltas
{
  "file_path": "notes/diary.md",
  "base_version": 3,
  "checksum": "<hash of the resulting plaintext after applying the delta>",
  "size_bytes": 412,
  "encrypted_data": "<base64 of encrypted delta>"
}
```

`base_version` must be the current version of the file on the server. If it is stale, the server returns **409 Conflict** — the client must download the latest version, perform a local merge, and retry.

### `needs_snapshot` signaling

When the delta upload response contains `"needs_snapshot": true`, the server is signaling that the reconstruction cost via deltas is becoming high. The client must upload a full snapshot at the next opportunity. This is triggered by:

- More than 10 consecutive deltas since the last snapshot (configurable via `CORTEX_SYNC_MAX_DELTAS_BEFORE_SNAPSHOT`).
- Accumulated delta size exceeds 50% of the last snapshot size (configurable via `CORTEX_SYNC_MAX_DELTA_SIZE_RATIO`).

When the snapshot is uploaded, the server automatically cleans up old deltas preceding the new version.

---

## 6. Download Flow

### Trigger: SSE event

On receiving a `file_created`, `file_updated`, or `file_renamed` event via SSE:

1. Check `sync_status` of the file in local `sync.db`:
   - `'synced'` or `'remote_ahead'`: no local conflict. Download directly.
   - `'local_ahead'`: possible conflict. Compare hashes (see section 7).
   - `'conflict'`: conflict already detected. Execute the configured resolution strategy.

2. To download:
```
GET /sync/v1/vaults/{vaultID}/files?path=notes/diary.md
```
The response body is the encrypted blob. Decrypt and write to disk.

3. After a successful conflict-free download:
   - `ancestor_hash = remote_hash = <hash of the decrypted content>`
   - `sync_status = 'synced'`

### File rename/move

The `file_renamed` event contains `file_path` (new path) and `old_path`. The client must:

1. Move the file locally from `old_path` to `file_path`.
2. Update the local `sync.db` (rename the entry).
3. Do **not** download the content — the content has not changed, only the path.

This is critical for performance: moving a file to a different folder must **not** re-download its content. The server stores renames as a first-class operation, preserving the full version history under the new path.

---

## 7. Conflict Detection and Resolution

### When there is a conflict

A conflict occurs when **both** sides have changed since the last common sync:
- `local_hash !== ancestor_hash` (file edited locally)
- `remote_hash !== ancestor_hash` (file edited remotely)

If only one side changed, it is a fast-forward — no conflict:
- Only local changed: upload directly.
- Only remote changed: download directly.

### Resolution strategies by file type

**Markdown files (`.md`):**
Three-way merge via `diff-match-patch` with an explicit ancestor:
1. Download the remote version and decrypt.
2. Retrieve the ancestor (from the `server_version_id` stored in `sync.db`, via `GET /files?path=...&version=<n>`).
3. Run three-way merge: `merge(ancestor, local, remote)`.
4. Result `Clean`: upload the merged result.
5. Result `WithConflicts`: insert inline conflict markers and save to disk. Emit `SyncConflict` event to the frontend. The file is set to `sync_status = 'conflict'`.

**Binary files (images, PDFs, attachments):**
Last-modified-wins: the file with the more recent `mtime` wins. No merge attempt.

**JSON config files (`.cortex/*.json`):**
Object merge: key by key, `updatedAt` as tiebreaker. Keys present on only one side are preserved (union). Never discard keys.

**`workspace.json`:** never sync. It is device-specific.

### Pre-merge snapshot

Before any merge operation that might overwrite local content, create a local snapshot of the file (`trigger: 'pre-sync'`) via the Note Cache. This guarantees no local edit is ever lost.

---

## 8. E2E Encryption

The server **never** sees plaintext content. All encryption happens on the client.

### Vault encryption key (VEK)

Generated randomly on sync activation. Stored in the OS keychain. Never sent to the server in plaintext.

The server stores the encrypted VEK (EVEK) derived from a user-supplied password, to allow recovery on a new device.

### Encrypted blob format

```
IV (12 bytes) || ciphertext || auth_tag (16 bytes)
```

- Algorithm: **AES-256-GCM**.
- IV: randomly generated for each file version (never reuse).
- If the `auth_tag` fails on decryption: the file is corrupted or tampered with — do not write to disk, report an error.

### Hash sent to the server

The `X-Local-Hash` sent on upload is always the hash of the **plaintext**, computed before encryption. This allows the server to detect duplicates and perform version comparisons without seeing the content.

### E2EE and collab sessions

E2EE is relaxed during active collab sessions. Yjs CRDT updates travel as plaintext over the WebSocket connection. When the last peer disconnects, the client sends a compacted `Y.Doc` state (the `compact` message type), and the server stores it. The next file sync snapshot upload from any client will re-encrypt and persist the final state in the durable file sync layer.

**Rule:** when a `collab_inactive` SSE event is received for a file, the local sync engine should schedule a snapshot upload of that file to ensure the latest collab state is durably persisted under E2EE.

---

## 9. Real-time Collaboration (Collab)

The collab layer enables multiple users to edit the same file simultaneously. It uses **Yjs** CRDTs over WebSocket. The server is a relay — it does not interpret CRDT internals.

### Connecting to a collab session

```
GET /sync/v1/vaults/{vaultID}/collab?path=notes/diary.md&token=<access_token>
Upgrade: websocket
```

Authentication is via the `?token=` query parameter because the browser WebSocket API does not support custom headers. The token is the same JWT access token used for regular HTTP requests.

The connection is rejected before the WebSocket upgrade with:
- `401` if the token is missing or invalid.
- `403` if the user is not a member of the vault.
- `1008 Policy Violation` close frame if the room is full (`CORTEX_COLLAB_MAX_PEERS_PER_ROOM`, default 10).

### Message protocol

All messages are JSON text frames:

```json
{
  "type": "sync_step1 | sync_step2 | update | awareness | compact | ping | pong",
  "data": "<base64-encoded bytes>"
}
```

`data` is omitted for `ping` and `pong`.

| Type | Direction | Description |
|------|-----------|-------------|
| `sync_step1` | client → server | Yjs sync state vector — writers only |
| `sync_step2` | server → client | Full Y.Doc state sent on connect |
| `update` | bidirectional | Incremental Yjs CRDT update — writers only |
| `awareness` | bidirectional | Cursor/presence data, relayed to all peers |
| `compact` | client → server | Full compacted Y.Doc state for persistence |
| `ping` | server → client | Keepalive every 30s |
| `pong` | client → server | Response to ping |

### Connection lifecycle

1. **On connect**: the server sends the existing compacted state as `sync_step2`, followed by any buffered incremental updates as `update` messages. The client applies them to its local `Y.Doc`.
2. **During session**: send local Yjs updates as `update` messages. Receive and apply `update` messages from other peers.
3. **On disconnect (last peer)**: before closing, send a `compact` message containing the full serialized `Y.Doc` state. This triggers server-side persistence: the server discards incremental updates and stores the compacted state.

### Read-only enforcement

Viewers (role `viewer`) can connect and receive updates but their `update`, `sync_step1`, and `sync_step2` messages are silently dropped by the server. The client should still disable editing in the UI for viewer-role connections.

### Awareness (cursors and presence)

Send cursor position and user identity as `awareness` messages. These are relayed to all other peers in the room without persistence — they are ephemeral by design. Format the `data` field according to the Yjs awareness protocol.

### SSE events for collab

The file sync SSE stream emits collab lifecycle events:

- `collab_active`: published when the first peer joins a room. Data: `{"file_path":"notes/diary.md"}`. Use this to show a "live editing" indicator in the UI.
- `collab_inactive`: published when the last peer leaves a room. Data: `{"file_path":"notes/diary.md"}`. Use this to hide the indicator and trigger a snapshot upload (see section 8).

### Presence REST endpoint

To query who is currently in a room without connecting via WebSocket:

```
GET /sync/v1/vaults/{vaultID}/collab/peers?path=notes/diary.md
Authorization: Bearer <token>
X-Device-ID: <uuid>
```

Response:
```json
{
  "vault_id": "uuid",
  "file_path": "notes/diary.md",
  "peer_count": 2,
  "peer_ids": ["uuid", "uuid"]
}
```

### Collab and file sync interaction

The two layers are complementary, not mutually exclusive. A file can be in an active collab session and also have its snapshot synced to other devices via Layer 1. The key rules are:

- During an active collab session (`collab_active`), suppress the 5-second debounce upload for that file — changes are flowing through the collab channel.
- On `collab_inactive`, re-enable normal file sync behavior and schedule an immediate snapshot upload.
- The server does **not** run compaction itself — the client is responsible for sending `compact` before disconnecting. If a client crashes without sending `compact`, the next client to connect will receive the incremental updates buffered in the database and must compact them itself before or after the session.

---

## 10. Initial Sync on a New Device

When the vault does not yet exist locally or this is the first sync activation:

1. **Fetch metadata for all files** via bulk:
```
POST /sync/v1/vaults/{vaultID}/files/bulk
{ "paths": [] }
```
For large vaults, use `GET /sync/v1/vaults/{vaultID}/files/list` to get the full list first, then bulk in batches of up to 500 paths.

2. **Compare with local state** (if the vault already exists on disk but has never been synced):
   - Local hash != remote hash: enqueue verification (upload or download based on mtime).
   - File absent locally: enqueue download.
   - Local file absent on remote: enqueue upload.

3. **Prioritize downloads** over uploads on initial sync: the user wants to see the latest content as soon as possible.

4. **Run with concurrency of 3** simultaneous operations (without saturating the network).

5. After completing: emit `SyncInitialComplete` to the frontend.

---

## 11. SSE — Real-time Connection

### Connect

```
GET /sync/v1/vaults/{vaultID}/events
Authorization: Bearer <token>
X-Device-ID: <uuid>
Last-Event-ID: <last_sync_event_id from local cursor>
```

`Last-Event-ID` triggers server-side replay: all events missed since that ID are sent before live streaming begins.

### Reconnect

On disconnect, reconnect with exponential backoff: 1s, 2s, 4s, 8s, cap at 60s. Always include the `Last-Event-ID` of the last **successfully processed** event (not just the last received — only after processing).

If SSE fails repeatedly (e.g., 5 attempts), fall back to polling:
```
GET /sync/v1/vaults/{vaultID}/changes?since=<last_sync_event_id>&limit=100
```
Repeat every 30 seconds. Return to SSE when the connection is re-established.

### Ignore events from the current device

SSE events include `device_id`. Ignore events where `device_id == local_device_id` — they are reflections of local uploads already processed.

### SSE event types

| Event | Trigger |
|-------|---------|
| `file_created` | A new file was uploaded to the vault |
| `file_updated` | An existing file was uploaded (new version) or a delta was applied |
| `file_deleted` | A file was deleted |
| `file_renamed` | A file was moved/renamed |
| `collab_active` | First peer joined a collab room for a file |
| `collab_inactive` | Last peer left a collab room for a file |
| `ping` | Keepalive every 30s (no action needed) |

---

## 12. Operation Queue and Priorities

The engine maintains a priority queue persisted in local SQLite. Priorities:

| Priority | Operation type |
|----------|---------------|
| 100 | Conflict download that the user requested to resolve |
| 80 | Download via SSE (remote file modified by another device) |
| 60 | Upload of locally-modified file |
| 40 | Upload of config file (`.cortex/*.json`) |
| 20 | Download during initial sync |
| 10 | Retry of a failed operation |

Maximum of **3 concurrent operations**. Downloads and uploads can run in parallel with each other.

### Retry backoff

| Attempt | Wait |
|---------|------|
| 1st failure | 30s |
| 2nd failure | 2min |
| 3rd failure | 10min |
| 4th failure | 30min |
| 5th failure | 2h |
| 6th+ | 6h (ceiling) |

After 10 failures: mark as `failed_permanent`, notify frontend.

### Errors that do not retry

- **401**: attempt token refresh first. If that also fails: `SyncAuthRequired`.
- **403**: vault not authorized — permanent error.
- **413**: file exceeds server limit — permanent error. Default limit is 100MB (configurable via `CORTEX_SYNC_MAX_FILE_SIZE`). Notify the user.
- **409 Conflict** on delta upload: not an error — this is a signal to download the latest version and retry with a merge.

---

## 13. Server Limits

| Limit | Default | Config |
|-------|---------|--------|
| Max file size | 100MB | `CORTEX_SYNC_MAX_FILE_SIZE` |
| Versions retained per file | 50 | `CORTEX_SYNC_MAX_SNAPSHOTS_PER_FILE` |
| Deltas before requesting snapshot | 10 | `CORTEX_SYNC_MAX_DELTAS_BEFORE_SNAPSHOT` |
| Delta size ratio | 0.5 (50% of snapshot) | `CORTEX_SYNC_MAX_DELTA_SIZE_RATIO` |
| Sync event retention | 30 days | `CORTEX_SYNC_EVENT_RETENTION` |
| Max peers per collab room | 10 | `CORTEX_COLLAB_MAX_PEERS_PER_ROOM` |
| Collab flush interval | 10s | `CORTEX_COLLAB_FLUSH_INTERVAL` |

The client does **not** need to know version or event retention limits — the server manages them silently. The client must know and respect the file size limit (never attempt to upload a file larger than 100MB) and the collab room capacity (show a "room full" error to the user when the WebSocket is closed with `1008`).

---

## 14. Reconnection After a Long Offline Period

If the device was offline for longer than the event retention window (default 30 days), the `Last-Event-ID` may not return all events via SSE replay. In that case:

1. Connect to SSE normally with the cursor's `Last-Event-ID`.
2. In parallel, run a full **reconciliation**: `GET /files/list` to fetch the current state of all files in the vault.
3. Compare the remote manifest with local `sync.db` and determine:
   - Files absent locally: enqueue download.
   - Local files that are more recent: enqueue upload.
   - Files with changes on both sides: enqueue conflict resolution.
4. SSE continues running normally to capture real-time changes while reconciliation is in progress.

---

## 15. What Never to Sync

- `workspace.json` — tab and pane state is device-specific.
- `vault-id.json` — vault UUID is immutable, cannot be overwritten by sync.
- `sync.db` — the engine's internal database, never a syncable file.
- Local file recovery snapshots (`.cortex/snapshots/`) — local backups by design.
- Plugin data that declares `syncable: false`.

---

## 16. Version History

The server retains up to 50 versions per file (default). The client can:

- List versions: `GET /files/history?path=notes/diary.md`
- Download a specific version: `GET /files?path=notes/diary.md&version=3`

When restoring an old version: create a local snapshot of the current state before overwriting, then upload the restored version as a new version (not as a silent rollback).
