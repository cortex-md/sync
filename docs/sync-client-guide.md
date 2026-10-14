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
- **Layer 2 — Real-time collab**: Yjs CRDT over WebSocket for simultaneous co-editing. Every payload is end-to-end encrypted with a key derived from the VEK; the file itself is still persisted through Layer 1.

---

## 2. Authentication and Identity

### Device ID

Each device has a persistent UUID generated on first run and stored in the OS keychain. This UUID is the `device_id` and must be sent on **every authenticated request** via the `X-Device-ID` header.

The device is registered implicitly on login (`POST /auth/v1/login`) with `device_id`, `device_name`, and `device_type`.

### Tokens

- **Access token**: JWT, 15-minute lifetime. Sent as `Authorization: Bearer <token>`.
- **Refresh token**: Opaque, valid for 90 days. Rotated on every use — each use issues a new pair. Reuse detection revokes the entire token family (theft protection).

### Password and identity keys

The account password never leaves the client. Before login or register, call
`POST /auth/v1/prelogin {email}`:

- `kdf_version: 1`: derive `master = Argon2id(password, kdf_salt, 65536 KiB, 3 iterations,
  parallelism 1, 32 bytes)`, then `authKey = HKDF-SHA256(master, info "cortex/auth/v1")` and
  `wrapKey = HKDF-SHA256(master, info "cortex/wrap/v1")`. Send `authKey` (base64url, no padding) as
  `password`. Unwrap `encrypted_private_key` from the login response with `wrapKey`.
- `kdf_version: 0` (legacy account): send the raw password, then immediately call
  `PUT /account/v1/me/keys` with a fresh salt, X25519 identity, wrapped private key and recovery wrap,
  and show the recovery key once.

Register new accounts with `kdf_salt`, `public_key`, `encrypted_private_key` and
`recovery_wrapped_private_key`. Keep the identity private key only in the OS secret store and clear
it on logout. A password change sends a new salt and the re-wrapped private key; a password reset
(`cortex://reset-password?token=&server=` deep link) calls `POST /account/v1/password/reset/prepare`
to get the recovery wrap, unwraps it with the recovery key and resets with the new key fields. If no
recovery key is available, generate a new identity: the server drops the user's vault keys and
emits `member_key_changed` so admins grant them again.

**Rule:** the sync engine must renew the access token **proactively** before it expires, not reactively after receiving a 401. On receiving a 401, attempt refresh once. If the refresh also fails with `token_revoked` or `token_reuse`, emit a `SyncAuthRequired` event to the frontend — the user must log in again.

---

## 3. Sync Cursor per Vault

The client keeps its event cursor locally and reports it per vault so other screens can show when
each device last synced:

```
PUT /sync/v1/vaults/{vaultID}/cursor
{ "last_event_id": 4821 }
```

Seed the cursor from the `X-Latest-Event-ID` header of `GET /files/list` after a full sync. Use the
`Last-Event-ID` header in SSE for replay, and advance the cursor **after confirming** that the
events were successfully processed (not before).

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

Generated randomly when the vault is created. Stored in the OS keychain. Never sent to the server in
plaintext.

Each member gets the VEK sealed to their X25519 public key (`vault_keys`, opaque to the server):
`eph = X25519 keypair`, `k = HKDF-SHA256(X25519(eph, recipientPub), salt = eph.pub || recipientPub,
info "cortex/vek-seal/v1")`, blob = `0x01 || eph.pub || AES-256-GCM(k)`.

- On vault creation, seal the VEK to your own public key and `PUT /vaults/v1/{id}/keys/me`.
- On a new device, `GET /vaults/v1/{id}/keys/me` and unseal it. Without a key, wait for an admin
  (`key_granted`); the legacy password-wrapped VEK (`GET /encryption`, `has_key`) is still accepted.
- Admins holding the VEK list `GET /vaults/v1/{id}/keys/pending` on start and on `member_joined`,
  `member_key_changed` and `access_changed`, seal the VEK to each pending member's public key and
  grant it. Pin each member's key fingerprint on first sight and never auto-grant to a changed key.

### Path access

