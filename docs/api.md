# Cortex Sync API Reference

Base URL: `http://localhost:8080`

## Common

### Error Response

```json
{
  "error": "string",
  "code": "string (optional)",
  "details": "any (optional)"
}
```

### Authentication

Authenticated endpoints require:

- `Authorization: Bearer <access_token>` header
- `X-Device-ID: <uuid>` header

---

## Health

### GET /health

**Response** `200`

```json
{
  "status": "ok"
}
```

---

## Auth

### POST /auth/v1/register

**Request**

```json
{
  "email": "string",
  "password": "string",
  "display_name": "string"
}
```

**Response** `201`

```json
{
  "user_id": "uuid",
  "email": "string",
  "display_name": "string"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid input |
| 409 | `user_exists` | Email already registered |

### POST /auth/v1/login

**Request**

```json
{
  "email": "string",
  "password": "string",
  "device_id": "uuid",
  "device_name": "string",
  "device_type": "string"
}
```

**Response** `200`

```json
{
  "access_token": "string",
  "refresh_token": "string",
  "user_id": "uuid",
  "email": "string"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid input or device_id |
| 401 | | Invalid credentials |
| 403 | `device_revoked` | Device has been revoked |

### POST /auth/v1/token/refresh

**Request**

```json
{
  "refresh_token": "string"
}
```

**Response** `200`

```json
{
  "access_token": "string",
  "refresh_token": "string"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 401 | `token_expired` | Token expired |
| 401 | `token_revoked` | Token revoked |
| 401 | `token_reuse` | Token reuse detected, all sessions revoked |

### POST /auth/v1/logout

Requires authentication.

**Request**

```json
{
  "all_devices": false
}
```

**Response** `200`

```json
{
  "status": "ok"
}
```

---

## Devices

All endpoints require authentication.

### GET /devices/v1/

**Response** `200`

```json
[
  {
    "id": "uuid",
    "device_name": "string",
    "device_type": "string",
    "last_seen_at": "string",
    "created_at": "string",
    "revoked": false,
    "is_current": true,
    "last_sync_event_id": 0
  }
]
```

### GET /devices/v1/{deviceID}

**URL Params**: `deviceID` (uuid)

**Response** `200`

```json
{
  "id": "uuid",
  "device_name": "string",
  "device_type": "string",
  "last_seen_at": "string",
  "created_at": "string",
  "revoked": false,
  "last_sync_event_id": 0
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 404 | Device not found |

### DELETE /devices/v1/{deviceID}

**URL Params**: `deviceID` (uuid)

**Response** `200`

```json
{
  "status": "ok"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 404 | | Device not found |
| 403 | `device_revoked` | Device already revoked |

### PATCH /devices/v1/{deviceID}

**URL Params**: `deviceID` (uuid)

**Request**

```json
{
  "device_name": "string"
}
```

**Response** `200`

```json
{
  "id": "uuid",
  "device_name": "string",
  "device_type": "string",
  "last_seen_at": "string",
  "created_at": "string",
  "revoked": false,
  "last_sync_event_id": 0
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Invalid input |
| 404 | Device not found |

### PUT /devices/v1/{deviceID}/sync-cursor

Update the last sync event ID for a device. Used by clients to track their sync position across reconnects.

**URL Params**: `deviceID` (uuid)

**Request**

```json
{
  "last_sync_event_id": 42
}
```

**Response** `200`

```json
{
  "status": "ok"
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Invalid device ID or negative event ID |
| 403 | Device belongs to another user, or device is revoked |
| 404 | Device not found |

---

## Vaults

All endpoints require authentication.

### POST /vaults/v1/

**Request**

```json
{
  "name": "string",
  "description": "string",
  "encrypted_vault_key": "base64 string"
}
```

**Response** `201`

```json
{
  "id": "uuid",
  "name": "string",
  "description": "string",
  "owner_id": "uuid",
  "role": "owner",
  "created_at": "2006-01-02T15:04:05Z",
  "updated_at": "2006-01-02T15:04:05Z"
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Invalid input or non-base64 encrypted_vault_key |

### GET /vaults/v1/

**Response** `200`

```json
[
  {
    "id": "uuid",
    "name": "string",
    "description": "string",
    "owner_id": "uuid",
    "role": "owner|admin|editor|viewer",
    "created_at": "2006-01-02T15:04:05Z",
    "updated_at": "2006-01-02T15:04:05Z"
  }
]
```

### GET /vaults/v1/{vaultID}/

**URL Params**: `vaultID` (uuid)

**Response** `200`

```json
{
  "id": "uuid",
  "name": "string",
  "description": "string",
  "owner_id": "uuid",
  "role": "owner|admin|editor|viewer",
  "created_at": "2006-01-02T15:04:05Z",
  "updated_at": "2006-01-02T15:04:05Z"
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 403 | Vault access denied |
| 404 | Vault not found |

### PATCH /vaults/v1/{vaultID}/

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "name": "string",
  "description": "string|null"
}
```

**Response** `200` (same shape as GET)

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | Requires admin or owner role |
| 404 | | Vault not found |

### DELETE /vaults/v1/{vaultID}/

**URL Params**: `vaultID` (uuid)

**Response** `200`

```json
{
  "status": "ok"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | Requires owner role |
| 404 | | Vault not found |

---

## Vault Members

All endpoints require authentication.

### GET /vaults/v1/{vaultID}/members/

**URL Params**: `vaultID` (uuid)

**Response** `200`

```json
[
  {
    "vault_id": "uuid",
    "user_id": "uuid",
    "email": "string",
    "display_name": "string",
    "role": "owner|admin|editor|viewer",
    "joined_at": "string"
  }
]
```

### PATCH /vaults/v1/{vaultID}/members/{userID}

**URL Params**: `vaultID` (uuid), `userID` (uuid)

**Request**

```json
{
  "role": "admin|editor|viewer"
}
```

**Response** `200`

```json
{
  "status": "ok"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid role |
| 403 | `insufficient_role` | Insufficient permissions |
| 404 | | Vault or member not found |

### DELETE /vaults/v1/{vaultID}/members/{userID}

**URL Params**: `vaultID` (uuid), `userID` (uuid)

**Response** `200`

```json
{
  "status": "ok"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | Insufficient permissions |
| 404 | | Vault or member not found |

---

## Vault Invites

All endpoints require authentication.

### POST /vaults/v1/{vaultID}/invites/

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "invitee_email": "string",
  "role": "admin|editor|viewer",
  "encrypted_vault_key": "base64 string"
}
```

**Response** `201`

```json
{
  "id": "uuid",
  "vault_id": "uuid",
  "vault_name": "string",
  "inviter_id": "uuid",
  "invitee_email": "string",
  "role": "admin|editor|viewer",
  "encrypted_vault_key": "base64 string",
  "accepted": false,
  "expires_at": "2006-01-02T15:04:05Z",
  "created_at": "2006-01-02T15:04:05Z"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid role or non-base64 encrypted_vault_key |
| 403 | `insufficient_role` | Requires admin or owner role |
| 409 | | User already a member or invite already exists |

### GET /vaults/v1/{vaultID}/invites/

**URL Params**: `vaultID` (uuid)

**Response** `200`

```json
[
  {
    "id": "uuid",
    "vault_id": "uuid",
    "vault_name": "string",
    "inviter_id": "uuid",
    "invitee_email": "string",
    "role": "admin|editor|viewer",
    "accepted": false,
    "expires_at": "2006-01-02T15:04:05Z",
    "created_at": "2006-01-02T15:04:05Z"
  }
]
```

### DELETE /vaults/v1/{vaultID}/invites/{inviteID}

**URL Params**: `vaultID` (uuid), `inviteID` (uuid)

**Response** `200`

```json
{
  "status": "ok"
}
```

### GET /vaults/v1/invites

Returns pending invites for the authenticated user's email.

**Response** `200`

```json
[
  {
    "id": "uuid",
    "vault_id": "uuid",
    "vault_name": "string",
    "inviter_id": "uuid",
    "invitee_email": "string",
    "role": "admin|editor|viewer",
    "encrypted_vault_key": "base64 string",
    "accepted": false,
    "expires_at": "2006-01-02T15:04:05Z",
    "created_at": "2006-01-02T15:04:05Z"
  }
]
```

### POST /vaults/v1/invites/accept

**Request**

```json
{
  "invite_id": "uuid"
}
```

**Response** `200`

```json
{
  "id": "uuid",
  "name": "string",
  "description": "string",
  "owner_id": "uuid",
  "role": "admin|editor|viewer",
  "created_at": "2006-01-02T15:04:05Z",
  "updated_at": "2006-01-02T15:04:05Z"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 404 | | Invite not found |
| 410 | `invite_expired` | Invite has expired |

---

## File Sync

All endpoints require authentication. Base path: `/sync/v1/vaults/{vaultID}`

### POST /sync/v1/vaults/{vaultID}/files

Upload an encrypted file snapshot.

**URL Params**: `vaultID` (uuid)

**Headers**

| Header | Required | Description |
|--------|----------|-------------|
| Content-Type | No | Should be `application/octet-stream` |
| X-File-Path | Yes | Vault-relative file path |
| X-Local-Hash | No | Client-computed checksum |
| X-Content-Type | No | Original content type (defaults to `application/octet-stream`) |
| Content-Length | No | Size in bytes |

**Body**: Raw binary (encrypted file content)

**Response** `201`

```json
{
  "vault_id": "uuid",
  "file_path": "string",
  "version": 1,
  "snapshot_id": "uuid",
  "checksum": "string",
  "size_bytes": 1024,
  "content_type": "string",
  "deleted": false,
  "last_modified_by": "uuid",
  "last_device_id": "uuid",
  "updated_at": "2006-01-02T15:04:05Z07:00",
  "created_at": "2006-01-02T15:04:05Z07:00"
}
```

**Deduplication**: If `X-Local-Hash` matches the checksum of the current version, no new snapshot is created. The response returns the existing file info with status `201`.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Missing X-File-Path |
| 403 | `insufficient_role` | Viewer role cannot upload |
| 413 | | File too large (exceeds `CORTEX_SYNC_MAX_FILE_SIZE`) |

### GET /sync/v1/vaults/{vaultID}/files

Download an encrypted file snapshot.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| path | Yes | Vault-relative file path |
| version | No | Specific version (default: latest) |

**Response** `200`

Body: Raw binary (encrypted file content)

**Response Headers**

| Header | Description |
|--------|-------------|
| Content-Type | `application/octet-stream` |
| X-File-Path | File path |
| X-File-Version | Version number |
| X-Checksum | File checksum |
| X-Size-Bytes | File size |
| X-Created-By | User UUID who created this version |
| X-Device-ID | Device UUID that created this version |
| X-Snapshot-ID | Snapshot UUID |

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Missing path param |
| 403 | Vault access denied |
| 404 | File not found |

### DELETE /sync/v1/vaults/{vaultID}/files

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required |
|-------|----------|
| path | Yes |

**Response** `200`

```json
{
  "status": "ok"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | Viewer role cannot delete |
| 404 | | File not found |

### POST /sync/v1/vaults/{vaultID}/files/deltas

Upload an encrypted delta.

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "file_path": "string",
  "base_version": 1,
  "checksum": "string",
  "size_bytes": 512,
  "encrypted_data": "base64 string"
}
```

**Response** `201` (same shape as file info)

```json
{
  "vault_id": "uuid",
  "file_path": "string",
  "version": 2,
  "checksum": "string",
  "size_bytes": 512,
  "content_type": "string",
  "deleted": false,
  "needs_snapshot": true,
  "last_modified_by": "uuid",
  "last_device_id": "uuid",
  "updated_at": "2006-01-02T15:04:05Z07:00",
  "created_at": "2006-01-02T15:04:05Z07:00"
}
```

The `needs_snapshot` field is only present (and `true`) when the server determines the client should upload a full snapshot. This is triggered by either:

- **Delta count threshold**: More than N consecutive deltas since the last snapshot (default: 10, configurable via `CORTEX_SYNC_MAX_DELTAS_BEFORE_SNAPSHOT`)
- **Delta size ratio**: Accumulated delta bytes since last snapshot exceed a ratio of the last snapshot size (default: 0.5, configurable via `CORTEX_SYNC_MAX_DELTA_SIZE_RATIO`)

When the client uploads a new snapshot, the server automatically cleans up old deltas that precede the new snapshot version. Setting either threshold to 0 disables that check.
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid input or non-base64 encrypted_data |
| 403 | `insufficient_role` | Viewer role cannot upload |
| 409 | `conflict` | Version conflict (base_version mismatch) |

### GET /sync/v1/vaults/{vaultID}/files/deltas

Download deltas since a version.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| path | Yes | File path |
| since_version | No | Return deltas after this version (default: 0) |

**Response** `200`

```json
[
  {
    "id": "uuid",
    "file_path": "string",
    "base_version": 1,
    "target_version": 2,
    "encrypted_delta": "base64 string",
    "size_bytes": 512,
    "created_by": "uuid",
    "device_id": "uuid",
    "created_at": "2006-01-02T15:04:05Z07:00"
  }
]
```

### POST /sync/v1/vaults/{vaultID}/files/rename

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "old_path": "string",
  "new_path": "string"
}
```

**Response** `200` (file info of renamed file)

```json
{
  "vault_id": "uuid",
  "file_path": "new/path.md",
  "version": 2,
  "snapshot_id": "uuid",
  "checksum": "string",
  "size_bytes": 1024,
  "content_type": "string",
  "deleted": false,
  "last_modified_by": "uuid",
  "last_device_id": "uuid",
  "updated_at": "2006-01-02T15:04:05Z07:00",
  "created_at": "2006-01-02T15:04:05Z07:00"
}
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Missing old_path or new_path |
| 403 | `insufficient_role` | Viewer role cannot rename |
| 404 | | Source file not found |
| 409 | | Target path already exists |

### POST /sync/v1/vaults/{vaultID}/files/bulk

Fetch metadata for multiple files in a single request. Useful for initial sync to determine which files exist and their current versions. Missing files are silently omitted from the response.

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "paths": ["notes/hello.md", "notes/world.md"]
}
```

**Response** `200`

```json
[
  {
    "vault_id": "uuid",
    "file_path": "string",
    "version": 1,
    "snapshot_id": "uuid",
    "checksum": "string",
    "size_bytes": 1024,
    "content_type": "string",
    "deleted": false,
    "last_modified_by": "uuid",
    "last_device_id": "uuid",
    "updated_at": "2006-01-02T15:04:05Z07:00",
    "created_at": "2006-01-02T15:04:05Z07:00"
  }
]
```

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid request body |
| 403 | | Not a vault member |

### GET /sync/v1/vaults/{vaultID}/files/info

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required |
|-------|----------|
| path | Yes |

**Response** `200` (file info shape)

### GET /sync/v1/vaults/{vaultID}/files/list

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| include_deleted | No | `"true"` to include deleted files |

**Response** `200`

```json
[
  {
    "vault_id": "uuid",
    "file_path": "string",
    "version": 1,
    "snapshot_id": "uuid",
    "checksum": "string",
    "size_bytes": 1024,
    "content_type": "string",
    "deleted": false,
    "last_modified_by": "uuid",
    "last_device_id": "uuid",
    "updated_at": "2006-01-02T15:04:05Z07:00",
    "created_at": "2006-01-02T15:04:05Z07:00"
  }
]
```

### GET /sync/v1/vaults/{vaultID}/files/history

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required |
|-------|----------|
| path | Yes |

**Response** `200`

```json
[
  {
    "snapshot_id": "uuid",
    "version": 1,
    "size_bytes": 1024,
    "checksum": "string",
    "author_id": "uuid",
    "author_name": "string",
    "device_id": "uuid",
    "device_name": "string",
    "created_at": "2006-01-02T15:04:05Z07:00"
  }
]
```

### GET /sync/v1/vaults/{vaultID}/changes

Polling fallback for sync. Returns sync events since a given event ID.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| since | No | Return events after this event ID (int64) |
| limit | No | Max events to return |

**Response** `200`

```json
[
  {
    "id": 1,
    "vault_id": "uuid",
    "event_type": "file_created|file_updated|file_deleted|file_renamed",
    "file_path": "string",
    "version": 1,
    "actor_id": "uuid",
    "device_id": "uuid",
    "metadata": {},
    "created_at": "2006-01-02T15:04:05Z07:00"
  }
]
```

---

## SSE (Server-Sent Events)

### GET /sync/v1/vaults/{vaultID}/events

Requires authentication. Long-lived streaming connection.

**URL Params**: `vaultID` (uuid)

**Request Headers**

| Header | Required | Description |
|--------|----------|-------------|
| Last-Event-ID | No | Resume from this event ID for replay |

**Response**: `200` with `Content-Type: text/event-stream`

**Event Types**

`file_created`

```
id: 1
event: file_created
data: {"vault_uuid":"uuid","file_path":"notes/hello.md","version":1,"actor_id":"uuid","device_id":"uuid"}
```

`file_updated`

```
id: 2
event: file_updated
data: {"vault_uuid":"uuid","file_path":"notes/hello.md","version":2,"actor_id":"uuid","device_id":"uuid"}
```

`file_deleted`

```
id: 3
event: file_deleted
data: {"vault_uuid":"uuid","file_path":"notes/hello.md","version":3,"actor_id":"uuid","device_id":"uuid"}
```

`file_renamed`

```
id: 4
event: file_renamed
data: {"vault_uuid":"uuid","file_path":"notes/new-name.md","version":2,"actor_id":"uuid","device_id":"uuid","old_path":"notes/old-name.md"}
```

`ping` (every 30 seconds)

```
event: ping
data: {}
```

**Behavior**

- On connect, if `Last-Event-ID` is provided, server replays all missed events before streaming live events
- Ping every 30 seconds to keep connection alive through proxies
- Client should reconnect with exponential backoff (1s, 2s, 4s, 8s, cap 60s) using `Last-Event-ID`
- If SSE repeatedly fails, fall back to `GET /changes?since=<last_event_id>` every 30s

---

## Collab (Real-time Co-editing)

### GET /sync/v1/vaults/{vaultID}/collab

WebSocket upgrade endpoint for real-time collaborative editing (Yjs CRDT). Authentication is via query parameter because the browser WebSocket API does not support custom headers.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| path | Yes | Vault-relative file path |
| token | Yes | JWT access token (same token used in `Authorization: Bearer`) |

**Upgrade**: `101 Switching Protocols`

**Errors** (returned as HTTP before upgrade)

| Status | Condition |
|--------|-----------|
| 400 | Missing or invalid `path` or `vaultID` |
| 401 | Missing or invalid `token` |
| 403 | Not a member of this vault |

**Message Protocol**

All messages are JSON text frames with the envelope:

```json
{
  "type": "sync_step1 | sync_step2 | update | awareness | compact | ping | pong",
  "data": "<base64-encoded bytes (omitted for ping/pong)>"
}
```

| Type | Direction | Description |
|------|-----------|-------------|
| `sync_step1` | client → server | Yjs sync step 1 (state vector) — writers only |
| `sync_step2` | server → client | Yjs sync step 2 (full doc state on connect) |
| `update` | bidirectional | Incremental Yjs CRDT update — writers only |
| `awareness` | bidirectional | Cursor/presence data, relayed to all peers |
| `compact` | client → server | Full compacted Y.Doc state; server discards incremental updates and stores compacted state |
| `ping` | server → client | Keepalive every 30s |
| `pong` | client → server | Response to ping |

**Behavior**

- On connect, the server sends the existing compacted state (as `sync_step2`) followed by any buffered incremental updates (as `update`)
- Updates from writers are broadcast to all other peers in the room and buffered in memory
- Buffered updates are flushed to the database every `CORTEX_COLLAB_FLUSH_INTERVAL` (default: 10s)
- When the last peer disconnects, any remaining buffered updates are flushed to the database
- Viewers can connect and receive updates but their `update`, `sync_step1`, and `sync_step2` messages are silently dropped
- Room capacity is capped at `CORTEX_COLLAB_MAX_PEERS_PER_ROOM` (default: 10); joining a full room closes the connection with `1008 Policy Violation`

---

### GET /sync/v1/vaults/{vaultID}/collab/peers

REST endpoint to query current presence for a file. Requires authentication.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| path | Yes | Vault-relative file path |

**Response** `200`

```json
{
  "vault_id": "uuid",
  "file_path": "string",
  "peer_count": 2,
  "peer_ids": ["uuid", "uuid"]
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Missing or invalid `path` or `vaultID` |
| 401 | Unauthorized |
| 403 | Not a member of this vault |
