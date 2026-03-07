# Cortex Sync Server

Go HTTP sync backend for the Cortex note-taking app. Handles multi-device sync, multi-user shared vaults, real-time SSE notifications, JWT auth with rotating refresh tokens, device identity management, and E2E encryption (server never sees plaintext).

## Quick Reference

```bash
make build        # Build binary to ./bin/cortex-sync
make run          # Build + run
make test         # All tests with -race
make test-unit    # Domain + usecase tests only
make lint         # golangci-lint
make fmt          # gofmt + goimports
make docker-up    # PostgreSQL 16 + MinIO via docker-compose
make docker-down  # Stop containers
make docker-reset # Destroy volumes + restart
make migrate-up   # Run migrations
make migrate-down # Rollback one migration
```

## Project Structure

```
sync/
  cmd/server/main.go          # Entry point, wires all dependencies + routes
  internal/
    domain/                    # Entities, value objects, error sentinels (zero dependencies)
    port/                      # Repository + service interfaces (contracts)
    usecase/                   # Business logic (depends on port interfaces)
    config/                    # Viper-based config (env vars with CORTEX_ prefix)
    adapter/
      auth/                    # bcrypt hasher, JWT generator
      fake/                    # In-memory repository implementations (for tests + dev)
      handler/                 # HTTP handlers + middleware (chi router)
      middleware/              # RequestID, Logger, Recoverer
      postgres/                # PostgreSQL repository implementations + PG NOTIFY listener
      s3/                      # S3/MinIO blob storage adapter
      sse/                     # In-memory SSE broker
  migrations/                  # golang-migrate SQL files
  docs/                        # API reference documentation
  docker-compose.yml           # PostgreSQL 16 + MinIO + bucket init
  Dockerfile                   # Multi-stage alpine build
  Makefile                     # Build/test/migrate/docker targets
```

## Architecture

Clean Architecture with strict dependency direction: `domain` -> `port` -> `usecase` -> `adapter` -> `handler`.