Membership gives every member the VEK; isolation between members is enforced by the server through
path access rules (see `docs/api.md#path-access`). `GET /vaults/v1/{id}/overview` returns
`my_access` (`all`, `base`, `rules[{path, level}]`); the effective level of a path is the level of
the deepest rule covering it, else `base`. On the `access_changed` SSE event, refetch the overview,
recompute access and run a full sync:

- Files that became unreadable: delete the local copy if it is tracked and unmodified, otherwise keep
  it and log.
- Paths without `edit`: never upload. Restore server content over local divergence and log
  "Discarded local changes to read-only X"; new local files in read-only folders stay local.

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

Collab is always end-to-end encrypted. Every binary payload a client sends (`update`, `awareness`,
`sync_step1`, `sync_step2` and the `compact` state) is a sealed envelope:

```
[0x01][nonce 12 bytes][AES-256-GCM ciphertext || tag 16 bytes]
```

- Key: `HKDF-SHA256(VEK, info "cortex/collab/v1")`, 32 bytes, derived once per vault.
- Nonce: random per envelope, never reused.
- AAD: `utf8("cortex/collab/v1:" + vaultId)`. It does not bind the opcode or the path: the server
  replays a stored `sync_step2` as compacted state and moves documents when a note is renamed.
- A payload that fails to open (bad version byte, bad tag) is dropped, never applied.

The server stores and relays envelopes as opaque bytes and never reads them. Compacted state and
persisted updates in the database are ciphertext.

---

## 9. Real-time Collaboration (Collab)

The collab layer enables multiple users to edit the same file simultaneously. It uses **Yjs** CRDTs over WebSocket. The server is an opaque relay: it does not interpret CRDT internals and only sees encrypted envelopes (section 8).

### Connecting to a collab session

First request a short-lived ticket over the authenticated HTTP API:

```http
POST /sync/v1/vaults/{vaultID}/collab/tickets
Authorization: Bearer <access_token>
X-Device-ID: <device_uuid>
Content-Type: application/json

{"file_path":"notes/diary.md"}
```

Then use that ticket for the WebSocket upgrade:

```
GET /sync/v1/vaults/{vaultID}/collab?path=notes/diary.md&ticket=<collab_ticket>[&frames=binary]
Upgrade: websocket
```

The ticket is HMAC-signed and bound to the user, vault and path. It is valid for 60 seconds on any
instance and can be reused within that window; request a new one for every reconnect attempt. Never
put the JWT access token in the WebSocket URL.

The connection is rejected before the WebSocket upgrade with:
- `400` if `path` or `vaultID` is missing or invalid.
- `401` if the ticket is missing, invalid or expired.
- `403` if the user is not a member of the vault or has no `view` access to the path.
- `503` if the instance is draining.

After the upgrade the server closes the socket with:

| Code | Meaning | Client action |
|------|---------|---------------|
| `1008` | Room full (`CORTEX_COLLAB_MAX_PEERS_PER_ROOM`, default 10) | Fall back to file sync, show "Live session full" |
| `1001` | Server shutting down | Reconnect with backoff |
| `1013` | Update buffer full | Reconnect with backoff |
| `4402` | Subscription entitlement lapsed | Stop collab for the vault |
| `4403` | Access changed (role, rules, removal) | Reconnect; a new ticket returns `403` when access was lost, otherwise `welcome.from.can_write` carries the new mode |
| `4408` | Client too slow to drain frames | Reconnect and resync |

### Message protocol

JSON text frames by default; `data` is the base64 envelope. With `&frames=binary`, `sync_step1`,
`sync_step2`, `update` and `awareness` travel as binary messages (client → server
`[opcode][payload]`, server → client `[opcode][client_id len][client_id][payload]`, opcodes 1–4;
see `docs/api.md`). Control messages are always JSON text.

| Type | Direction | Description |
|------|-----------|-------------|
| `welcome` | server → client | First frame: own `client_id`, `from` (own identity with `can_write`), `peers` and `up_to` |
| `sync_step2` | server → client | Compacted document state sent on join |
| `update` | bidirectional | Incremental Yjs update; dropped by the server when sent by a viewer |
| `synced` | server → client | Last frame of the initial backlog |
| `sync_step1` | bidirectional | Yjs state vector, relayed to peers (allowed for viewers) |
| `awareness` | bidirectional | Cursor/presence data, relayed to all peers, never persisted |
| `compact` | client → server | Compacted state plus `up_to`; writers only |
| `peer_joined` / `peer_left` | server → client | Presence changes (`peer` / `client_id`) |
| `ping` / `pong` | server ↔ client | Keepalive every 30s |

