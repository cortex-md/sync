# Configuration

Cortex Sync is configured only through environment variables with the `CORTEX_` prefix (plus
`PORT`). There is no config file. Invalid values and unsafe combinations fail at boot with a
message that names the variable.

Most deployments only need the high-level keys below. Each one expands into a preset of
fine-grained values; any fine-grained variable you set explicitly always wins over the preset.
[`.env.example`](../.env.example) is the reference for a Render or self-hosted deployment.

On boot the server logs one `configuration` line with the effective deployment, bus, migration
mode, pool size, proxy mode, protection level, tenancy, vault quota, mail driver, storage backend,
registration mode, enabled features, max file size and max JSON body. Deprecated variables and
risky combinations are logged as warnings before it.

## High-level keys

| Variable | Values | Default | Effect |
|----------|--------|---------|--------|
| `CORTEX_DEPLOYMENT` | `single`, `cluster` | `single` | Bus, migrations and pool sizing (see [Deployment](#deployment)) |
| `CORTEX_PROXY` | `none`, `render`, `cloudflare`, `hops:N` | `none` | Where the real client IP comes from (see [Client IP](#client-ip)) |
| `CORTEX_PROTECTION` | `off`, `standard`, `strict` | `standard` | Rate limits, body limits, upload and connection caps (see [Protection](#protection)) |
| `CORTEX_FEATURES` | comma list of `collab`, `avatars`, `guests`, `public_links`, `metrics` | `collab,avatars,guests,public_links` | Optional features (see [Features](#features)) |
| `CORTEX_TENANCY_MODE` | `single`, `multi` | `single` | `multi` enforces vault quotas, account caps and bills sync by the vault owner |
| `CORTEX_TENANCY_DEFAULT_VAULT_QUOTA` | size such as `500MiB`, `1GiB`, `0` | `0` (single), `1GiB` (multi) | Quota stored on every new vault |
| `CORTEX_MAIL_DRIVER` | `none`, `log`, `smtp`, `resend` | `none` | Email delivery (see [Mail](#mail)) |
| `CORTEX_AUTH_REGISTRATION_MODE` | `open`, `first-user`, `closed` | `open` | Who can sign up |
| `CORTEX_VAULT_CREATION` | `everyone`, `admins` | `everyone` | `admins` lets only the instance admin (first user) create vaults |

Sizes accept a plain byte count or a number with `B`, `KB`, `MB`, `GB`, `TB` (powers of 1000) or
`KiB`, `MiB`, `GiB`, `TiB` (powers of 1024).

### Deployment

| | `single` | `cluster` |
|---|---|---|
| Event bus | In memory; no `LISTEN` connection | PostgreSQL `LISTEN/NOTIFY` (`cortex_signals`, `sync_events`) |
| Collab presence | In memory | `collab_presence` table, shared by every replica |
| Migrations | Run on boot (`CORTEX_DATABASE_MIGRATE_ON_START=true`) | Run `cortex-sync migrate up` before rolling out |
| Pool (`MAX_CONNS` / `MIN_CONNS`) | 5 / 0 | 10 / 1 |
| `CORTEX_DATABASE_MAX_CONN_IDLE_TIME` | 2m | 30m |
| Background job locks | None (only one process) | Transaction-scoped advisory locks |

`single` is for exactly one process. Running two `single` replicas against the same database
splits SSE, collab and access revocations between them. `cluster` supports any number of replicas
behind a load balancer without sticky sessions; when the `LISTEN` connection reconnects, each
replica catches up on missed sync events and rechecks access.

Setting `CORTEX_DATABASE_MIGRATE_ON_START=true` with `cluster` is allowed but logs a warning,
because every replica would migrate on boot.

### Client IP

Per-IP rate limits, login throttling and the request log use the resolved client IP.

| Value | Source |
|-------|--------|
| `none` | The TCP peer address. A warning is logged once if requests carry `X-Forwarded-For` |
| `render` | Same as `hops:1` |
| `cloudflare` | `CF-Connecting-IP`; falls back to the peer address |
| `hops:N` (1-10) | The Nth address from the right of `X-Forwarded-For`, i.e. the one appended by the outermost of N trusted proxies |

Only set a proxy mode when the proxy is the only way to reach the server, otherwise clients can
forge the header. `CORTEX_SERVER_TRUST_PROXY_HEADERS=true` still works as `hops:1` and logs a
deprecation warning.

### Protection

`CORTEX_PROTECTION` picks a preset. `off` disables every limiter, concurrency cap, upload budget
and login throttle, keeping only body and size limits. Rates are tokens per second with a burst
bucket; per-user limits fall back to the client IP for anonymous requests.

| Setting | Variable | `off` | `standard` | `strict` |
|---------|----------|-------|------------|----------|
| Per-IP requests (rate / burst) | `CORTEX_RATE_LIMIT_REQUESTS_PER_SECOND` / `_BURST` | off | 20 / 60 | 10 / 30 |
| Sensitive per-IP (prelogin, register, login, password, email, key setup, invite preview and decline) | `CORTEX_RATE_LIMIT_SENSITIVE_REQUESTS_PER_SECOND` / `_BURST` | off | 0.2 / 5 | 0.1 / 3 |
| Registrations per hour per IP | `CORTEX_PROTECTION_REGISTRATIONS_PER_HOUR` | off | 5 | 2 |
| Per-user reads (GET/HEAD) | `CORTEX_PROTECTION_USER_READ_PER_SECOND` / `_BURST` | off | 50 / 100 | 20 / 40 |
| Per-user writes | `CORTEX_PROTECTION_USER_WRITE_PER_SECOND` / `_BURST` | off | 20 / 60 | 8 / 20 |
| Per-user heavy routes (overviews, first list page, share publish, template writes) | `CORTEX_PROTECTION_USER_HEAVY_PER_SECOND` / `_BURST` | off | 1 / 5 | 0.5 / 3 |
| Public pages per IP (avatars, invite, share, template pages) | `CORTEX_PROTECTION_PUBLIC_PER_SECOND` / `_BURST` | off | 10 / 30 | 5 / 15 |
| Concurrent uploads per instance | `CORTEX_PROTECTION_MAX_CONCURRENT_UPLOADS` | off | 4 | 2 |
| Concurrent uploads per user | `CORTEX_PROTECTION_MAX_CONCURRENT_UPLOADS_PER_USER` | off | 2 | 1 |
| Concurrent batch downloads per user (each batch also streams at most 16 MiB in flight) | `CORTEX_PROTECTION_MAX_CONCURRENT_BATCHES_PER_USER` | off | 1 | 1 |
| Upload bytes per user per hour | `CORTEX_PROTECTION_UPLOAD_BYTES_PER_HOUR` | off | 500 MiB | 200 MiB |
| Max delta size | `CORTEX_PROTECTION_MAX_DELTA_BYTES` | 4 MiB | 1 MiB | 1 MiB |
| SSE streams per user | `CORTEX_PROTECTION_MAX_SSE_PER_USER` | off | 8 | 4 |
| SSE streams per instance | `CORTEX_PROTECTION_MAX_SSE_TOTAL` | off | 300 | 150 |
| Collab sockets per user | `CORTEX_PROTECTION_MAX_COLLAB_PER_USER` | off | 16 | 8 |
| Collab messages per socket (rate / burst) | `CORTEX_PROTECTION_COLLAB_MESSAGES_PER_SECOND` / `_BURST` | off | 50 / 200 | 30 / 100 |
| Failed logins before lockout (per email + IP) | `CORTEX_PROTECTION_LOGIN_FAILURES_BEFORE_LOCKOUT` | off | 5 | 3 |
| Concurrent bcrypt operations | `CORTEX_PROTECTION_MAX_CONCURRENT_PASSWORD_HASHES` | 4 | 2 | 1 |
| Max tracked limiter keys (LRU) | `CORTEX_PROTECTION_RATE_LIMIT_MAX_KEYS` | 50000 | 50000 | 20000 |
| Max JSON body | `CORTEX_SERVER_MAX_JSON_BODY` | 32 MiB | 2 MiB | 1 MiB |
| Max upload body | `CORTEX_SERVER_MAX_UPLOAD_BODY` | 100 MiB | 25 MiB | 10 MiB |
| Max file size | `CORTEX_SYNC_MAX_FILE_SIZE` | 100 MiB | 25 MiB | 10 MiB |

Notes:

- A value of `0` disables that limiter or cap. `CORTEX_PROTECTION_RATE_LIMIT_MAX_KEYS` must be at
  least 1000, and `MAX_DELTA_BYTES` and `MAX_CONCURRENT_PASSWORD_HASHES` must be positive.
- The login lockout starts at 1 minute once the threshold is reached and doubles with each further
  failure, up to 1 hour. A successful login clears it.
- Byte variables take plain byte counts (for example `26214400` for 25 MiB).
- Concurrency and stream caps are tracked per instance. The effective values are published in
  `GET /meta/v1/capabilities` under `limits`; see [docs/api.md](api.md#get-metav1capabilities)
  for the status codes each limit returns.

### Features

`CORTEX_FEATURES` lists the optional features that are on. When the variable is set, everything
not listed is off; `CORTEX_FEATURES=` (empty) turns every optional feature off. A disabled feature's
routes answer `404 {"code":"feature_disabled"}` and its background jobs are not started.

| Feature | Default | Effect when off |
|---------|---------|-----------------|
| `collab` | on | Collab WebSocket, tickets, peers and presence routes are disabled |
| `avatars` | on | Avatar routes are disabled and `avatar_url` is empty |
| `guests` | on | Guest invites and the `guest` role are disabled; existing guests lose access |
| `public_links` | on | Share routes, `/s/{id}`, `/share/v1/{id}` and public template routes are disabled |
| `metrics` | off | No `/metrics` route (`CORTEX_METRICS_PATH`) and no metrics middleware |

The old per-feature variables `CORTEX_FEATURE_COLLAB`, `CORTEX_FEATURE_AVATARS`,
`CORTEX_FEATURE_GUESTS`, `CORTEX_FEATURE_PUBLIC_LINKS` and `CORTEX_METRICS_ENABLED` are still
accepted. When one is set it overrides `CORTEX_FEATURES` for that feature and a deprecation warning
is logged.

Expose `/metrics` only on a private network or behind your own authentication.

### Tenancy

`CORTEX_TENANCY_DEFAULT_VAULT_QUOTA` is written to each new vault as `storage_quota_bytes`;
instance admins can change it per vault with `PATCH /vaults/v1/{id}`. Quotas are enforced
(`507 quota_exceeded`) in `multi` mode.

In `multi` mode these account caps apply (`403 limit_reached` when exceeded). In `single` mode
they default to `0` (unlimited) but explicit values still apply.

| Variable | `multi` default |
|----------|-----------------|
| `CORTEX_TENANCY_MAX_VAULTS_PER_USER` | 10 |
| `CORTEX_TENANCY_MAX_DEVICES_PER_USER` | 10 |
| `CORTEX_TENANCY_MAX_PENDING_INVITES_PER_VAULT` | 50 |
| `CORTEX_TENANCY_MAX_SHARES_PER_VAULT` | 200 |

### Mail

| Driver | Required variables |
|--------|--------------------|
| `none` | None. Email verification, password reset and email-addressed invites are off; invite links still work |
| `log` | None. Messages are printed to the server log (development) |
| `smtp` | `CORTEX_MAIL_HOST`, `CORTEX_MAIL_PORT`, `CORTEX_MAIL_FROM`; optional `CORTEX_MAIL_USERNAME`, `CORTEX_MAIL_PASSWORD`, `CORTEX_MAIL_TLS_MODE` |
| `resend` | `CORTEX_MAIL_RESEND_API_KEY`, `CORTEX_MAIL_FROM`; optional `CORTEX_MAIL_RESEND_URL` |

`CORTEX_FEATURE_EMAIL_VERIFICATION` and `CORTEX_FEATURE_PASSWORD_RESET` default to `auto` (on when
the driver is not `none`). Setting either to `true` with `CORTEX_MAIL_DRIVER=none` fails at boot.

## Boot checks

- `CORTEX_AUTH_ACCESS_TOKEN_SECRET` must be at least 32 characters and not a published default
  (`change-me-in-production`, `dev-only-change-this-token-secret-32`). A weak secret fails boot in
  every environment, except outside production when the public URL is `localhost`/`127.0.0.1`/`::1`
  or `CORTEX_USE_FAKE_REPOS=true`, where it only logs a warning. Generate one with
  `openssl rand -hex 32`.
- `CORTEX_CORS_ALLOW_CREDENTIALS=true` with `CORTEX_CORS_ALLOWED_ORIGINS=*` fails boot.
- In production (`CORTEX_SERVER_ENV=production`): the development database URL, fake repositories
  and a `localhost` public URL are rejected; subscriptions require the three Stripe values; the
  Discord webhook must be a `https://discord.com/api/webhooks/` URL.
- Remote storage (`r2`, `s3`) requires credentials and an existing bucket. R2 endpoints must end
  with `.r2.cloudflarestorage.com`, use SSL, and region `auto` or `us-east-1`.

## Fine-grained variables

Every variable below overrides the value derived from the high-level keys when set. "Preset" means
the default comes from `CORTEX_DEPLOYMENT` or `CORTEX_PROTECTION` as listed above.

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_SERVER_ENV` | `development` | `development` or `production` |
| `CORTEX_SERVER_HOST` | `0.0.0.0` | Bind address |
| `CORTEX_SERVER_PORT` | `8080` | Listen port; when unset, `PORT` is used if present |
| `CORTEX_SERVER_PUBLIC_URL` | `http://localhost:<port>` | Public base URL for email and invite links (required in production) |
| `CORTEX_SERVER_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown timeout |
| `CORTEX_SERVER_READ_TIMEOUT` | `15s` | HTTP read timeout |
| `CORTEX_SERVER_READ_HEADER_TIMEOUT` | `5s` | HTTP header read timeout |
| `CORTEX_SERVER_IDLE_TIMEOUT` | `60s` | HTTP idle timeout |
| `CORTEX_SERVER_DRAIN_DELAY` | `0s` | Time `/ready` reports 503 before streams are closed on shutdown |
| `CORTEX_SERVER_MAX_JSON_BODY` | preset | Max request body for JSON routes, in bytes |
| `CORTEX_SERVER_MAX_UPLOAD_BODY` | preset | Max body for snapshot and share uploads, in bytes |
| `CORTEX_SERVER_TRUST_PROXY_HEADERS` | `false` | Deprecated; use `CORTEX_PROXY` |
| `CORTEX_CORS_ALLOWED_ORIGINS` | `*` | Comma-separated browser origins |
| `CORTEX_CORS_ALLOW_CREDENTIALS` | `false` | Credentialed CORS; requires explicit origins |
| `CORTEX_METRICS_PATH` | `/metrics` | Prometheus path when `metrics` is enabled |

### Database

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_DATABASE_URL` | `postgres://cortex:cortex@localhost:5432/cortex_sync?sslmode=disable` | PostgreSQL connection string |
| `CORTEX_DATABASE_MAX_CONNS` | preset (5 / 10) | Pool max |
| `CORTEX_DATABASE_MIN_CONNS` | preset (0 / 1) | Pool min; cannot exceed max |
| `CORTEX_DATABASE_MAX_CONN_LIFETIME` | `1h` | Max connection lifetime |
| `CORTEX_DATABASE_MAX_CONN_IDLE_TIME` | preset (2m / 30m) | Idle connection timeout |
| `CORTEX_DATABASE_MIGRATE_ON_START` | preset (true / false) | Run migrations before serving |
| `CORTEX_DATABASE_MIGRATIONS_PATH` | | Optional golang-migrate source URL; empty uses the embedded migrations |

### Auth

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_AUTH_ACCESS_TOKEN_SECRET` | none usable | JWT signing secret (see [Boot checks](#boot-checks)) |
| `CORTEX_AUTH_ACCESS_TOKEN_EXPIRY` | `15m` | Access token lifetime |
| `CORTEX_AUTH_REFRESH_TOKEN_EXPIRY` | `2160h` | Refresh token lifetime (90 days) |
| `CORTEX_AUTH_ISSUER` | `cortex-sync` | JWT issuer, checked on validation |
| `CORTEX_AUTH_REGISTRATION_MODE` | `open` | `open`, `first-user` or `closed` |

### Rate limits and protection

`CORTEX_RATE_LIMIT_REQUESTS_PER_SECOND`, `CORTEX_RATE_LIMIT_BURST`,
`CORTEX_RATE_LIMIT_SENSITIVE_REQUESTS_PER_SECOND`, `CORTEX_RATE_LIMIT_SENSITIVE_BURST` and every
`CORTEX_PROTECTION_*` variable in the [Protection](#protection) table. All default to the preset.

### Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_STORAGE_BACKEND` | `local` | `local` (SeaweedFS/MinIO, creates the bucket), `r2` or `s3` |
| `CORTEX_STORAGE_ENDPOINT` | `localhost:9000` (local) | S3 API endpoint; a scheme sets SSL |
| `CORTEX_STORAGE_ACCESS_KEY` / `_SECRET_KEY` | `minioadmin` (local) | Credentials, required for `r2`/`s3` |
| `CORTEX_STORAGE_BUCKET` | `cortex-snapshots` | Snapshot bucket |
| `CORTEX_STORAGE_REGION` | `auto` (r2), `us-east-1` | Region |

### Sync

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_SYNC_MAX_FILE_SIZE` | preset | Max snapshot size in bytes (0 disables the check) |
| `CORTEX_SYNC_MAX_DELTAS_BEFORE_SNAPSHOT` | `10` | Deltas before `needs_snapshot` (0 disables) |
| `CORTEX_SYNC_MAX_DELTA_SIZE_RATIO` | `0.5` | Accumulated delta bytes / last snapshot size before `needs_snapshot` (0 disables) |
| `CORTEX_SYNC_MAX_SNAPSHOTS_PER_FILE` | `50` | Snapshot versions kept per file (0 disables pruning) |
| `CORTEX_SYNC_EVENT_RETENTION` | `720h` | Sync event retention; older cursors get `410 cursor_expired` |

### Collab

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_COLLAB_MAX_PEERS_PER_ROOM` | `10` | Max peers per collab room |
| `CORTEX_COLLAB_FLUSH_INTERVAL` | `2s` | How often buffered Yjs updates are flushed to the database |
| `CORTEX_COLLAB_MAX_BUF_BYTES` | `4194304` | Buffered bytes per room before a forced flush |

### Mail

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_MAIL_DRIVER` | `none` | `none`, `log`, `smtp` or `resend` |
| `CORTEX_MAIL_FROM` | `Cortex <no-reply@localhost>` | Sender address |
| `CORTEX_MAIL_RESEND_API_KEY` | | Resend API key |
| `CORTEX_MAIL_RESEND_URL` | `https://api.resend.com/emails` | Resend endpoint |
| `CORTEX_MAIL_HOST` / `_PORT` | / `587` | SMTP server |
| `CORTEX_MAIL_USERNAME` / `_PASSWORD` | | SMTP credentials |
| `CORTEX_MAIL_TLS_MODE` | `starttls` | `starttls`, `tls` (implicit, port 465) or `none` |
| `CORTEX_MAIL_TIMEOUT` | `15s` | SMTP and Resend request timeout |
| `CORTEX_FEATURE_EMAIL_VERIFICATION` | `auto` | `auto`, `true` or `false` |
| `CORTEX_FEATURE_PASSWORD_RESET` | `auto` | `auto`, `true` or `false` |

### Other

| Variable | Default | Description |
|----------|---------|-------------|
| `CORTEX_AVATAR_MAX_BYTES` | `5242880` | Max avatar upload |
| `CORTEX_AVATAR_SIZE` | `256` | Avatar output size in pixels (32-1024) |
| `CORTEX_JOBS_SWEEP_INTERVAL` | `1h` | Safety sweep of the blob janitor (it is also kicked after each commit that queues work); at least `1s` |
| `CORTEX_SUBSCRIPTION_ENABLED` | `false` | Stripe subscription enforcement |
| `CORTEX_SUBSCRIPTION_STRIPE_SECRET_KEY` / `_PRICE_ID` / `_WEBHOOK_SECRET` | | Required in production when subscriptions are enabled |
| `CORTEX_SUBSCRIPTION_CACHE_TTL` | `60s` | Positive entitlement cache lifetime |
| `CORTEX_SUBSCRIPTION_RENEWAL_GRACE` | `48h` | Grace after each billing period |
| `CORTEX_DEVOPS_DISCORD_WEBHOOK_URL` | | Optional Discord webhook for account, auth and billing notifications |
| `CORTEX_DEVOPS_DISCORD_USERNAME` | `Cortex DevOps` | Webhook display name |
| `CORTEX_DEVOPS_DISCORD_TIMEOUT` | `5s` | Webhook timeout |
| `CORTEX_USE_FAKE_REPOS` | `false` | In-memory repositories (development and tests only; rejected in production) |
