# Cortex Sync Server

Go HTTP sync backend for the Cortex note-taking app. Handles multi-device sync, multi-user shared vaults, real-time SSE notifications, JWT auth with rotating refresh tokens, device identity management, and E2E encryption (server never sees plaintext).

## Quick Reference

```bash
make build        # Build binary to ./bin/cortex-sync
make run          # Build + run
make test         # All tests with -race
make test-unit    # ./internal/... with -race
make test-e2e     # Multi-instance E2E suite (Docker: postgres, s3, mailpit)
make sqlc         # Regenerate internal/platform/postgres/db from queries + migrations
make lint         # golangci-lint
make fmt          # gofmt + goimports
make docker-up    # PostgreSQL 16 + SeaweedFS S3 via docker-compose
make docker-down  # Stop containers
make docker-reset # Destroy volumes + restart
make migrate-up   # Run migrations
make migrate-down # Rollback one migration
cortex-sync migrate up      # Run migrations in the container image
cortex-sync migrate version # Print migration version
docker compose --profile replicas up -d  # Adds sync-replica (:8081) + Mailpit (:1025/:8025)
```

Local dev envs live in `.env.local`, copied from `.env.local.example`. The Makefile loads it by
default for `make run` and Docker targets; use `make ENV_FILE=.env.stripe docker-up` for alternate
credential sets. Direct Docker Compose usage should pass `--env-file .env.local`.

## Project Structure

```
sync/
  cmd/server/main.go          # Entry point: config.Load, `migrate` subcommand, server.Run
  internal/
    domain/                    # Entities, errors, RBAC (permission.go), path access scope (scope.go); no internal imports
    auth/                      # Register/login/refresh/logout, bcrypt hasher, JWT generator
    account/                   # Profile, password, email change/verification, reset, avatar
    device/                    # Device list/revoke/rename/sync cursor
    vault/                     # Vaults, members, groups + path access (access.go), invites, keys, encryption
    file/                      # Snapshots, deltas, list/batch/history/changes, cursor, quota
    realtime/                  # SSE broker + handler, collab rooms (WebSocket), tickets, binary frames
    share/                     # E2E public note links: service, HTTP, /s/{id} viewer (embedded assets)
    subscription/              # Optional Stripe billing + entitlement middleware
    devops/                    # Optional Discord notifications
    job/                       # Cleaner, blob janitor, mail worker
    server/                    # Wiring (server.go), routes.go, overviews, readiness, background jobs
    platform/
      config/                  # Env-only config (caarlos0/env), storage.go resolves S3 settings
      feature/                 # Feature set, feature.Require gate, /meta/v1/capabilities
      httpx/                   # JSON helpers, auth + device middleware, claims
      authz/                   # Cached authorizer (role + compiled path access scope)
      bus/                     # Signal bus interface + in-memory impl, vault event helpers
      lifecycle/               # Drain/stream tracking for graceful shutdown
      pages/                   # HTML pages for email links and invite landing
      secret/                  # Random tokens + hashes
      mail/                    # Email templates (embedded), SMTP and log mailers
      avatar/                  # Avatar decoding, crop and JPEG re-encoding
      middleware/              # RequestID, Logger, Recoverer, BodyLimit, RateLimit, Metrics
      postgres/                # Repositories, listener, signal bus, migrator; queries/*.sql -> db/ (sqlc)
      s3/                      # S3-compatible blob storage (minio-go)
      fake/                    # In-memory repositories (tests + CORTEX_USE_FAKE_REPOS)
      validate/                # Input validation helpers
  migrations/                  # golang-migrate SQL files, embedded via migrations/embed.go
  test/e2e/                    # Multi-instance E2E suite (build tag e2e) + run.sh
  deploy/local/                # Local S3 credentials
  deploy/production/           # Caddy + 2 replicas + R2 compose template and scripts
  docs/                        # api.md, self-hosting.md, sync-client-guide.md
  sqlc.yaml                    # sqlc config
  docker-compose.yml           # PostgreSQL 16 + SeaweedFS S3; `replicas` profile adds sync-replica + Mailpit
  Dockerfile                   # Multi-stage alpine build
  Makefile                     # Build/test/sqlc/migrate/docker targets
```