Every relayed JSON frame carries a server-stamped `from`; binary frames carry the sender's
`client_id`. Use them, never identity fields inside the payload.

### Connection lifecycle

1. **Join**: the server sends `welcome`, then the compacted state as `sync_step2` (when one exists),
   then persisted `update`s, then updates still buffered on that instance, then `{"type":"synced"}`.
   Apply everything before `synced` as remote state; only after `synced` decide how to reconcile with
   the local file (seed an empty document, let the disk win when alone, let the room win when peers
   are present) and send your own `sync_step1`.
2. **Answering `sync_step1`**: only one elected responder per room answers a peer's `sync_step1`,
   and it answers with an `update` containing the missing state. Clients never send `sync_step2`:
   the server would persist and rebroadcast it to every peer.
3. **During the session**: batch local Yjs updates (for example every 40 ms) and send them as
   `update`. Apply incoming `update`s with a remote origin.
4. **Compaction**: a writer sends `compact` with the encrypted full state and the `up_to` received
   in `welcome`. The server flushes buffered updates, deletes persisted updates with id <= `up_to`
   and stores the state. Updates that arrived after `welcome` survive. The server never compacts on
   its own; if a client crashes without compacting, the next one receives the update log and can
   compact it.

### Read-only enforcement

Viewers (and paths without `edit` access) can connect, receive updates and send `sync_step1` and
`awareness`. Their `update`, `sync_step2` and `compact` messages are silently dropped. Disable editing
in the UI when `welcome.from.can_write` is `false`.

### Awareness (cursors and presence)

Send cursor position and user state as encrypted `awareness` messages following the Yjs awareness
protocol. They are relayed to all other peers in the room and never persisted.

### SSE events for collab

The file sync SSE stream emits collab lifecycle events:

- `collab_active`: published when the first peer joins a room. Data: `{"file_path":"notes/diary.md"}`. Use this to show a "live editing" indicator in the UI.
- `collab_inactive`: published when the last peer leaves a room. Data: `{"file_path":"notes/diary.md"}`. Use this to hide the indicator.

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
  "peer_ids": ["uuid", "uuid"],
  "peers": [
    { "client_id": "uuid", "user_id": "uuid", "display_name": "Ana", "avatar_url": "/avatars/v1/...", "can_write": true }
  ]
}
```

### Collab and file sync interaction

The collab room carries live edits; file sync (Layer 1) stays the durable, encrypted store. While a
room is live for a path, file sync for that path follows a leader/follower model:

- One writer in the room is elected the **leader** (for example the connected writer with the lowest
  Yjs client id in awareness). Only the leader uploads and downloads that file.
- **Followers** never upload the path. When a remote version arrives they adopt it as their sync
  base without writing the file, because the content already reached them through the room.
- Edits made outside the room (another device without collab) arrive through the leader: it
  downloads the file and applies the difference to the shared document, which propagates to all.
- When a client leaves the room it stops being a follower and settles normally: it uploads its local
  file when it differs from the server, otherwise it just adopts the server version.
- Deleting a file deletes its collab document (compacted state and update log) in the same
  transaction. Renaming moves it with the file.

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
Last-Event-ID: <last_event_id from the local cursor>
```

`Last-Event-ID` triggers server-side replay: all events missed since that ID are sent before live streaming begins.

### Reconnect

On disconnect, reconnect with exponential backoff: 1s, 2s, 4s, 8s, cap at 60s. Always include the `Last-Event-ID` of the last **successfully processed** event (not just the last received — only after processing).

If SSE fails repeatedly (e.g., 5 attempts), fall back to polling:
```
GET /sync/v1/vaults/{vaultID}/changes?since=<last_event_id>&limit=100
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
| `member_joined` / `member_role_changed` / `member_removed` | Membership changed |
| `key_granted` | A member received a sealed vault key |
| `member_key_changed` | A member's identity key changed; their vault key must be granted again |
| `access_changed` | Groups, path access, roles or membership changed; refetch the overview and resync |
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
