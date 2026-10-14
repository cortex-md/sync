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

### Feature-gated endpoints

Optional features are switched off per server (see `GET /meta/v1/capabilities`). A route whose
feature is disabled returns:

`404`

```json
{
  "error": "feature disabled on this server",
  "code": "feature_disabled"
}
```

| Feature | Env | Routes |
|---------|-----|--------|
| collab | `CORTEX_FEATURE_COLLAB` | `/sync/v1/vaults/{vaultID}/collab`, `/collab/tickets`, `/collab/peers`, `/presence` |
| avatars | `CORTEX_FEATURE_AVATARS` | `PUT/DELETE /account/v1/me/avatar`, `GET /avatars/v1/...` |
| guests | `CORTEX_FEATURE_GUESTS` | Guest invites and assigning the `guest` role |
| email_verification | `CORTEX_FEATURE_EMAIL_VERIFICATION` | `/account/v1/email/verify`, `/account/v1/email/confirm-change`, `POST /account/v1/me/email/verification` |
| password_reset | `CORTEX_FEATURE_PASSWORD_RESET` | `/account/v1/password/forgot`, `/account/v1/password/reset`, `/account/v1/password/reset/prepare` |

### Compression

JSON responses are gzip/deflate compressed when the client sends `Accept-Encoding`. Binary
downloads, batch responses and SSE are never compressed.

### Idempotent creates

`POST /vaults/v1/` and `POST /vaults/v1/{vaultID}/invites/` accept an optional client-generated
`id` (uuid). Retrying with the same `id` returns the existing resource instead of `409`:

- Vault: returns `201` with the existing vault when it is owned by the caller; `409` otherwise.
- Invite: returns `201` with the existing pending invite and a **new** `invite_token` / `invite_url`
  (the previous token stops working). `409` when the invite was already accepted or belongs to
  another vault, inviter or email.

### Common error codes