## Architecture

Feature packages own their HTTP handlers (`http.go`, `*_http.go`), service (`service.go` or one
file per service) and the interfaces they consume (`ports.go`). Services are built from a `Deps`
struct (`NewService(Deps{...})`). Shared infrastructure lives under `platform/`.

- **domain/**: Pure Go structs, error sentinels and the permission matrix. No imports from other internal packages.
- **Feature packages** depend on `domain` and `platform/*` (plus `devops` for notifications) and
  do not import each other. Only `server/` wires them together and serves the cross-feature
  overviews.
- **platform/postgres**: implements the feature ports using sqlc-generated code in `postgres/db`.
- **server/**: `server.New` builds services from `config` + `feature.Set`; `routes.go` mounts
  handlers and wraps optional routes with `feature.Require` (`404 feature_disabled` when off).

## Code Conventions

- **No comments in code.** Code must be self-documenting via naming.
- **Billing is optional and backend-only.** Self-hosted deployments keep
  `CORTEX_SUBSCRIPTION_ENABLED=false`; Cortex Cloud enables subscription enforcement through the
  sync backend.
- All Go files use standard `gofmt` formatting.
- Error sentinels live in `domain/errors.go`. Handlers map domain errors to HTTP status codes.
- Request/response structs are defined in the package's handler files with json tags.
- SQL lives in `platform/postgres/queries/*.sql`; never hand-edit `platform/postgres/db/`.
- Binary data (encrypted keys, deltas) uses base64 encoding over JSON, raw binary over `application/octet-stream`.
- UUIDs are strings in JSON, `uuid.UUID` in Go.
- Timestamps are RFC3339 in JSON responses. Vault timestamps use `2006-01-02T15:04:05Z` layout; file timestamps use `time.RFC3339`.

## Auth Model

- JWT access token (15min, `Authorization: Bearer <token>`)
- Opaque refresh token (90 days, rotated on every use, reuse detection revokes entire token family)
- Device identity via `X-Device-ID` header (UUID, client-generated, required for all authenticated endpoints)
- Auth middleware extracts `AccessTokenClaims{UserID, Email}` into context
- Device middleware extracts `X-Device-ID` into context
- Access tokens carry the device id (`did` claim); `X-Device-ID` must match it or the request is rejected with `401 device_mismatch`
- A device id is owned by one user: logging in with a device id owned by another account returns `409 device_conflict`, and the client must generate a new device id
- Refresh tokens are consumed atomically; reuse or a revoked device revokes the whole token family
- Revoked devices are reactivated on successful login with valid credentials
- Client clears device_id from keychain on logout; a fresh UUID is generated on next login

## E2E Encryption

Server is "dumb encrypted storage." It stores encrypted blobs and encrypted keys. It never sees plaintext content.

- Per-user X25519 identity; the private key is wrapped client-side (Argon2id + HKDF, `kdf_version` 1)
  and the server only stores `kdf_salt`, `public_key`, `encrypted_private_key` and the recovery wrap.
  Clients send an HKDF-derived auth key as `password` (`POST /auth/v1/prelogin` returns the salt;
  unknown emails get a deterministic fake salt from HMAC(JWT secret)). `kdf_version` 0 is legacy.
- Vault master key sealed per-member to their X25519 public key (`vault_keys`, opaque); a changed
  identity drops the user's vault keys and emits `member_key_changed`
- Per-file ephemeral key encrypted with vault master key
- XChaCha20-Poly1305 for symmetric encryption
- All merge logic is client-side (three-way merge via diff-match-patch)

## Vault Roles (RBAC)

`owner` > `admin` > `editor` > `viewer` > `guest`

Permissions are a static bitmask matrix in `domain/permission.go` (`PermFileRead`, `PermFileWrite`,
`PermMemberRead`, `PermMemberManage`, `PermInviteManage`, `PermKeyGrant`, `PermVaultUpdate`,
`PermVaultDelete`). Services check access only through `platform/authz` (`Require`, `RequirePath`,
`Access`); never compare roles by hand.

- **owner**: everything, including deleting the vault and promoting admins
- **admin**: members, invites, key grants and vault settings; can only manage roles below its own
- **editor**: read + write files
- **viewer**: read-only (collab updates from viewers are dropped)
- **guest**: no access by default; only paths granted through access entries. Created only through
  guest invites (`grants` in the invite become `user` entries on accept). Gated by `CORTEX_FEATURE_GUESTS`.

### Groups and path access (always on)

- `vault_groups` / `vault_group_members`, `vault_path_rules` (path, `restricted`) and
  `vault_path_access` (principal `user|group`, level `view|edit`). Only admins/owners mutate them
  (`/vaults/v1/{id}/groups`, `GET/PUT /vaults/v1/{id}/access`).
- `domain.CompileScope` builds the effective `Scope` (All / Base level / Rules, deepest match):
  owner/admin `All`; base editor `edit`, viewer `view`, guest `none`; restricted rules use only
  their entries, others take max(entry, inherited). `RequirePath` decides file reads/writes by the
  scope, so a viewer with an `edit` entry can write there. The authorizer loads the member row plus
  one `ListUserRuleAccess` query (rules + the user's direct and group entries).
- Folder rule `A/` covers the prefix, file rule is exact. File rename moves exact-file rules and
  carries a folder rule to a new empty folder (`domain.FolderCarryCandidates`); delete removes the
  exact-file rule; both inside the file transaction (`file.Deps.Access`).
- List/changes/SSE replay prefilter in SQL when base is `none`, then filter in Go (`Scope.CanRead`).
- `authz.NewCached` caches access per vault/user for 30s. Every access mutation (groups, rules, role
  change, removal, guest accept) publishes a `vault_access` signal: every instance invalidates the
  whole vault and emits the `access_changed` SSE event; open SSE streams recompute their scope and
  collab sockets whose read/write access changed are closed (`4403`).
- A role change or removal also publishes the per-user `access` signal that disconnects the
  member's SSE and collab sockets on every instance.

## Tenancy

- `CORTEX_TENANCY_MODE=single` (default) or `multi`. Multi enables per-vault storage quotas
  (`storage_quota_bytes`, set by instance admins via `PATCH /vaults/v1/{id}`, `507 quota_exceeded`)
  and bills `/sync/v1/vaults/{id}/...` against the vault owner's subscription.
- The first registered user is the instance admin (`users.is_instance_admin`).
  `CORTEX_VAULT_CREATION=admins` limits vault creation to instance admins.
- Vault settings: `icon`, `allowed_email_domains` (checked on invite and accept), `allow_guests`.
- `POST /vaults/v1/` and `POST /vaults/v1/{id}/invites/` accept a client `id` for idempotent
  retries; replaying an invite rotates its token.

## File Sync Model

- **Snapshots**: Full encrypted file content stored in S3-compatible storage (SeaweedFS locally, R2/S3 in production). Versioned per file.
- **Deltas**: Small encrypted diffs (0.5-5KB) stored as BYTEA in PostgreSQL. Client computes deltas locally.
- **file_latest**: Tracks current version, checksum, size, content_type, deleted flag per file per vault. Also tracks `LatestSnapshotVersion` and `SizeBytes` for delta policy evaluation.
- **sync_events**: Append-only log of file mutations. Used for SSE replay and polling fallback.
- Upload: `POST /sync/v1/vaults/{vaultID}/files` with `application/octet-stream` body, metadata in `X-File-Path` (percent-encoded when `X-File-Path-Encoding: uri`), `X-Local-Hash`, `X-Content-Type`, optional `X-Base-Version` headers. The blob is stored before the metadata transaction; `file_latest` rows are locked with `SELECT ... FOR UPDATE` on every mutation.
- Rename moves snapshots and deltas to the new path, so history and downloads follow the file.
- Download: `GET /sync/v1/vaults/{vaultID}/files?path=...&version=...` returns raw binary with metadata in response headers.
- Batch download: `POST /files/batch` streams `application/vnd.cortex.batch` frames
  (`[u32 BE header len][JSON header][u64 BE body len][body]`), max 200 items / 64 MB.
- List: `GET /files/list` supports `limit`/`cursor` (`X-Next-Cursor`), returns `X-Latest-Event-ID`
  and a weak `ETag` (first page) so `If-None-Match` gets `304`.
- `PUT /sync/v1/vaults/{id}/cursor` stores a per-device, per-vault cursor (`device_vault_cursors`).
- Blobs are tracked in `blob_uploads_pending` until the metadata commits; deletions go through the
  `blob_deletions` queue. `job.BlobJanitor` deletes orphans and drains the queue with backoff.
- **Delta-first sync**: Server signals `needs_snapshot: true` in UploadDelta response when client should create a new snapshot. Two triggers: max delta count (default 10) and accumulated delta size ratio vs last snapshot size (default 0.5). When a new snapshot is uploaded, old deltas (before snapshot version) are cleaned up automatically.

## Multi-instance

Every instance is stateless; PostgreSQL is the source of truth and the message bus.

- One `LISTEN` connection per instance (`postgres.Listener`) handles `sync_events`,
  `cortex_signals` and `subscription_updates`.
- `bus.Bus` (`postgres.SignalBus`) carries ephemeral vault events (`member_joined`,
  `key_granted`, `collab_active`...), access revocations and collab relay frames. Payloads above
  7KB go through the `realtime_relay` table and NOTIFY only carries the row id.
- Sync events lock the vault row (`FOR NO KEY UPDATE`) before inserting, so event ids are
  monotonic in commit order per vault and `Last-Event-ID` replay has no gaps.
- Background jobs (`job.Cleaner`, `job.BlobJanitor`, `job.MailWorker`) are safe on N replicas:
  cleaners take a transaction-scoped advisory lock, queue workers claim rows with
  `FOR UPDATE SKIP LOCKED`. The mail worker only runs when a mailer is configured.
- Collab tickets are HMAC-signed (stateless), valid on any instance.
- Shutdown drains: `/ready` returns 503, waits `CORTEX_SERVER_DRAIN_DELAY`, closes SSE/WebSocket
  streams (collab flushes buffered updates), then shuts down HTTP.

## Accounts and Email

- `/account/v1/me` profile (name, locale), password change (revokes other devices; kdf 1 accounts
  send the new salt + re-wrapped private key), email change (confirmation sent to the new address,
  notice to the old one), email verification, forgot/reset password, avatar (re-encoded to a 256px
  JPEG, served immutable from `/avatars/v1/...`), identity keys (`GET/PUT /me/keys`,
  `PUT /me/recovery`) and the legacy X25519 public key route.
- Password reset emails open a page that deep links to `cortex://reset-password?token=&server=`;
  the app calls `POST /account/v1/password/reset/prepare` (recovery wrap, token not consumed) and
  then resets with the new key material.
- Emails are written to `email_outbox` in the same transaction as the action and sent by
  `job.MailWorker` with exponential backoff. Templates live in `platform/mail/templates/{en,pt-BR}`.
  Each template defines `subject` and `text` plus the HTML blocks `preheader`, `heading`, `body`,
  `note` and optionally `cta`, `details` and `why`; `layout.html` owns the card, the pill CTA (shown
  only when `Link` is set), the copyable fallback link, dark mode and the footer. The renderer adds
  `Locale` to the HTML data.
- `CORTEX_MAIL_DRIVER=none` (default) disables mail: invites still return `invite_url`, email
  change applies immediately (`200 {"status":"changed"}`), verification and password reset routes
  return `404 feature_disabled`.
- Links in emails open GET pages that only render a form; the token is consumed on POST so email
  scanners cannot burn it. GET pages peek the token first, so an expired or used link shows the
  expired page (reset links offer a form that posts to `/password/forgot`).
- `platform/pages` renders every callback page from one `card` template (Design System callback
  layout, en/pt-BR by `Accept-Language`) with a strict CSP (no inline styles or scripts). Shared
  assets (`cortex.css` tokens, Geist fonts under OFL, `logo.png`, `pages.js`) are embedded and
  served at `/assets/v1/*`; emails use `{PublicURL}/assets/v1/logo.png`.
- The reset page never posts a password: it deep links to the app, because a reset without new
  key material drops the user's identity and every vault key.

## Sharing and Keys

- Invites carry a random token (only its hash is stored). The email links to `/invite/{token}`,
  a landing page that opens `cortex://invite/{token}` and preserves the URL fragment so the client
  can put an E2E secret there. The page shows the vault, member count, role and expiry, and
  `POST /invite/{token}/decline` deletes the invite and emails the inviter (`invite_declined`).
  Replaying an invite `id` rotates the token and re-sends the email with the new link.
- `POST /vaults/v1/invites/accept` accepts `{token}` (any account) or `{invite_id}` (email match).
- `encrypted_vault_key` is optional on vault creation and invites. Members without a key appear in
  `GET /vaults/v1/{id}/keys/pending` with their public key, name, email and avatar; an admin seals
  the VEK to that key and calls `PUT /vaults/v1/{id}/keys/{userID}` (`key_granted`).
  `GET /sync/v1/vaults/{id}/encryption` reports `has_member_keys`.

## Public Note Links

- `internal/share` owns `note_shares` (one row per vault path). The client encrypts a gzip JSON
  bundle with a random link key, uploads ciphertext, and keeps the key in the URL fragment; the
  server stores `wrapped_key` (link key wrapped with the VEK) so members can republish.
- Sharing needs `PermNoteShare` (owner, admin, editor) and the vault's `allow_public_links`.
  Turning the setting off makes existing links 404. File delete revokes the link and rename moves
  it inside the file transaction (`file.Deps.Shares`). Content counts toward vault storage and
  quota; old revisions and revoked links go through `blob_deletions`.
- `/s/{id}` serves a static viewer with `script-src 'self'` and `frame-ancestors 'none'`; decrypted
  HTML renders in an iframe sandboxed with `allow-same-origin` but never `allow-scripts`, so the
  page can size it and build the table of contents. The header shows the bundle's optional
  `author` and `tags`, the share `updated_at`, read time, a Paper/Ink toggle and copy link. Long-lived SSE and collab streams re-check subscription entitlement on
  every ping (`realtime.StreamGuard`) and close when it lapses.

## Realtime Collaboration

- WebSocket `GET /sync/v1/vaults/{vaultID}/collab?path=&ticket=` (`github.com/coder/websocket`).
  JSON frames by default; `&frames=binary` sends `sync_step1/sync_step2/update/awareness` as binary
  (`collab_frames.go`): client→server `[opcode][payload]`, server→client
  `[opcode][client_id len][client_id][payload]`, opcodes 1–4. Control messages stay JSON; JSON and
  binary clients can share a room.
- On join the server sends `welcome` (`client_id`, `up_to`, `peers`), then the compacted state
  (`sync_step2`), persisted updates, this instance's unflushed updates and finally `synced`.
- Payloads are always client-side encrypted (`[0x01][nonce][AES-GCM ct||tag]`, key
  `HKDF(VEK, "cortex/collab/v1")`); the server only relays and stores opaque bytes.
- Every relayed JSON frame carries a server-stamped `from` (user id, name, avatar, `can_write`);
  binary frames carry the sender's `client_id`, so awareness/cursors cannot be spoofed. `peer_joined` / `peer_left` announce presence.
- Frames are relayed to other instances only when the room has remote peers (tracked through the
  `collab_presence` table), so a single instance pays no bus cost.
- `compact` must send `up_to` (from `welcome`); only updates with id <= `up_to` are deleted.
- Rename moves the collab document with the file; delete removes it in the same transaction.

## SSE

- Endpoint: `GET /sync/v1/vaults/{vaultID}/events`
- Event types: `file_created`, `file_updated`, `file_deleted`, `file_renamed` (with id) and the
  ephemeral `collab_active`, `collab_inactive`, `member_joined`, `member_role_changed`,
  `member_removed`, `key_granted`, `member_key_changed`, `access_changed` (no id), plus `ping`
- `Last-Event-ID` header for replay on reconnect
- 30s ping interval
- Slow subscribers are disconnected when their buffer is full; clients reconnect with `Last-Event-ID`
- Events reach the broker only through PostgreSQL `NOTIFY` (id only) + `GetByID`, so every instance emits the same payload exactly once; fake-repo mode publishes directly
- The handler subscribes before replaying, and drops events already sent during replay
- Polling fallback: `GET /sync/v1/vaults/{vaultID}/changes?since=<event_id>`
- File events are filtered by the caller's scope; guests only get member/key events about themselves

## Testing

```bash
make test       # go test ./... -race
make test-e2e   # docker compose --profile replicas up postgres s3 mailpit, then test/e2e/run.sh
```

- Unit and service tests live next to the code and use `platform/fake` repositories.
- Router-level HTTP tests live in `internal/server/*_test.go` (`httptest.NewRecorder`, and
  `httptest.NewServer` for SSE/WebSocket). SSE tests use an `sseReader` (single goroutine owns the
  scanner); file tests use `doRawRequest` for binary uploads.
- Bcrypt cost 4 in tests (`auth.NewBcryptHasherWithCost(4)`).
- E2E (`test/e2e`, build tag `e2e`): `run.sh` migrates and starts 3 binaries against real
  PostgreSQL/S3 — two instances (:8080, :8081) with SMTP to Mailpit and a minimal multi-tenant
  instance (:8082) with mail, collab, avatars and guests off. Covers multi-instance SSE/collab,
  sync contracts, guest scope, groups/path access, prelogin/kdf/key upgrade/reset, orphaned uploads,
  the minimal feature set, quotas and graceful drain.
  Runs as the `e2e` job in `.github/workflows/ci.yml`.

## Config

All config via environment variables with `CORTEX_` prefix, parsed by `caarlos0/env` in
`platform/config` (no config file). Invalid combinations fail at boot.

| Variable | Default | Description |
|----------|---------|-------------|
| CORTEX_SERVER_ENV | development | Runtime mode: development or production |
| CORTEX_SERVER_HOST | 0.0.0.0 | Bind address |
| CORTEX_SERVER_PORT | 8080 | Listen port |
| CORTEX_SERVER_SHUTDOWN_TIMEOUT | 15s | Graceful shutdown timeout |
| CORTEX_SERVER_READ_TIMEOUT | 15s | HTTP read timeout |
| CORTEX_SERVER_READ_HEADER_TIMEOUT | 5s | HTTP header read timeout |
| CORTEX_SERVER_IDLE_TIMEOUT | 60s | HTTP idle timeout |
| CORTEX_SERVER_TRUST_PROXY_HEADERS | false | Trust X-Forwarded-For/X-Real-IP for rate limiting |
| CORTEX_SERVER_PUBLIC_URL | http://localhost:PORT | Public base URL used in email links (required in production) |
| CORTEX_SERVER_DRAIN_DELAY | 0s | Time `/ready` reports 503 before streams are closed on shutdown |
| CORTEX_RATE_LIMIT_SENSITIVE_REQUESTS_PER_SECOND | 1 | Per-IP rate for login, register and account token endpoints |
| CORTEX_RATE_LIMIT_SENSITIVE_BURST | 10 | Burst for the sensitive limiter |
| CORTEX_TENANCY_MODE | single | `single` or `multi` (quotas, owner-billed vaults) |
| CORTEX_VAULT_CREATION | everyone | `everyone` or `admins` (instance admins only) |
| CORTEX_AUTH_REGISTRATION_MODE | open | `open`, `first-user` or `closed` |
| CORTEX_FEATURE_COLLAB | true | Collab WebSocket, tickets, peers, presence |
| CORTEX_FEATURE_AVATARS | true | Avatar upload and serving |
| CORTEX_FEATURE_GUESTS | true | Guest invites and the guest role |
| CORTEX_FEATURE_PUBLIC_LINKS | true | Public note links and the `/s/{id}` viewer |
| CORTEX_FEATURE_EMAIL_VERIFICATION | auto | `auto` (on when mail is enabled), `true`, `false`; `true` without mail fails at boot |
| CORTEX_FEATURE_PASSWORD_RESET | auto | Same values as above |
| CORTEX_METRICS_ENABLED / _PATH | true / /metrics | Prometheus endpoint |
| CORTEX_RATE_LIMIT_REQUESTS_PER_SECOND / _BURST | 100 / 200 | Global per-IP limiter |
| CORTEX_MAIL_DRIVER | none | `none` (mail off), `log` (prints emails) or `smtp` |
| CORTEX_MAIL_HOST / _PORT | / 587 | SMTP server |
| CORTEX_MAIL_USERNAME / _PASSWORD | | SMTP credentials |
| CORTEX_MAIL_FROM | Cortex <no-reply@localhost> | Sender address |
| CORTEX_MAIL_TLS_MODE | starttls | `starttls`, `tls` (implicit, port 465) or `none` |
| CORTEX_MAIL_TIMEOUT | 15s | SMTP timeout |
| CORTEX_AVATAR_MAX_BYTES | 5242880 | Max avatar upload size |
| CORTEX_AVATAR_SIZE | 256 | Avatar output size in pixels |
| CORTEX_SERVER_MAX_JSON_BODY | 134217728 | Max JSON request body bytes |
| CORTEX_SERVER_MAX_UPLOAD_BODY | 104857600 | Max octet-stream upload bytes |
| CORTEX_CORS_ALLOWED_ORIGINS | * | Comma-separated CORS origins |
| CORTEX_CORS_ALLOW_CREDENTIALS | false | Enable credentialed browser CORS |
| CORTEX_DATABASE_URL | postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable | PostgreSQL connection |
| CORTEX_DATABASE_MAX_CONNS | 25 | Connection pool max |
| CORTEX_DATABASE_MIN_CONNS | 5 | Connection pool min |
| CORTEX_DATABASE_MIGRATIONS_PATH | | Optional golang-migrate source URL; empty uses the embedded migrations |
| CORTEX_STORAGE_BACKEND | local | Storage backend: local, r2, or s3 |
| CORTEX_STORAGE_ENDPOINT | localhost:9000 | Object storage endpoint |
| CORTEX_STORAGE_ACCESS_KEY | minioadmin | Object storage access key |
| CORTEX_STORAGE_SECRET_KEY | minioadmin | Object storage secret key |
| CORTEX_STORAGE_BUCKET | cortex-snapshots | Bucket for file snapshots |
| CORTEX_STORAGE_REGION | us-east-1 | Storage region; use auto for R2 |
| CORTEX_USE_FAKE_REPOS | false | Use in-memory fake repos instead of PostgreSQL/S3 |
| CORTEX_AUTH_ACCESS_TOKEN_SECRET | change-me-in-production | JWT signing secret |
| CORTEX_AUTH_ACCESS_TOKEN_EXPIRY | 15m | Access token lifetime |
| CORTEX_AUTH_REFRESH_TOKEN_EXPIRY | 2160h | Refresh token lifetime (90 days) |
| CORTEX_AUTH_ISSUER | cortex-sync | JWT issuer claim |
| CORTEX_SYNC_MAX_DELTAS_BEFORE_SNAPSHOT | 10 | Max consecutive deltas before server signals needs_snapshot |
| CORTEX_SYNC_MAX_DELTA_SIZE_RATIO | 0.5 | Max accumulated delta size as ratio of last snapshot size |
| CORTEX_SYNC_MAX_FILE_SIZE | 104857600 | Max file size in bytes (0 disables check) |
| CORTEX_SYNC_MAX_SNAPSHOTS_PER_FILE | 50 | Max snapshot versions retained per file (0 disables pruning) |
| CORTEX_SYNC_EVENT_RETENTION | 720h | How long sync events are retained (0 disables cleanup) |
| CORTEX_COLLAB_MAX_PEERS_PER_ROOM | 10 | Max concurrent WebSocket peers per collab room |
| CORTEX_COLLAB_FLUSH_INTERVAL | 10s | How often buffered Yjs updates are flushed to the database |
| CORTEX_COLLAB_MAX_BUF_BYTES | 4194304 | Per-room buffered update bytes before a forced flush |
| CORTEX_SUBSCRIPTION_ENABLED | false | Enable Cortex Cloud subscription enforcement |
| CORTEX_SUBSCRIPTION_STRIPE_SECRET_KEY | | Stripe secret key, required in production when subscription is enabled |
| CORTEX_SUBSCRIPTION_STRIPE_PRICE_ID | | Stripe recurring price ID used for Checkout subscription line items |
| CORTEX_SUBSCRIPTION_STRIPE_WEBHOOK_SECRET | | Stripe webhook endpoint secret for `Stripe-Signature` validation |
| CORTEX_SUBSCRIPTION_CACHE_TTL | 60s | Maximum positive entitlement cache lifetime |
| CORTEX_SUBSCRIPTION_RENEWAL_GRACE | 48h | Grace added after each billing period before entitlement expires |
| CORTEX_DEVOPS_DISCORD_WEBHOOK_URL | | Optional Discord webhook for DevOps account, auth, and billing notifications |
| CORTEX_DEVOPS_DISCORD_USERNAME | Cortex DevOps | Discord webhook display username |
| CORTEX_DEVOPS_DISCORD_TIMEOUT | 5s | Discord webhook request timeout |

## Dependencies

- `go-chi/chi/v5` — HTTP router
- `go-chi/cors` — CORS middleware
- `golang-jwt/jwt/v5` — JWT generation/validation
- `google/uuid` — UUID generation
- `jackc/pgx/v5` — PostgreSQL driver + connection pool
- `golang-migrate/migrate/v4` — Migrations (embedded `iofs` source)
- `minio/minio-go/v7` — S3-compatible object storage client
- `coder/websocket` — WebSocket (collab real-time co-editing)
- `prometheus/client_golang` — Metrics
- `rs/zerolog` — Structured logging
- `caarlos0/env/v11` — Configuration
- `stripe/stripe-go/v86` — Optional billing
- `golang.org/x/image`, `golang.org/x/time` — Avatar decoding, rate limiting
- `stretchr/testify` — Test assertions
- `golang.org/x/crypto` — bcrypt

## Database

PostgreSQL 16. Schema is the sequence of `migrations/*.up.sql` (embedded in the binary; run with
`cortex-sync migrate up`).

Core tables: `users`, `devices`, `refresh_tokens`, `user_tokens`, `vaults`, `vault_members`,
`vault_groups`, `vault_group_members`, `vault_path_rules`, `vault_path_access`, `vault_invites`, `vault_keys`, `vault_encryption`, `file_snapshots`,
`file_deltas`, `file_latest`, `sync_events`, `device_vault_cursors`, `blob_uploads_pending`,
`blob_deletions`, `collab_documents`, `collab_updates`, `collab_presence`, `realtime_relay`,
`email_outbox`, `subscriptions`.

Queries are generated with sqlc:

1. Add or change a migration in `migrations/` (new numbered file; never edit applied ones).
2. Write the query in `internal/platform/postgres/queries/<area>.sql` with a `-- name: X :one|:many|:exec` header.
3. Run `make sqlc` (`go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate`, config `sqlc.yaml`).
   Schema input is `internal/platform/postgres/sqlc_prelude.sql` + `migrations/`; output goes to
   `internal/platform/postgres/db` (commit it).
4. Map `db` rows to domain types in the matching `platform/postgres/*.go` repository.

PG NOTIFY trigger on `sync_events` insert for real-time event propagation.

Subscription updates also emit PG NOTIFY on `subscription_updates`. The runtime entitlement checker
caches only positive access decisions and invalidates local cache entries from webhooks and database
notifications, so failed notifications fall back to the short TTL.

## Current State

- Auth, devices, vaults/members/invites/keys, file sync (snapshots, deltas, batch, list paging),
  SSE, collab (JSON + binary frames), accounts/email, optional Stripe billing and Discord DevOps
  notifications are implemented.
- Restructured into feature packages + `platform/`; old `internal/usecase`, `internal/adapter`,
  `internal/port` and viper config are gone. Postgres access is sqlc-generated.
- Feature flags and tenancy (`single`/`multi`) with `/meta/v1/capabilities`; guest role, groups and
  server-enforced path access; Argon2id/HKDF identity keys with recovery; vault tenant settings and storage quotas; `/account/v1/overview` and
  `/vaults/v1/{id}/overview`; gzip for JSON responses.
- Multi-instance safe (PG LISTEN/NOTIFY bus, advisory-locked jobs, HMAC collab tickets, drain on
  shutdown) and covered by the E2E suite in CI.
- `main.go` → `server.Run`; `CORTEX_USE_FAKE_REPOS=true` runs fully in memory (not allowed in production).
- Logger middleware logs non-2xx responses with Warn (4xx) or Error (5xx) level, including query, remote_addr, user_agent, and up to 1KB of response body

## API Reference

See `docs/api.md` for complete endpoint documentation.
