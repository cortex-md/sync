# Cortex Sync

This repository contains the Cortex sync server. Endpoint documentation lives in
[docs/api.md](docs/api.md), and production self-hosting guidance lives in
[docs/self-hosting.md](docs/self-hosting.md).

## How to self host the sync or contribute to the project

Cortex follow the idea of open source self hosting, so you can deploy the sync server yourself and configure it to work with your cortex instance, gaining total control over your data.

### Prerequisites

- You must have docker installed for a better experience, check the docs [here](https://docs.docker.com/engine/install/)
- If you are gonna contribute to the project, you must have go lang installed, check the doc [here](https://go.dev/doc/install)

### Installation

First, you need to clone this repository and navigate to the sync directory:

```bash
git clone https://github.com/cortex-md/sync.git
cd sync
```

Then, you can build the local development infra using Docker Compose:

```bash
docker compose up -d
```

This starts PostgreSQL, runs migrations, starts lightweight SeaweedFS S3-compatible storage, and starts
the sync server. The server is available at `http://localhost:8080`, and the local S3 endpoint is
available at `localhost:9000`.

You can check the sync server logs with:

```bash
docker logs cortex-sync
```

### Local development env

Create a private `.env.local` when you need optional development credentials such as Stripe or
Discord DevOps webhooks:

```bash
cp .env.local.example .env.local
```

`make run`, `make docker-up`, `make docker-down`, and `make docker-reset` load `.env.local` by
default. You can point Make at another file with `ENV_FILE`:

```bash
make ENV_FILE=.env.stripe docker-up
```

If you run Docker Compose directly, pass the file explicitly:

```bash
docker compose --env-file .env.local up -d
```

### Storage

The sync server has one storage backend choice.

```bash
CORTEX_STORAGE_BACKEND=local # local SeaweedFS (S3 API) for development
CORTEX_STORAGE_BACKEND=r2    # Cloudflare R2 for production
CORTEX_STORAGE_BACKEND=s3    # generic S3-compatible storage
```

For host development with the local S3 container, run Docker Compose and start the server normally:

```bash
docker compose up -d postgres s3
make run
```

If the desktop app wants to start or configure a local sync server, it only needs to pass
`CORTEX_STORAGE_BACKEND=local` plus endpoint overrides when needed. The desktop still talks to the
sync server through the HTTP URL, usually `http://localhost:8080`.

### Production self-hosting

For a small VPS, use the templates in `deploy/production`:

```bash
cd deploy/production
cp env.example .env
./scripts/deploy.sh v0.1.0
```

The production template runs Caddy for HTTPS in front of two sync replicas, PostgreSQL in a Docker
volume, and stores encrypted snapshots in Cloudflare R2. Read
[docs/self-hosting.md](docs/self-hosting.md) before deploying.

### Features

Every optional feature is a single environment variable. A disabled feature has its routes answer
`404 {"code":"feature_disabled"}` and its background jobs are never started. Clients read the
active set from `GET /meta/v1/capabilities`.

| Feature | Variable | Default | Effect when off |
| --- | --- | --- | --- |
| Tenancy | `CORTEX_TENANCY_MODE` | `single` | `multi` enables per-vault storage quotas and bills sync by the vault owner |
| Vault creation | `CORTEX_VAULT_CREATION` | `everyone` | `admins` lets only the instance admin (first user) create vaults |
| Email | `CORTEX_MAIL_DRIVER` | `none` | Invites return only the link; email changes apply immediately with the password |
| Email verification | `CORTEX_FEATURE_EMAIL_VERIFICATION` | `auto` | Verification routes are disabled; `auto` follows the mail driver |
| Password reset | `CORTEX_FEATURE_PASSWORD_RESET` | `auto` | Forgot/reset routes are disabled; `auto` follows the mail driver |
| Live collaboration | `CORTEX_FEATURE_COLLAB` | `true` | Collab, presence and ticket routes are not mounted |
| Avatars | `CORTEX_FEATURE_AVATARS` | `true` | Avatar routes are not mounted and `avatar_url` is empty |
| Guests | `CORTEX_FEATURE_GUESTS` | `true` | Guest invites and the `guest` role are disabled (groups and path access stay on) |
| Public note links | `CORTEX_FEATURE_PUBLIC_LINKS` | `true` | Share routes, the `/s/{id}` viewer and `/share/v1/{id}` are disabled |
| Subscriptions | `CORTEX_SUBSCRIPTION_ENABLED` | `false` | No billing checks |
| Metrics | `CORTEX_METRICS_ENABLED` | `true` | No `/metrics` route and no metrics middleware |
| Registration | `CORTEX_AUTH_REGISTRATION_MODE` | `open` | `first-user` or `closed` restrict sign-up |

Setting `CORTEX_FEATURE_EMAIL_VERIFICATION=true` or `CORTEX_FEATURE_PASSWORD_RESET=true` with
`CORTEX_MAIL_DRIVER=none` fails at startup instead of silently degrading.

### Email

Password reset emails link to a page that opens the Cortex app
(`cortex://reset-password?token=...&server=<CORTEX_SERVER_PUBLIC_URL>`): the new password is chosen
in the app because the server never sees it (see the key model in [docs/api.md](docs/api.md#key-model)).

Account emails (verification, email change, password reset) and vault invites are queued in
PostgreSQL and delivered by a background worker. By default `CORTEX_MAIL_DRIVER=none` disables
email entirely; `log` prints messages to the server log, which is handy in development. To send real
emails:

```bash
CORTEX_SERVER_PUBLIC_URL=https://sync.example.com
CORTEX_MAIL_DRIVER=smtp
CORTEX_MAIL_HOST=smtp.example.com
CORTEX_MAIL_PORT=587
CORTEX_MAIL_USERNAME=...
CORTEX_MAIL_PASSWORD=...
CORTEX_MAIL_FROM="Cortex <no-reply@example.com>"
```

### Running several instances

Any number of replicas can run behind a load balancer without sticky sessions. Events, presence,
collaboration relay and background jobs coordinate through PostgreSQL. Set
`CORTEX_SERVER_DRAIN_DELAY` (for example `5s`) so the load balancer sees `/ready` fail before
streams are closed during a rolling deploy.

To try it locally with two replicas and Mailpit:

```bash
CORTEX_MAIL_DRIVER=smtp CORTEX_MAIL_HOST=mailpit CORTEX_MAIL_PORT=1025 CORTEX_MAIL_TLS_MODE=none \
  docker compose --profile replicas up -d
```

The replicas listen on `:8080` and `:8081`; captured emails are at `http://localhost:8025`.