| Status | Code | Condition |
|--------|------|-----------|
| 401 | `device_mismatch` | `X-Device-ID` does not match the token's device |
| 402 | `subscription_required` / `subscription_expired` | See [Subscription](#subscription) |
| 403 | `insufficient_role` | Role or path access does not allow the action |
| 403 | `domain_not_allowed` | Email domain not in the vault's `allowed_email_domains` |
| 404 | `feature_disabled` | Feature disabled on this server |
| 409 | `conflict` | File version conflict |
| 507 | `quota_exceeded` | Vault storage quota exceeded (multi-tenant mode) |

---

## Health

### GET /health

**Response** `200`

```json
{
  "status": "ok"
}
```

### GET /ready

`200` `{"status":"ready"}`. `503` with `status` `draining` (shutdown in progress),
`database_unavailable` or `storage_unavailable`.

---

## Meta

### GET /meta/v1/capabilities

Public. Lets clients adapt to the server configuration. `Cache-Control: public, max-age=60`.

**Response** `200`

```json
{
  "version": "string",
  "tenancy_mode": "single|multi",
  "features": {
    "collab": true,
    "avatars": true,
    "guests": true,
    "email": false,
    "email_verification": false,
    "password_reset": false,
    "subscription": false,
    "registration_mode": "open|first-user|closed",
    "vault_creation": "everyone|admins"
  },
  "limits": {
    "max_file_size": 104857600,
    "max_upload_bytes": 104857600,
    "avatar_max_bytes": 5242880,
    "batch_max_items": 200,
    "batch_max_bytes": 67108864
  }
}
```

`email` is `true` when `CORTEX_MAIL_DRIVER` is `log` or `smtp`.

---

## Auth

### Key model

Every account has an X25519 identity keypair. The server never sees the account password nor
anything that can unwrap the private key:

- `master = Argon2id(password, kdf_salt, memory 65536 KiB, iterations 3, parallelism 1, 32 bytes)`
- `authKey = HKDF-SHA256(master, info "cortex/auth/v1")`, sent as `password` (base64url, no padding)
- `wrapKey = HKDF-SHA256(master, info "cortex/wrap/v1")` wraps the private key with AES-256-GCM
  (`encrypted_private_key` = nonce(12) || ciphertext || tag(16))
- `recovery_wrapped_private_key` wraps the same private key with a key derived from the recovery
  key (`info "cortex/recovery/v1"`)

Accounts created without `kdf_salt` are `kdf_version: 0` (legacy: the raw password is sent and no
identity is stored). Clients upgrade them with `PUT /account/v1/me/keys`. Binary fields are
standard base64 in responses; requests also accept unpadded and URL-safe base64.

### POST /auth/v1/prelogin

Rate limited like login.

**Request** `{ "email": "string" }`

**Response** `200`

```json
{
  "kdf_version": 1,
  "kdf_salt": "<base64 16 bytes, omitted for kdf_version 0>",
  "kdf": { "algorithm": "argon2id", "memory_kib": 65536, "iterations": 3, "parallelism": 1 }
}
```

Unknown emails get `kdf_version: 1` with a deterministic fake salt (first 16 bytes of
`HMAC-SHA256(server JWT secret, "prelogin:" + lowercased email)`), so the response does not reveal
whether a modern account exists.

### POST /auth/v1/register

**Request**

```json
{
  "email": "string",
  "password": "string (authKey for kdf_version 1)",
  "display_name": "string",
  "locale": "en|pt-BR (optional)",
  "kdf_salt": "<base64 16 bytes, optional>",
  "public_key": "<base64 32-byte X25519 key>",
  "encrypted_private_key": "<base64>",
  "recovery_wrapped_private_key": "<base64, optional>"
}
```

With `kdf_salt` the account is `kdf_version: 1` and `public_key` plus `encrypted_private_key` are
required. Without it the account is legacy (`kdf_version: 0`) and no key field may be sent.

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
| 403 | `registration_closed` | `CORTEX_AUTH_REGISTRATION_MODE` is `closed`, or `first-user` and a user exists |
| 409 | `user_exists` | Email already registered |

The first account created on a server becomes the instance admin (`is_instance_admin`).

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
  "email": "string",
  "display_name": "string",
  "avatar_url": "string (omitted when unset)",
  "email_verified": true,
  "locale": "en",
  "kdf_version": 1,
  "public_key": "<base64> | null",
  "encrypted_private_key": "<base64> | null"
}
```

`public_key` and `encrypted_private_key` are `null` for `kdf_version: 0` accounts.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid input or device_id |
| 401 | | Invalid credentials |
| 403 | `device_revoked` | Device has been revoked |
| 409 | `device_conflict` | `device_id` belongs to another account; generate a new one |

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

## Subscription

Subscription enforcement is optional and disabled by default for self-hosted deployments. When
`CORTEX_SUBSCRIPTION_ENABLED=true`, auth, devices, subscription status, and checkout remain
available without an active subscription. Creating or accepting remote vaults, member/invite
mutations that enable sync, all `/sync/v1/vaults/{vaultID}/...` endpoints, collab tickets, and SSE
or polling sync access require an active entitlement.

### POST /subscription/v1/checkout

Requires authentication.

Creates a Stripe Checkout subscription session for the authenticated user.

**Request**

```json
{
  "return_url": "https://app.example.com/settings/sync",
  "completion_url": "https://app.example.com/settings/sync/complete"
}
```

**Response** `200`

```json
{
  "checkout_url": "https://..."
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Missing or invalid return URL |
| 401 | Unauthorized |

### GET /subscription/v1/status

Requires authentication.

**Response** `200`

When a subscription exists:

```json
{
  "status": "active",
  "entitled": true,
  "current_period_start": "2026-06-01T00:00:00Z",
  "current_period_end": "2026-07-01T00:00:00Z",
  "entitlement_expires_at": "2026-07-03T00:00:00Z",
  "billing_cycle": "MONTHLY",
  "plan_product_id": "price_..."
}
```

When no subscription exists:

```json
{
  "status": "none",
  "entitled": false
}
```

### POST /webhooks/stripe

Public endpoint called by Stripe. The server validates the `Stripe-Signature` header with the Stripe
webhook endpoint secret before processing the raw body.

Handled events:

- `checkout.session.completed`
- `customer.subscription.created`
- `customer.subscription.updated`
- `customer.subscription.deleted`

Duplicate webhook event IDs return `200` without reprocessing.

### Subscription Errors

Protected sync endpoints return `402 Payment Required` when subscription enforcement is enabled and
the user is not entitled:

```json
{
  "error": "subscription required",
  "code": "subscription_required"
}
```

Expired entitlements use:

```json
{
  "error": "subscription expired",
  "code": "subscription_expired"
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
    "is_current": true
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
  "revoked": false
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 404 | Device not found |

### DELETE /devices/v1/{deviceID}

**URL Params**: `deviceID` (uuid)

**Response** `204` (no body)

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
  "revoked": false
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Invalid input |
| 404 | Device not found |

---

## Vaults

All endpoints require authentication. When subscription enforcement is enabled, creating vaults,
accepting invites, updating/deleting vaults, and member/invite mutations require an active
entitlement. Listing and reading existing vault metadata remains available.

### Vault object

```json
{
  "id": "uuid",
  "name": "string",
  "description": "string",
  "icon": "string",
  "owner_id": "uuid",
  "role": "owner|admin|editor|viewer|guest",
  "member_count": 3,
  "has_key": true,
  "allowed_email_domains": ["example.com"],
  "allow_guests": true,
  "storage_used_bytes": 1048576,
  "storage_quota_bytes": null,
  "created_at": "2006-01-02T15:04:05Z",
  "updated_at": "2006-01-02T15:04:05Z"
}
```

- `has_key`: the caller has a wrapped vault key.
- `allowed_email_domains`: empty means any domain can be invited/accept.
- `storage_quota_bytes`: `null` means unlimited; only enforced in multi-tenant mode.
- `member_count` and `has_key` are filled by `GET /vaults/v1/` and on create; single-vault reads
  (`GET`, `PATCH`, invite accept, overview) currently return `0` / `false`.

### POST /vaults/v1/

**Request**

```json
{
  "id": "uuid, optional (idempotency)",
  "name": "string",
  "description": "string",
  "icon": "string",
  "encrypted_vault_key": "base64 string, optional"
}
```

**Response** `201` (vault object, `role: "owner"`)

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid input, invalid `id` or non-base64 encrypted_vault_key |
| 403 | `insufficient_role` | `CORTEX_VAULT_CREATION=admins` and the caller is not an instance admin |
| 409 | | `id` already used by a vault the caller does not own |

### GET /vaults/v1/

**Response** `200` — array of vault objects the caller is a member of.

### GET /vaults/v1/{vaultID}/

**URL Params**: `vaultID` (uuid)

**Response** `200` (vault object)

**Errors**

| Status | Condition |
|--------|-----------|
| 403 | Vault access denied |
| 404 | Vault not found |

### PATCH /vaults/v1/{vaultID}/

Requires admin or owner. Every field is optional; omitted fields are unchanged.

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "name": "string",
  "description": "string",
  "icon": "string",
  "allowed_email_domains": ["example.com", "@corp.example"],
  "allow_guests": true,
  "storage_quota_bytes": 10737418240
}
```

- `allowed_email_domains`: replaces the list; values are lower-cased, a leading `@` is stripped,
  duplicates removed. Existing members are not affected; new invites and accepts are checked.
- `allow_guests`: `false` blocks new guest invites for this vault.
- `storage_quota_bytes`: `null` clears the quota. Only accepted in multi-tenant mode
  (`CORTEX_TENANCY_MODE=multi`) and only from an instance admin.

**Response** `200` (vault object)

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid domain, negative quota, or quota sent in single-tenant mode |
| 403 | `insufficient_role` | Requires admin or owner; quota requires instance admin |
| 404 | | Vault not found |

### DELETE /vaults/v1/{vaultID}/

**URL Params**: `vaultID` (uuid)

**Response** `204` (no body)

Blobs are queued for asynchronous deletion.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | Requires owner role |
| 404 | | Vault not found |

### GET /vaults/v1/{vaultID}/overview

Everything a vault settings screen needs in one call. Sections are filled according to the
caller's role.

**Response** `200`

```json
{
  "vault": { "...vault object" },
  "my_role": "owner|admin|editor|viewer|guest",
  "my_access": { "all": false, "base": "none|view|edit", "rules": [ { "path": "docs/", "level": "none|view|edit" } ] },
  "members": [ { "...member object" } ],
  "groups": [ { "...group object" } ],
  "access_rules": [ { "...access rule object" } ],
  "invites": [ { "...invite object" } ],
  "devices": [ { "id": "uuid", "user_id": "uuid", "device_name": "string", "device_type": "string", "last_seen_at": "RFC3339", "synced_at": "RFC3339 (omitted if never synced)" } ],
  "presence": [ { "user_id": "uuid", "file_path": "string", "can_write": true } ]
}
```

| Field | Present when |
|-------|--------------|
| `my_access` | Always; the caller's compiled access (see [Path access](#path-access)). `all: true` for owner/admin |
| `members`, `devices`, `groups` | Caller can read members (viewer and above); otherwise `[]` |
| `access_rules` | Caller can manage members (admin, owner); omitted otherwise |
| `invites` | Caller can manage invites (admin, owner); pending, unexpired only |
| `presence` | Collab enabled; filtered to paths the caller can read |

---

## Vault Members

All endpoints require authentication. Role changes and member removal require an active entitlement
when subscription enforcement is enabled.

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
    "avatar_url": "string (omitted when unset)",
    "public_key": "<base64 X25519 key, omitted when unset>",
    "has_key": true,
    "role": "owner|admin|editor|viewer|guest",
    "joined_at": "2006-01-02T15:04:05Z"
  }
]
```

Requires viewer or above (guests cannot list members).

### PATCH /vaults/v1/{vaultID}/members/{userID}

**URL Params**: `vaultID` (uuid), `userID` (uuid)

**Request**

```json
{
  "role": "admin|editor|viewer"
}
```

`guest` and `owner` cannot be assigned here; guests are created through guest invites.

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

**Response** `204` (no body)

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | Insufficient permissions |
| 404 | | Vault or member not found |

A member can remove themselves (leave) unless they are the owner. Removal deletes the member's
group memberships, path access entries and vault key in the same transaction.

Role changes and removals invalidate cached access for the whole vault on every instance and emit
the `access_changed` SSE event.

### Path access

Access to paths is enforced by the server for every file operation (list, info, bulk, batch,
download, history, deltas, changes, SSE, collab, upload, delete, restore, rename) and shares.

- Levels: `none` < `view` < `edit`.
- Owner and admin always have `edit` everywhere; restrictions do not apply to them.
- Base level by role: editor `edit`, viewer `view`, guest `none`.
- A rule path ending in `/` covers that folder; any other path covers exactly that file. Leading
  `/` is stripped; paths containing `..` or `//` are rejected.
