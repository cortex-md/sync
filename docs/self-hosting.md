# Self-hosting Cortex Sync

This guide covers the small-VPS deployment path: Docker Compose on the VPS, Caddy for HTTPS,
PostgreSQL on a Docker volume, and Cloudflare R2 for encrypted file snapshots.

## 1. Create R2 storage

Create one private R2 bucket for snapshots, then create an R2 API token with Object Read & Write
permission scoped to that bucket. Use the S3 API endpoint from Cloudflare:

```text
https://account-id.r2.cloudflarestorage.com
```

Use these sync settings:

```env
CORTEX_STORAGE_BACKEND=r2
CORTEX_STORAGE_ENDPOINT=https://account-id.r2.cloudflarestorage.com
CORTEX_STORAGE_REGION=auto
```

The server does not create R2 buckets in production. Create the bucket in Cloudflare first.

## 2. Prepare the VPS

Point a DNS record such as `sync.example.com` at the VPS. Then copy `deploy/production` to the VPS
or to your private infra repository.

```bash
cd deploy/production
cp env.example .env
./scripts/bootstrap-vps.sh
```

Edit `.env` and replace every placeholder. At minimum:

- `CORTEX_SYNC_DOMAIN`
- `POSTGRES_PASSWORD`
- `CORTEX_DATABASE_URL`, using the same Postgres password
- `CORTEX_AUTH_ACCESS_TOKEN_SECRET`, at least 32 random characters
- `CORTEX_AUTH_REGISTRATION_MODE`, usually `first-user` for private self-hosted servers
- `CORTEX_STORAGE_ENDPOINT`, `CORTEX_STORAGE_ACCESS_KEY`, `CORTEX_STORAGE_SECRET_KEY`, and
  `CORTEX_STORAGE_BUCKET`

Keep `.env` private. It is ignored by git.

Subscriptions are disabled by default for self-hosted servers:

```env
CORTEX_SUBSCRIPTION_ENABLED=false
```

Leave it disabled for normal self-hosting. When it is enabled, production startup requires Abacate
Pay API, product, webhook secret, and webhook HMAC values, and sync-mutating endpoints return `402`
until the authenticated user has an active entitlement.

Optional Discord DevOps notifications are disabled by default:

```env
CORTEX_DEVOPS_DISCORD_WEBHOOK_URL=
CORTEX_DEVOPS_DISCORD_USERNAME=Cortex DevOps
CORTEX_DEVOPS_DISCORD_TIMEOUT=5s
```

Set `CORTEX_DEVOPS_DISCORD_WEBHOOK_URL` to a Discord webhook URL to receive operational embeds for
account creation, suspicious device reuse signals, checkout creation, and subscription lifecycle
webhooks. These messages include user IDs, email addresses, display names, and billing metadata, so
use a private channel and rotate the webhook if it is exposed. Discord delivery failures are logged
as warnings and never block user-facing auth or billing flows.

## 3. Deploy

GitHub Actions publishes images to `ghcr.io/cortex-md/sync` from `main`, commit SHA tags, and
version tags. Deploy an explicit version tag from the VPS:

```bash
./scripts/deploy.sh v0.1.0
```

The deploy script pulls the image, runs migrations, starts Caddy/Postgres/sync, and waits for:

```text
https://$CORTEX_SYNC_DOMAIN/ready
```

Use `/health` for liveness and `/ready` for dependency readiness.

## 4. Configure Cortex Desktop

Open Settings > Self-hosted sync in Cortex and set the Sync URL to:

```text
https://sync.example.com
```

Then sign in or register against that server and link the vault.

## 5. Backups

Run a database backup before upgrades and on a schedule:

```bash
./scripts/backup-postgres.sh
```

Validate a backup with:

```bash
./scripts/restore-check.sh backups/cortex-sync-YYYYMMDDTHHMMSSZ.dump
```

Set `CORTEX_BACKUP_STORAGE_BUCKET` if you want `backup-postgres.sh` to upload database dumps to
the same storage provider credentials used by the sync server.

Snapshot blobs live in remote object storage. The database contains metadata, users, devices, vault
membership, sync events, and object keys, so database backups are required for disaster recovery.

## 6. Maintenance

- Rotate `CORTEX_AUTH_ACCESS_TOKEN_SECRET` only with a planned sign-out window; existing access
  tokens become invalid.
- Rotate R2 or S3 keys by adding a new key in the storage provider, updating `.env`, deploying,
  then revoking the old key.
- Keep `CORTEX_SERVER_TRUST_PROXY_HEADERS=true` only when Caddy or another trusted reverse proxy is
  the only public entrypoint.
- Keep `CORTEX_AUTH_REGISTRATION_MODE=first-user` for private servers after the first account is
  created, or switch it to `closed` when all expected users are invited.
- Keep `CORTEX_CORS_ALLOW_CREDENTIALS=false` unless you need cookie credentials. Bearer-token desktop
  clients do not require browser credential mode.
- Keep `CORTEX_METRICS_ENABLED=false` unless metrics are exposed only on a private network or behind
  your own authentication.
- Keep the Discord DevOps webhook in a private channel because messages include operational PII.
- Check `docker compose -f compose.yaml logs --tail=200 sync` after upgrades.