- **domain/**: Pure Go structs and error sentinels. No imports from other internal packages.
- **port/**: Interfaces only (`repository.go` for data access, `service.go` for external services). No implementations.
- **usecase/**: Business logic. Depends only on port interfaces. Never imports adapters directly.
- **adapter/**: Implements port interfaces. Each adapter in its own subpackage.
- **handler/**: HTTP handlers. Depends on usecases. Handles JSON serialization, HTTP status codes, error mapping.

## Code Conventions

- **No comments in code.** Code must be self-documenting via naming.
- **No plans/billing system.** Only core sync functionality.
- All Go files use standard `gofmt` formatting.
- Error sentinels live in `domain/errors.go`. Handlers map domain errors to HTTP status codes.
- Request/response structs are defined in handler files with json tags.
- Binary data (encrypted keys, deltas) uses base64 encoding over JSON, raw binary over `application/octet-stream`.
- UUIDs are strings in JSON, `uuid.UUID` in Go.
- Timestamps are RFC3339 in JSON responses. Vault timestamps use `2006-01-02T15:04:05Z` layout; file timestamps use `time.RFC3339`.

## Auth Model

- JWT access token (15min, `Authorization: Bearer <token>`)
- Opaque refresh token (90 days, rotated on every use, reuse detection revokes entire token family)
- Device identity via `X-Device-ID` header (UUID, client-generated, required for all authenticated endpoints)
- Auth middleware extracts `AccessTokenClaims{UserID, Email}` into context
- Device middleware extracts `X-Device-ID` into context

## E2E Encryption

Server is "dumb encrypted storage." It stores encrypted blobs and encrypted keys. It never sees plaintext content.

- Vault master key encrypted per-user with X25519 public key
- Per-file ephemeral key encrypted with vault master key
- XChaCha20-Poly1305 for symmetric encryption
- All merge logic is client-side (three-way merge via diff-match-patch)

## Vault Roles

`owner` > `admin` > `editor` > `viewer`

- **owner**: Full control, can delete vault, only role that can
- **admin**: Manage members + invites, edit files, cannot delete vault or demote other admins
- **editor**: Read + write files
- **viewer**: Read-only

## File Sync Model

- **Snapshots**: Full encrypted file content stored in S3/MinIO. Versioned per file.
- **Deltas**: Small encrypted diffs (0.5-5KB) stored as BYTEA in PostgreSQL. Client computes deltas locally.
- **file_latest**: Tracks current version, checksum, size, content_type, deleted flag per file per vault. Also tracks `LatestSnapshotVersion` and `SizeBytes` for delta policy evaluation.
- **sync_events**: Append-only log of file mutations. Used for SSE replay and polling fallback.
- Upload: `POST /sync/v1/vaults/{vaultID}/files` with `application/octet-stream` body, metadata in `X-File-Path`, `X-Local-Hash`, `X-Content-Type` headers.
- Download: `GET /sync/v1/vaults/{vaultID}/files?path=...&version=...` returns raw binary with metadata in response headers.
- **Delta-first sync**: Server signals `needs_snapshot: true` in UploadDelta response when client should create a new snapshot. Two triggers: max delta count (default 10) and accumulated delta size ratio vs last snapshot size (default 0.5). When a new snapshot is uploaded, old deltas (before snapshot version) are cleaned up automatically.

## SSE

- Endpoint: `GET /sync/v1/vaults/{vaultID}/events`
- Event types: `file_created`, `file_updated`, `file_deleted`, `file_renamed`, `collab_active`, `collab_inactive`, `ping`
- `Last-Event-ID` header for replay on reconnect
- 30s ping interval
- Non-blocking publish (dropped if subscriber buffer full)
- Polling fallback: `GET /sync/v1/vaults/{vaultID}/changes?since=<event_id>`

## Testing

```bash
go test ./... -v -race -count=1
```

- **337 total tests**, all passing with `-race`.
- Usecase tests use fake repositories from `adapter/fake/`.
- Handler tests use `httptest.NewRecorder` for standard HTTP and `httptest.NewServer` for SSE (long-lived streaming).
- SSE handler tests use an `sseReader` struct pattern: single goroutine owns `bufio.Scanner`, sends parsed events to a channel.
- File handler tests use `doRawRequest` helper for binary uploads, `fileDoJSON` for JSON requests (separate from auth's `doRequestWithHeaders` which hardcodes `Content-Type: application/json`).
- Bcrypt cost=4 in tests for speed.

## Config

All config via environment variables with `CORTEX_` prefix, or `config.yaml` file.

| Variable | Default | Description |
|----------|---------|-------------|
| CORTEX_SERVER_HOST | 0.0.0.0 | Bind address |
| CORTEX_SERVER_PORT | 8080 | Listen port |
| CORTEX_SERVER_SHUTDOWN_TIMEOUT | 15s | Graceful shutdown timeout |
| CORTEX_DATABASE_URL | postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable | PostgreSQL connection |
| CORTEX_DATABASE_MAX_CONNS | 25 | Connection pool max |
| CORTEX_DATABASE_MIN_CONNS | 5 | Connection pool min |
| CORTEX_S3_ENDPOINT | localhost:9000 | MinIO/S3 endpoint |
| CORTEX_S3_ACCESS_KEY | minioadmin | S3 access key |
| CORTEX_S3_SECRET_KEY | minioadmin | S3 secret key |
| CORTEX_S3_BUCKET | cortex-snapshots | Bucket for file snapshots |
| CORTEX_S3_USE_SSL | false | Enable TLS for S3 |
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

## Dependencies

- `go-chi/chi/v5` — HTTP router
- `go-chi/cors` — CORS middleware
- `golang-jwt/jwt/v5` — JWT generation/validation
- `google/uuid` — UUID generation
- `jackc/pgx/v5` — PostgreSQL driver + connection pool
- `minio/minio-go/v7` — S3-compatible object storage client
- `nhooyr.io/websocket v1.8.17` — WebSocket (used for collab real-time co-editing)
- `rs/zerolog` — Structured logging
- `spf13/viper` — Configuration
- `stretchr/testify` — Test assertions
- `golang.org/x/crypto` — bcrypt

## Database

PostgreSQL 16. Schema in `migrations/000001_initial_schema.up.sql`.

12 tables: `users`, `devices`, `refresh_tokens`, `vaults`, `vault_members`, `vault_invites`, `vault_keys`, `file_snapshots`, `file_deltas`, `file_latest`, `sync_events`.

PG NOTIFY trigger on `sync_events` insert for real-time event propagation.

## Current State

- Sprints 1-7 complete (Foundation, Auth, Devices, Vaults+Members+Invites+E2E, Files, SSE, Hardening)
- Phase 1-3 complete: PostgreSQL + S3/MinIO adapters, delta-first sync, hash dedup, file size enforcement, per-device sync cursor, snapshot pruning, sync event retention, bulk file info endpoint
- Phase 4 complete: Real-time collaborative editing (Yjs CRDT over WebSocket)
  - Domain entities (`CollabUpdate`, `CollabDocument`), port interfaces, migration `000003_collab_tables`
  - WebSocket broker with in-memory update buffering (`BufferUpdate`/`FlushUpdates`) and `pgx.CopyFrom` batch persistence
  - Handler: join/leave, auth via `?token=`, read-only enforcement, ping/pong keepalive, client-triggered compaction
  - Bridge to file sync: publishes `collab_active` / `collab_inactive` SSE events on first/last peer
  - Awareness relay (cursor/presence) without persistence
  - REST presence endpoint (`GET /sync/v1/vaults/{vaultID}/collab/peers`)
  - Config-driven `MaxPeersPerRoom` and `FlushInterval`
- All 12 repository interfaces implemented in `adapter/postgres/` using pgx/v5
- S3/MinIO blob storage implemented in `adapter/s3/` using minio-go/v7
- PG NOTIFY listener in `adapter/postgres/listener.go` bridges DB events to SSE broker
- `main.go` wires real or fake repos based on `CORTEX_USE_FAKE_REPOS` env var (default: real)
- Error mapping: PG unique violation -> `ErrAlreadyExists`, no rows -> `ErrNotFound`, FK violation -> `ErrNotFound`
- No integration or E2E tests yet (only unit + handler tests with fake repos)

## API Reference

See `docs/api.md` for complete endpoint documentation.