- A rule is `restricted` (only its entries grant access) or not (entries raise the inherited level).
- Entries grant `view` or `edit` to a member (`user`) or a group (`group`).
- Effective level of a path = level of the deepest rule covering it, compiled top-down: for each
  rule that is restricted or has an entry for the caller (directly or through a group), take the
  highest entry level; a restricted rule uses only that, a non-restricted rule uses the higher of
  that and the level inherited from its nearest covering rule (or the base level).
- File writes are decided by the effective level, so a viewer with an `edit` entry can write there.

Renaming a file moves its exact-file rule. When a file leaves a folder rule `R` (old = `R` + rest)
for a new folder `R'` (new = `R'` + rest) that has no rule and no live files yet, `R`'s rule and
entries are copied to `R'` so folder renames keep their restriction. Deleting a file deletes its
exact-file rule. Both happen inside the file transaction.

Every group, membership, rule, role or removal change invalidates cached access for the whole vault
on every instance and emits the `access_changed` SSE event.

### Groups

Group object:

```json
{ "id": "uuid", "name": "Finance", "member_ids": ["uuid"], "created_at": "RFC3339" }
```

| Method | Path | Access | Body |
|--------|------|--------|------|
| GET | `/vaults/v1/{vaultID}/groups` | viewer+ | — → `200` array of groups |
| POST | `/vaults/v1/{vaultID}/groups` | admin/owner | `{ "name", "member_ids" }` → `201` group |
| PATCH | `/vaults/v1/{vaultID}/groups/{groupID}` | admin/owner | `{ "name"?, "member_ids"? }` → `200` group |
| DELETE | `/vaults/v1/{vaultID}/groups/{groupID}` | admin/owner | — → `204`; deletes the group's entries |

Names are trimmed, 1–64 characters and unique per vault ignoring case (`409 group_name_taken`).
`member_ids` must be vault members (`400` otherwise); `PATCH` replaces the member list.

### Access rules

Access rule object:

```json
{
  "path": "finance/",
  "restricted": true,
  "entries": [ { "principal_type": "user|group", "principal_id": "uuid", "level": "view|edit" } ]
}
```

#### GET /vaults/v1/{vaultID}/access

Admin/owner. **Response** `200` — array of access rule objects ordered by path.

#### PUT /vaults/v1/{vaultID}/access

Admin/owner. Upserts the rule for `path` and replaces its entries. Requires an active entitlement
when subscription enforcement is enabled.

**Request**: an access rule object.

**Response** `200` with the stored rule, or `204` when `restricted` is `false` and `entries` is
empty (the rule is deleted).

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid path, level or principal; user is not a member or group is not in the vault |
| 403 | `insufficient_role` | Requires admin or owner |

---

## Vault Invites

All endpoints require authentication. Creating, accepting, and deleting invites require an active
entitlement when subscription enforcement is enabled.

### Invite object

```json
{
  "id": "uuid",
  "vault_id": "uuid",
  "vault_name": "string",
  "inviter_id": "uuid",
  "inviter_name": "string (omitted when unknown)",
  "invitee_email": "string",
  "role": "admin|editor|viewer|guest",
  "grants": [ { "path": "projects/alpha/", "can_write": true } ],
  "encrypted_vault_key": "base64 string (omitted when absent)",
  "accepted": false,
  "invite_token": "string (create response only)",
  "invite_url": "string (create response only)",
  "expires_at": "2006-01-02T15:04:05Z",
  "created_at": "2006-01-02T15:04:05Z"
}
```

`grants` is only present for guest invites. Invites expire after 7 days.

### POST /vaults/v1/{vaultID}/invites/

Requires admin or owner. Admins can only invite roles below their own.

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "id": "uuid, optional (idempotency)",
  "invitee_email": "string",
  "role": "admin|editor|viewer|guest",
  "grants": [ { "path": "projects/alpha/", "can_write": true } ],
  "encrypted_vault_key": "base64 string, optional"
}
```

`grants` is required (1–100) for `guest` and rejected for every other role; same path rules as
[Path access](#path-access). On accept each grant becomes a `user` entry (`can_write` → `edit`,
otherwise `view`) on a non-restricted rule for that path.

**Response** `201` (invite object with `invite_token` and `invite_url`, returned only here). When
mail is enabled the invitee also receives an email with the same link. Replaying an existing `id`
rotates the token (see [Idempotent creates](#idempotent-creates)).

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid email, role, grants or non-base64 encrypted_vault_key |
| 403 | `insufficient_role` | Requires admin or owner, or role not below the inviter's |
| 403 | `domain_not_allowed` | Invitee email domain not in `allowed_email_domains` |
| 404 | `feature_disabled` | Guest invite while guests are disabled on the server or `allow_guests` is `false` |
| 409 | | User already a member, pending invite exists, or `id` conflict |

### GET /vaults/v1/{vaultID}/invites/

Requires admin or owner. Returns the vault invites that were not accepted yet (expired ones stay until
the cleaner removes them), oldest first, without `encrypted_vault_key`, `invite_token` or
`invite_url`.

**URL Params**: `vaultID` (uuid)

**Response** `200` — array of invite objects.

### DELETE /vaults/v1/{vaultID}/invites/{inviteID}

**URL Params**: `vaultID` (uuid), `inviteID` (uuid)

**Response** `204` (no body)

### GET /vaults/v1/invites

Returns pending invites for the authenticated user's email, with `inviter_name` and
`encrypted_vault_key`.

**Response** `200` — array of invite objects.

### POST /vaults/v1/invites/accept

**Request** — either field:

```json
{ "token": "string" }
```

```json
{ "invite_id": "uuid" }
```

`token` works for any account; `invite_id` requires the caller's email to match the invite.

**Response** `200` — vault object (`role` from the invite) plus `encrypted_vault_key` when the invite
carried one. Emits `member_joined`.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid `invite_id` |
| 403 | | `invite_id` belongs to another email |
| 403 | `domain_not_allowed` | Caller's email domain not allowed in the vault |
| 404 | | Invite not found |
| 409 | | Invite already used, or caller already a member |
| 410 | `invite_expired` | Invite has expired |

---

## Vault Encryption

Endpoints for managing vault-level E2E encryption keys. The server stores an encrypted VEK (Vault Encryption Key) and the salt used to derive the wrapping key from the user's password. The server never sees the plaintext VEK.

All endpoints require authentication and, when subscription enforcement is enabled, an active
entitlement. Base path: `/sync/v1/vaults/{vaultID}`

### GET /sync/v1/vaults/{vaultID}/encryption

Check if a vault has an encryption key set up, and retrieve the encrypted key data.

**URL Params**: `vaultID` (uuid)

**Response** `200`

```json
{
  "has_key": true,
  "has_member_keys": true,
  "salt": "base64-encoded-salt",
  "encrypted_vek": "base64-encoded-encrypted-vek"
}
```

When no password-based encryption key exists:

```json
{
  "has_key": false,
  "has_member_keys": true
}
```

`has_member_keys` is `true` when any member has a sealed vault key (`vault_keys` row). `has_key`
describes the legacy password-wrapped VEK.

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Invalid vault ID |
| 401 | Unauthorized |
| 403 | Not a member of this vault |

### POST /sync/v1/vaults/{vaultID}/encryption

Requires admin or owner. Create or update the vault encryption key. The client generates a random VEK, derives a wrapping key from the user's password via Argon2id, encrypts the VEK, and sends the salt + encrypted VEK to the server.

**URL Params**: `vaultID` (uuid)

**Request Body**

```json
{
  "salt": "base64-encoded-salt",
  "encrypted_vek": "base64-encoded-encrypted-vek"
}
```

**Response** `201`

```json
{
  "status": "created"
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Invalid vault ID, invalid base64, or missing fields |
| 401 | Unauthorized |
| 403 | Not a member of this vault, or not admin/owner (`insufficient_role`) |

---

## File Sync

All endpoints require authentication and, when subscription enforcement is enabled, an active
entitlement (in multi-tenant mode the vault owner's entitlement). Base path:
`/sync/v1/vaults/{vaultID}`. Callers only see paths they can `view` and modify paths they can
`edit` (see [Path access](#path-access)); anything else returns `403 insufficient_role` (or is
omitted from list-style responses).

### File info object

```json
{
  "vault_id": "uuid",
  "file_path": "string",
  "version": 1,
  "snapshot_id": "uuid (snapshot upload response only)",
  "checksum": "string",
  "size_bytes": 1024,
  "content_type": "string",
  "deleted": false,
  "needs_snapshot": true,
  "last_modified_by": "uuid",
  "last_device_id": "uuid",
  "created_by": "uuid (original author, omitted when unknown)",
  "updated_at": "2006-01-02T15:04:05Z07:00",
  "created_at": "2006-01-02T15:04:05Z07:00"
}
```

`needs_snapshot` is only present (and `true`) on delta upload responses.

### POST /sync/v1/vaults/{vaultID}/files

Upload an encrypted file snapshot.

**URL Params**: `vaultID` (uuid)

**Headers**

| Header | Required | Description |
|--------|----------|-------------|
| Content-Type | No | Should be `application/octet-stream` |
| X-File-Path | Yes | Vault-relative file path |
| X-File-Path-Encoding | No | Set to `uri` when `X-File-Path` is percent-encoded (required for non-ASCII paths) |
| X-Base-Version | No | Expected current version; a mismatch returns `409 conflict` without storing the upload |
| X-Local-Hash | No | Client-computed checksum |
| X-Content-Type | No | Original content type (defaults to `application/octet-stream`) |
| Content-Length | No | Size in bytes; chunked uploads are accepted |

**Body**: Raw binary (encrypted file content)

**Response** `201` (file info object, including `snapshot_id`)

**Deduplication**: If `X-Local-Hash` matches the checksum of the current, non-deleted version, no new snapshot is created. The response returns the existing file info with status `201`, so retries are idempotent. Uploading identical content to a deleted file creates a new version and restores it.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Missing or invalid X-File-Path / X-Base-Version |
| 403 | `insufficient_role` | No `edit` access to the path |
| 409 | `conflict` | X-Base-Version mismatch or concurrent upload won the version |
| 413 | | File too large (exceeds `CORTEX_SYNC_MAX_FILE_SIZE`) |
| 507 | `quota_exceeded` | Vault storage quota would be exceeded (multi-tenant mode) |

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
| X-File-Path-Encoded | Percent-encoded file path, safe for non-ASCII names |
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
| 403 | Vault access denied or no `view` access to the path |
| 404 | File not found |

### POST /sync/v1/vaults/{vaultID}/files/batch

Download many snapshots in one streamed response.

**Request**

```json
{
  "items": [
    { "path": "notes/a.md", "version": 0 },
    { "path": "notes/b.md", "version": 3 }
  ]
}
```

`version: 0` means latest. Limits: 1–200 items and 64 MB total snapshot size
(`limits.batch_max_items` / `limits.batch_max_bytes` in capabilities).

**Response** `200`, `Content-Type: application/vnd.cortex.batch`, `X-Batch-Count: <items>`.
The body is one frame per requested item, in request order:

| Field | Type |
|-------|------|
| header length | u32, big-endian |
| header | JSON, `header length` bytes |
| body length | u64, big-endian |
| body | raw encrypted snapshot, `body length` bytes (0 when status is not `ok`) |

Frame header:

```json
{
  "path": "notes/a.md",
  "version": 4,
  "status": "ok|not_found|forbidden|error",
  "snapshot_id": "uuid",
  "checksum": "string",
  "size_bytes": 1024,
  "created_by": "uuid",
  "created_at": "RFC3339"
}
```

`snapshot_id`, `checksum`, `created_by` and `created_at` are only set when `status` is `ok`;
`version` is then the resolved version. `forbidden` marks paths the caller cannot view.

**Errors** (before streaming starts)

| Status | Condition |
|--------|-----------|
| 400 | Empty or more than 200 items, empty path, or negative version |
| 403 | Vault access denied |
| 413 | Total size above 64 MB |

### DELETE /sync/v1/vaults/{vaultID}/files

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required |
|-------|----------|
| path | Yes |

**Response** `204` (no body)

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 403 | `insufficient_role` | No `edit` access to the path |
| 404 | | File not found |

### POST /sync/v1/vaults/{vaultID}/files/restore

Undeletes a file: bumps its version, keeps the latest snapshot as content and emits
`file_created`. Same permissions as delete.

**Query Params**: `path` (required)

**Response** `200` `{"status":"ok"}`. `400` when the file is not deleted, `404` when it has no
snapshot.

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

**Response** `201` (file info object, may include `needs_snapshot: true`)

The `needs_snapshot` field is only present (and `true`) when the server determines the client should upload a full snapshot. This is triggered by either:

- **Delta count threshold**: More than N consecutive deltas since the last snapshot (default: 10, configurable via `CORTEX_SYNC_MAX_DELTAS_BEFORE_SNAPSHOT`)
- **Delta size ratio**: Accumulated delta bytes since last snapshot exceed a ratio of the last snapshot size (default: 0.5, configurable via `CORTEX_SYNC_MAX_DELTA_SIZE_RATIO`)

When the client uploads a new snapshot, the server automatically cleans up old deltas that precede the new snapshot version. Setting either threshold to 0 disables that check.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid input or non-base64 encrypted_data |
| 403 | `insufficient_role` | No `edit` access to the path |
| 409 | `conflict` | Version conflict (base_version mismatch) |

### GET /sync/v1/vaults/{vaultID}/files/deltas

Download deltas since a version.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| path | Yes | File path |
| since_version | No | Return deltas whose `base_version` is at least this value (default: 0) |
| limit | No | Page size; `0` or omitted uses 200, values above 200 are capped |

**Response** `200`, ordered by `base_version`. When more deltas remain, the `X-Next-Cursor` header
carries the `since_version` for the next page.

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

**Response** `200` (file info object of the renamed file)

The caller needs `edit` access on both paths.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Missing old_path or new_path |
| 403 | `insufficient_role` | Viewer, or guest without write access to both paths |
| 404 | | Source file not found |
| 409 | | Target path already exists |

### POST /sync/v1/vaults/{vaultID}/files/bulk

Fetch metadata for multiple files in a single request. Missing files and paths the caller cannot
view are silently omitted.

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "file_paths": ["notes/hello.md", "notes/world.md"]
}
```

Up to 1000 paths.

**Response** `200` — array of file info objects.

**Errors**

| Status | Code | Condition |
|--------|------|-----------|
| 400 | | Invalid request body or more than 1000 paths |
| 403 | | Not a vault member |

### GET /sync/v1/vaults/{vaultID}/files/info

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required |
|-------|----------|
| path | Yes |

**Response** `200` (file info object)

### GET /sync/v1/vaults/{vaultID}/files/list

Lists current files ordered by path, limited to paths the caller can view.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| include_deleted | No | `true` to include deleted files |
| limit | No | Page size; `0` or omitted returns every file, values above 5000 are capped |
| cursor | No | Value of `X-Next-Cursor` from the previous page |

**Request Headers**

| Header | Description |
|--------|-------------|
| If-None-Match | `ETag` of a previous first-page response |

**Response** `200` — array of file info objects.

**Response Headers**

| Header | Description |
|--------|-------------|
| X-Latest-Event-ID | Latest sync event id of the vault when the list was read; use it as `since` for `/changes` or `Last-Event-ID` for SSE |
| X-Next-Cursor | Present when more pages exist |
| ETag | Weak ETag (first page only), derived from the latest event id, `include_deleted` and `limit` |

**Response** `304` — first page only, when `If-None-Match` equals the current `ETag`; carries
`ETag` and `X-Latest-Event-ID`.

### GET /sync/v1/vaults/{vaultID}/files/history

Up to 200 most recent snapshots of a file.

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
    "author_avatar_url": "string (omitted when unset)",
    "device_id": "uuid",
    "device_name": "string",
    "created_at": "2006-01-02T15:04:05Z07:00"
  }
]
```

### GET /sync/v1/vaults/{vaultID}/changes

Polling fallback for sync. Returns sync events since a given event ID, limited to paths the caller
can view.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| since | No | Return events after this event ID (int64) |
| limit | No | Max events to return (default 100, max 1000) |

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

### PUT /sync/v1/vaults/{vaultID}/cursor

Stores the calling device's sync position for this vault (shown in the overviews).

**Request**

```json
{ "last_event_id": 42 }
```

**Response** `204`

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Negative `last_event_id` |
| 403 | Vault access denied |

---

## Shared notes (public links)

Requires `CORTEX_FEATURE_PUBLIC_LINKS=true` (default); otherwise these routes answer
`404 {"code":"feature_disabled"}`. Links are end-to-end encrypted: the client encrypts the note
with a random 32-byte link key `K`, uploads only ciphertext, and puts `K` in the URL fragment
(`https://host/s/{id}#k=<base64url K>`), which browsers never send to the server.

- One link per note. Creating, publishing, changing expiry and revoking need `editor`, `admin` or
  `owner` (guests and viewers get `403 insufficient_role`).
- The vault setting `allow_public_links` (PATCH `/vaults/v1/{id}/`) blocks new links with
  `403 public_links_disabled` and makes existing links answer `404` until it is turned back on.
- Deleting a note revokes its link; renaming keeps it on the new path. Expired links are removed by
  the hourly cleaner and count toward vault storage while they exist.

**Share object**

```json
{
  "id": "22-char base64url",
  "vault_id": "uuid",
  "file_path": "notes/plan.md",
  "url": "https://sync.example.com/s/{id}",
  "wrapped_key": "base64 AES-256-GCM(VEK, K): nonce(12) | ciphertext(32) | tag(16)",
  "revision": 3,
  "size_bytes": 2048,
  "expires_at": "RFC3339 | null",
  "created_by": "uuid",
  "created_at": "RFC3339",
  "updated_at": "RFC3339"
}
```

`wrapped_key` lets any member holding the vault key (VEK) recover `K` to rebuild the link or
republish new content; the server cannot unwrap it.

### GET /sync/v1/vaults/{vaultID}/shares/

Lists the vault's shared notes visible to the caller (only paths the caller can view).

### POST /sync/v1/vaults/{vaultID}/shares/

`{"file_path": "notes/plan.md", "wrapped_key": "base64", "expires_at": "RFC3339 | null"}` →
`201` share. If the note is already shared, returns `200` with the existing share. `404` when the
note does not exist or is deleted; `400` for a malformed key or an expiry in the past.

### PUT /sync/v1/vaults/{vaultID}/shares/{shareID}/content

Body: the encrypted bundle (`application/octet-stream`, `Content-Length` required, max
`CORTEX_SYNC_MAX_FILE_SIZE`). Increments `revision` and returns the share. `507 quota_exceeded`
when the vault quota would be exceeded (multi-tenant mode).

Bundle plaintext (before encryption) is gzip-compressed JSON:
`{"v":1,"title":"...","markdown":"...","html":"<sanitized html>","attachments":[{"path":"img.png","type":"image/png","data":"base64"}]}`;
`html` references attachments as `src="cortex-attachment:<index>"`. Ciphertext layout:
`nonce(12) | AES-256-GCM ciphertext | tag(16)`.

### PATCH /sync/v1/vaults/{vaultID}/shares/{shareID}

`{"expires_at": "RFC3339 | null"}` → share.

### DELETE /sync/v1/vaults/{vaultID}/shares/{shareID}

`204`. The link stops working immediately; its blob is deleted by the blob janitor.

### GET /share/v1/{shareID} (public)

Returns the ciphertext. Headers: `ETag: "r<revision>"`, `X-Share-Revision`,
`Cache-Control: no-cache`; `If-None-Match` → `304`. `404` when the link is unknown, revoked,
expired, unpublished, or disabled by the vault.

### GET /s/{shareID} (public)

Static viewer page (`script-src 'self'`, no inline scripts, `frame-ancestors 'none'`). It fetches
the ciphertext, decrypts with WebCrypto using the fragment key, decompresses with
`DecompressionStream`, and renders the HTML inside an iframe sandboxed without script permission.
Images come only from the bundle as `data:` URLs. The bundle is
`{ v: 1, title, markdown, html, attachments, author?, tags? }`; the page shows the author, the
share's last update, read time, tags, a table of contents from `h2` headings, a light/dark toggle
and a copy-link button.

---

## Templates

Users share note and database templates with a group in the vault (`team`), with anyone holding
a link (`link`) or in the public Marketplace (`market`). Only the template structure is shared, as
an opaque `payload` (base64 in JSON, max 1 MiB decoded, else `413`).

- `team` payloads must be encrypted client-side with the vault key (`"encrypted": true`); `link`
  and `market` payloads must be plaintext (`"encrypted": false`). A mismatch is `400`.
- `group_id` is only valid for `team` (a group of this vault, else `400`); `null` means every vault
  member. Deleting the group sets it back to `null`.
- `link` and `market` need the vault setting `allow_public_links` (`403 public_links_disabled`);
  turning it off makes their public routes answer `404`.
- `team` and `link` are `published` immediately. `market` starts as `pending_review` and is
  checked right after the write (and by the hourly cleaner for leftovers): the payload must be
  `{"format":1,"kind":"note"|"database","files":[{"path":"...","content":"..."}]}` with no other
  fields, `kind` equal to the template kind, 1–200 files, unique relative paths (no leading `/`,
  no `\`, no `:`, no empty, `.` or `..` segments) ending in `.md` or `.json`. It becomes
  `published` or `rejected` with a `review_note`.
- Listing and reading need a vault role with read access (`viewer` and above; guests get `403`).
  Creating needs `editor`, `admin` or `owner`. A caller sees `team` templates for everyone or for
  a group they belong to, plus all templates they own; vault `admin`/`owner` see every template.
- Only the template owner (still `editor` or above) or a vault `admin`/`owner` can update it; the
  owner (any role) or a vault `admin`/`owner` can delete it. Others get `403 insufficient_role`,
  or `404` when the template is not visible to them.

**Template object**

```json
{
  "id": "uuid",
  "vault_id": "uuid",
  "kind": "note | database",
  "audience": "team | link | market",
  "group_id": "uuid | null",
  "group_name": "Design | null",
  "owner_id": "uuid | null",
  "owner_name": "Ana",
  "name": "Weekly review",
  "description": "",
  "include_samples": false,
  "encrypted": true,
  "version": 1,
  "file_count": 0,
  "status": "published | pending_review | rejected",
  "review_note": "",
  "slug": "12-char base64url",
  "url": "/t/{slug} | null",
  "created_at": "RFC3339",
  "updated_at": "RFC3339"
}
```

`url` is a relative path, set only for `link` and `market`. `file_count` is known only for
plaintext payloads (`0` for `team`, set by the review for `market`).

**Public template object**

```json
{
  "slug": "12-char base64url",
  "kind": "note | database",
  "name": "Weekly review",
  "description": "",
  "owner_name": "Ana",
  "version": 2,
  "updated_at": "RFC3339",
  "file_count": 3
}
```

### GET /sync/v1/vaults/{vaultID}/templates/

Array of template objects visible to the caller, newest first.

### POST /sync/v1/vaults/{vaultID}/templates/

```json
{
  "kind": "note",
  "audience": "team",
  "group_id": "uuid | null",
  "name": "Weekly review",
  "description": "optional",
  "include_samples": false,
  "payload": "base64",
  "encrypted": true
}
```

`201` template. `name` is 1–120 characters, `description` up to 2000.

### GET /sync/v1/vaults/{vaultID}/templates/{templateID}/payload

`{"payload": "base64", "encrypted": true, "version": 1}`.

### PUT /sync/v1/vaults/{vaultID}/templates/{templateID}/payload

`{"payload": "base64", "encrypted": false}` → template. Increments `version`; a `market` template
goes back to `pending_review` and is checked again.

### PATCH /sync/v1/vaults/{vaultID}/templates/{templateID}

Any subset of `{"audience", "group_id", "name", "description", "include_samples"}` → template.
`"group_id": null` clears the group; omitting it keeps the current one. Moving between `team` and
`link`/`market` changes the required encryption, so the same request must also carry
`"payload"` and `"encrypted"` (both or neither); this increments `version`. Switching to `market`
triggers the review; switching to `team`/`link` publishes immediately.

### DELETE /sync/v1/vaults/{vaultID}/templates/{templateID}

`204`.

### GET /template/v1/market (public)

Query: `kind=note|database` (optional, `400` otherwise), `q` (optional, case-insensitive match on
name and description). Array of public template objects for published `market` templates, newest
first, at most 100. Gated by `CORTEX_FEATURE_PUBLIC_LINKS`.

### GET /template/v1/{slug} (public)

`{"template": <public template object>, "payload": "base64"}` for a `link` template or a published
`market` template. `team`, pending and rejected templates answer `404`. Gated by
`CORTEX_FEATURE_PUBLIC_LINKS`.

---

## SSE (Server-Sent Events)

### GET /sync/v1/vaults/{vaultID}/events

Requires authentication and, when subscription enforcement is enabled, an active entitlement.
Long-lived streaming connection.

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

File events also carry `event_type` and `created_at` in `data`.

Ephemeral vault events (no `id`, not replayed):

| Event | Data |
|-------|------|
| `collab_active` / `collab_inactive` | `{"file_path"}` — first peer joined / last peer left a collab room |
| `member_joined` | `{"user_id","role","has_key"}` |
| `member_role_changed` | `{"user_id","role"}` |
| `member_removed` | `{"user_id"}` |
| `key_granted` | `{"user_id"}` |
| `member_key_changed` | `{"user_id"}` — the member's identity key changed; their vault key was dropped and must be granted again |
| `access_changed` | `{}` — groups, path access, roles or membership changed; refetch the overview and resync |

File and collab events are only delivered for paths the caller can view (renames match on either
path). Guests only receive member/key events about themselves. Replay is filtered the same way. On
`access_changed` the stream recomputes the caller's access without reconnecting.

**Errors**: `503` while the server is draining. A change of the caller's own role or removal closes
the stream; reconnect to pick up the new access.

**Behavior**

- On connect, if `Last-Event-ID` is provided, server replays all missed events before streaming live events
- Ping every 30 seconds to keep connection alive through proxies
- Client should reconnect with exponential backoff (1s, 2s, 4s, 8s, cap 60s) using `Last-Event-ID`
- If SSE repeatedly fails, fall back to `GET /changes?since=<last_event_id>` every 30s

---

## Collab (Real-time Co-editing)

All collab routes are feature-gated by `collab` (`404 feature_disabled` when off).

### POST /sync/v1/vaults/{vaultID}/collab/tickets

Creates a short-lived ticket for the WebSocket collab endpoint, bound to user, vault and path.
Requires authentication, `view` access to `file_path` and, when
subscription enforcement is enabled, an active entitlement.

**URL Params**: `vaultID` (uuid)

**Request**

```json
{
  "file_path": "notes/diary.md"
}
```

**Response** `201`

```json
{
  "ticket": "string",
  "expires_at": "2026-06-18T12:00:00Z"
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Missing or invalid `file_path` or `vaultID` |
| 401 | Missing or invalid bearer token |
| 403 | Not a member of this vault, or no `view` access to the path |

### GET /sync/v1/vaults/{vaultID}/collab

WebSocket upgrade endpoint for real-time collaborative editing (Yjs CRDT). Authentication uses a ticket created by `POST /sync/v1/vaults/{vaultID}/collab/tickets` (valid for 60 seconds and reusable within that window).

Collab is always end-to-end encrypted. Every binary payload (`update`, `awareness`, `sync_step1`,
`sync_step2` and the `compact` state) is an envelope
`[0x01][nonce 12][AES-256-GCM ciphertext || tag 16]` with key `HKDF-SHA256(VEK, info "cortex/collab/v1")`
and AAD `utf8("cortex/collab/v1:" + vaultId)`. The server relays and stores envelopes as opaque bytes
and never reads them.

**URL Params**: `vaultID` (uuid)

**Query Params**

| Param | Required | Description |
|-------|----------|-------------|
| path | Yes | Vault-relative file path |
| ticket | Yes | Short-lived collab ticket bound to this path |
| frames | No | `binary` to use binary frames for Yjs payloads (see below) |

**Upgrade**: `101 Switching Protocols`

**Errors** (returned as HTTP before upgrade)

| Status | Condition |
|--------|-----------|
| 400 | Missing or invalid `path` or `vaultID` |
| 401 | Missing, invalid or expired `ticket` |
| 403 | Not a member of this vault, or no `view` access to the path |
| 503 | Server is draining |

**Close codes** (WebSocket close frames after the upgrade)

| Code | Condition |
|------|-----------|
| `1001` | Server shutting down |
| `1008` | Room full (`CORTEX_COLLAB_MAX_PEERS_PER_ROOM`) |
| `1013` | Update buffer full, reconnect |
| `4402` | Subscription entitlement lapsed |
| `4403` | Access changed (role, rules or removal); reconnect to pick up the new access |
| `4408` | Client too slow to drain frames; reconnect and resync |

**Message Protocol**

Default: JSON text frames (binary payloads are base64 in `data`):

```json
{
  "type": "welcome | synced | sync_step1 | sync_step2 | update | awareness | compact | peer_joined | peer_left | ping | pong",
  "data": "<base64>",
  "up_to": 123,
  "client_id": "uuid",
  "from": { "client_id": "uuid", "user_id": "uuid", "display_name": "Ana", "avatar_url": "/avatars/v1/...", "can_write": true },
  "peer": { "...same shape as from" },
  "peers": [ { "...same shape as from" } ]
}
```

| Type | Direction | Description |
|------|-----------|-------------|
| `welcome` | server → client | First frame: own `client_id`, `from` (own identity), `peers` already in the room and `up_to` (highest persisted update id) |
| `synced` | server → client | Last frame of the initial backlog (`{"type":"synced"}`) |
| `sync_step1` | bidirectional | Yjs state vector request, relayed to peers (allowed for viewers) |
| `sync_step2` | server → client | Compacted document state sent on join. Clients must not send it: the server would persist and rebroadcast it like an `update` (dropped for viewers) |
| `update` | bidirectional | Incremental Yjs update; writers only (dropped for viewers). Also the answer to a peer's `sync_step1`, sent by one elected responder |
| `awareness` | bidirectional | Cursor/selection/presence payload, relayed to every peer |
| `compact` | client → server | Compacted Y.Doc state plus `up_to`; writers only; deletes persisted updates with id <= `up_to` |
| `peer_joined` / `peer_left` | server → client | Presence changes (`peer` / `client_id`) |
| `ping` / `pong` | server ↔ client | Keepalive every 30s |

**Binary frames** (`?frames=binary`)

`sync_step1`, `sync_step2`, `update` and `awareness` travel as WebSocket binary messages with raw
envelope bytes instead of base64 JSON. Control messages (`welcome`, `synced`, `peer_joined`,
`peer_left`, `compact`, `ping`, `pong`) stay JSON text frames in both modes.

| Direction | Layout |
|-----------|--------|
| client → server | `[opcode u8][payload]` |
| server → client | `[opcode u8][client_id length u8][client_id][payload]` |

| Opcode | Type |
|--------|------|
| 1 | `sync_step1` |
| 2 | `sync_step2` |
| 3 | `update` |
| 4 | `awareness` |

`client_id` is the sender's id (empty for state the server sends on join); resolve the sender's
identity from `welcome.peers` / `peer_joined`. Unknown opcodes are ignored. A binary-mode client
may still send JSON text frames, and JSON and binary clients can share a room; the server encodes
each frame per recipient.

**Behavior**

- Every relayed frame carries `from`, stamped by the server from the authenticated session. Clients must use it instead of identity fields inside `data`.
- On join the server sends `welcome`, the compacted state (`sync_step2`), persisted updates, updates still buffered on that instance and finally `synced`. Clients reconcile with their local file only after `synced`.
- Only one elected client answers a peer's `sync_step1`, with an `update`; clients never send `sync_step2`.
- While a room is live, only one elected writer (the leader) uploads and downloads the file through file sync; the other peers adopt remote versions as their base without writing the file. See `docs/sync-client-guide.md`.
- Deleting a file deletes its collab document (compacted state and update log) in the same transaction; renaming moves it.
- Updates are buffered and flushed every `CORTEX_COLLAB_FLUSH_INTERVAL`, when any peer leaves and during shutdown.
- Peers connected to different instances see each other; frames cross instances through PostgreSQL only when a room has remote peers.
- Tickets are HMAC-signed (stateless), valid for 60 seconds on any instance and reusable within that window.

---

### GET /sync/v1/vaults/{vaultID}/presence

Who is editing which file in the vault, across all instances.

**Response** `200`

```json
[
  { "file_path": "notes/a.md", "peers": [ { "client_id": "uuid", "user_id": "uuid", "display_name": "Ana", "avatar_url": "", "can_write": true } ] }
]
```

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
  "peer_ids": ["uuid", "uuid"],
  "peers": [
    { "client_id": "uuid", "user_id": "uuid", "display_name": "Ana", "avatar_url": "/avatars/v1/...", "can_write": true }
  ]
}
```

**Errors**

| Status | Condition |
|--------|-----------|
| 400 | Missing or invalid `path` or `vaultID` |
| 401 | Unauthorized |
| 403 | Not a member of this vault, or no `view` access to the path |


---

## Account

All `/account/v1/me` routes require `Authorization` and `X-Device-ID`.

### GET /account/v1/me/

```json
{ "id": "uuid", "email": "ana@example.com", "display_name": "Ana", "locale": "pt-BR", "avatar_url": "/avatars/v1/<user>/<hash>.jpg", "email_verified": true, "has_public_key": true, "is_instance_admin": false, "created_at": "RFC3339" }
```

`is_instance_admin` is `true` for the first account on the server; instance admins can create vaults
when `CORTEX_VAULT_CREATION=admins` and set vault storage quotas.

### PATCH /account/v1/me/

`{ "display_name": "Ana", "locale": "pt-BR" }` (both optional; locale is `en` or `pt-BR`). Returns the account.

### PUT /account/v1/me/password

`{ "current_password": "...", "new_password": "...", "kdf_salt": "<base64>", "encrypted_private_key": "<base64>" }` → `204`. For `kdf_version: 1` accounts both passwords are auth keys and `kdf_salt` plus the re-wrapped `encrypted_private_key` are required (the identity keypair is unchanged); legacy accounts must not send them. Revokes refresh tokens of every other device and emails a notice. `401 invalid_credentials` when the current password is wrong.

### POST /account/v1/me/email

`{ "password": "...", "new_email": "new@example.com" }`. `password` is the auth key for `kdf_version: 1` accounts.

- Email verification enabled: `202 {"status":"confirmation_sent"}`. Sends a confirmation link to the new address and a notice to the current one.
- Email verification disabled: `200 {"status":"changed"}`. The email changes immediately and is marked unverified.

`401 invalid_credentials` for a wrong password, `409 email_taken` if the address is in use.

### POST /account/v1/me/email/verification

Feature-gated by `email_verification`. Re-sends the verification email → `202 {"status":"verification_sent"}`. Registration sends one automatically.

### PUT /account/v1/me/avatar

Raw image body (JPEG, PNG, GIF or WebP, max `CORTEX_AVATAR_MAX_BYTES`). The image is center-cropped, resized and re-encoded as JPEG (metadata stripped). Returns the account with `avatar_url`. `400` for non-images, `413` when too large.

### DELETE /account/v1/me/avatar

→ `204`.

Avatar routes are feature-gated by `avatars`.

### GET /avatars/v1/{userID}/{file}

Public, content-addressed JPEG with `Cache-Control: public, max-age=31536000, immutable` and `ETag`.

### GET /account/v1/me/keys

```json
{ "kdf_version": 1, "kdf_salt": "<base64> | null", "public_key": "<base64> | null", "encrypted_private_key": "<base64> | null", "has_recovery": true }
```

### PUT /account/v1/me/keys

Sets up or replaces the account identity (also upgrades `kdf_version: 0` accounts). Rate limited.

```json
{
  "current_password": "current password (raw for kdf 0, auth key for kdf 1)",
  "password": "new auth key",
  "kdf_salt": "<base64 16 bytes>",
  "public_key": "<base64 32 bytes>",
  "encrypted_private_key": "<base64>",
  "recovery_wrapped_private_key": "<base64, optional>"
}
```

→ `200` with the `GET /account/v1/me/keys` shape. Sets the password hash from `password` and
`kdf_version: 1`. Sessions stay valid. When `public_key` differs from the stored one, the user's
vault keys are deleted and `member_key_changed` is emitted in every vault they belong to; the
recovery wrap is cleared unless a new one is sent. `401 invalid_credentials` for a wrong current
password, `400` for invalid key material.

### PUT /account/v1/me/recovery

`{ "recovery_wrapped_private_key": "<base64>" }` → `200` keys shape. `409 keys_not_set_up` for
`kdf_version: 0` accounts.

### PUT /account/v1/me/public-key

`{ "public_key": "<base64 32-byte X25519 key>" }` → `204`. Legacy accounts only; `409` when a
`kdf_version: 1` account sends a different key (use `PUT /account/v1/me/keys`).

### GET /users/v1/{userID}/public-key

→ `{ "user_id": "uuid", "public_key": "<base64>" }`.

### GET /account/v1/overview

Everything a client needs after login in one call.

**Response** `200`

```json
{
  "account": { "...GET /account/v1/me/ shape" },
  "devices": [
    {
      "id": "uuid",
      "device_name": "string",
      "device_type": "string",
      "last_seen_at": "string",
      "created_at": "string",
      "revoked": false,
      "is_current": true,
      "vault_cursors": [ { "vault_id": "uuid", "last_event_id": 42, "synced_at": "RFC3339" } ]
    }
  ],
  "vaults": [ { "...vault object" } ],
  "invites": [ { "...invite object (pending invites for the caller)" } ],
  "subscription": { "...GET /subscription/v1/status shape, only when subscriptions are enabled" }
}
```

`vault_cursors` come from `PUT /sync/v1/vaults/{vaultID}/cursor`.

### Email links (public)

Feature-gated: verify and confirm-change by `email_verification`, forgot/reset by `password_reset`.

| Route | Description |
|-------|-------------|
| `GET /account/v1/email/verify?token=` | HTML page with a confirm button |
| `POST /account/v1/email/verify` | Form (`token`) or JSON `{ "token" }`; JSON returns `200` / `404` |
| `GET /account/v1/email/confirm-change?token=` | HTML page with a confirm button |
| `POST /account/v1/email/confirm-change` | Form or JSON; `409` if the address was taken meanwhile |
| `POST /account/v1/password/forgot` | `{ "email" }` → always `202` |
| `GET /account/v1/password/reset?token=` | HTML page with an "Open Cortex" button linking `cortex://reset-password?token=<token>&server=<CORTEX_SERVER_PUBLIC_URL>` and the token as copyable text |
| `POST /account/v1/password/reset/prepare` | JSON `{ "token" }` → `{ "public_key", "recovery_wrapped_private_key" }` (both nullable); does not consume the token; `404` for unknown or expired tokens |
| `POST /account/v1/password/reset` | JSON `{ "token", "password", "kdf_salt"?, "public_key"?, "encrypted_private_key"?, "recovery_wrapped_private_key"? }` (form posts are still accepted); revokes every session |

Reset with `kdf_salt` makes the account `kdf_version: 1` (`public_key` and `encrypted_private_key`
required). If the public key differs from the stored one, the user's vault keys are deleted and
`member_key_changed` is emitted. Without key fields (old clients) the account returns to
`kdf_version: 0`, its identity and recovery wrap are cleared, its vault keys are deleted and
`member_key_changed` is emitted.

Tokens are single use; GET never consumes them.

## Sharing

### Invite links

`POST /vaults/v1/{vaultID}/invites/` accepts an optional `encrypted_vault_key` and returns `invite_token` and `invite_url` once (replaying the same `id` issues a new token and re-sends the email). When mail is enabled the invitee receives an email with the same link.

| Route | Auth | Description |
|-------|------|-------------|
| `GET /invites/v1/{token}` | public | `{ vault_name, inviter_name, role, expires_at }`; `404`, `409` (used) or `410` (expired) |
| `GET /invite/{token}` | public | Landing page that opens `cortex://invite/{token}` keeping the URL fragment; shows vault, members, role and expiry |
| `POST /invite/{token}/decline` | public | Form post from the landing page; deletes the invite and emails the inviter |
| `POST /vaults/v1/invites/accept` | yes | `{ "token" }` or `{ "invite_id" }`; returns the vault plus `encrypted_vault_key` when the invite carried one |

### Vault keys

| Route | Permission | Description |
|-------|------------|-------------|
| `GET /vaults/v1/{vaultID}/keys/me` | member | Own wrapped VEK |
| `PUT /vaults/v1/{vaultID}/keys/me` | member | `{ "encrypted_key" }` re-wrap own key |
| `GET /vaults/v1/{vaultID}/keys/pending` | admin | Members without a key: `[{ user_id, role, public_key?, display_name, email, avatar_url? }]` |
| `PUT /vaults/v1/{vaultID}/keys/{userID}` | admin | Grant a wrapped VEK to a member; emits `key_granted` |

`encrypted_key` is opaque to the server; clients seal the VEK to the member's X25519 public key.
`POST /sync/v1/vaults/{vaultID}/encryption` requires admin. Every member (guests included) receives
the same vault key; path access is enforced by the server, not by encryption.
