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

The reference deployment is a Render web service (Docker runtime) with Neon PostgreSQL, Cloudflare
R2 for encrypted snapshots and Resend for email. [`render.yaml`](render.yaml) is the Render
Blueprint, [`.env.example`](.env.example) lists every value it needs, and
[docs/self-hosting.md](docs/self-hosting.md) walks through the setup step by step.

The same image runs anywhere Docker does (`ghcr.io/cortex-md/sync`). A single box on a private
network only needs `CORTEX_DEPLOYMENT=single` and a PostgreSQL database; several replicas behind a
load balancer use `CORTEX_DEPLOYMENT=cluster`. Both setups are covered in
[docs/self-hosting.md](docs/self-hosting.md), and every variable is documented in
[docs/configuration.md](docs/configuration.md).

### Configuration

A handful of high-level keys cover most deployments; each expands into a preset that any
fine-grained variable can override:

| Variable | Values | Default |
| --- | --- | --- |
| `CORTEX_DEPLOYMENT` | `single` (one process, in-memory bus, migrations on boot) or `cluster` (replicas through PostgreSQL) | `single` |
| `CORTEX_PROXY` | `none`, `render`, `cloudflare`, `hops:N` | `none` |
| `CORTEX_PROTECTION` | `off`, `standard`, `strict` | `standard` |
| `CORTEX_FEATURES` | `collab,avatars,guests,public_links,metrics` | `collab,avatars,guests,public_links` |

See [docs/configuration.md](docs/configuration.md) for the protection presets and the full list.

### Features

Optional features are listed in `CORTEX_FEATURES`; anything not listed is off. A disabled feature
has its routes answer `404 {"code":"feature_disabled"}` and its background jobs are never started.
Clients read the active set from `GET /meta/v1/capabilities`.

| Feature | Variable | Default | Effect when off |
| --- | --- | --- | --- |
| Live collaboration | `CORTEX_FEATURES` contains `collab` | on | Collab, presence and ticket routes are disabled |
| Avatars | `CORTEX_FEATURES` contains `avatars` | on | Avatar routes are disabled and `avatar_url` is empty |
| Guests | `CORTEX_FEATURES` contains `guests` | on | Guest invites and the `guest` role are disabled (groups and path access stay on) |
| Public note links | `CORTEX_FEATURES` contains `public_links` | on | Share routes, the `/s/{id}` viewer and `/share/v1/{id}` are disabled |
| Metrics | `CORTEX_FEATURES` contains `metrics` | off | No `/metrics` route and no metrics middleware |
| Tenancy | `CORTEX_TENANCY_MODE` | `single` | `multi` enables per-vault storage quotas, per-account caps and bills sync by the vault owner |
| Vault creation | `CORTEX_VAULT_CREATION` | `everyone` | `admins` lets only the instance admin (first user) create vaults |
| Email | `CORTEX_MAIL_DRIVER` | `none` | No verification, password reset or email-addressed invites; invite links still work |
| Email verification | `CORTEX_FEATURE_EMAIL_VERIFICATION` | `auto` | Verification routes are disabled; `auto` follows the mail driver |
| Password reset | `CORTEX_FEATURE_PASSWORD_RESET` | `auto` | Forgot/reset routes are disabled; `auto` follows the mail driver |
| Subscriptions | `CORTEX_SUBSCRIPTION_ENABLED` | `false` | No billing checks |
| Registration | `CORTEX_AUTH_REGISTRATION_MODE` | `open` | `first-user` or `closed` restrict sign-up |

`CORTEX_FEATURES=` (empty) turns every optional feature off. The older `CORTEX_FEATURE_COLLAB`,
`CORTEX_FEATURE_AVATARS`, `CORTEX_FEATURE_GUESTS`, `CORTEX_FEATURE_PUBLIC_LINKS` and
`CORTEX_METRICS_ENABLED` variables are still accepted and log a deprecation warning.

Setting `CORTEX_FEATURE_EMAIL_VERIFICATION=true` or `CORTEX_FEATURE_PASSWORD_RESET=true` with
`CORTEX_MAIL_DRIVER=none` fails at startup instead of silently degrading.

### Email

Password reset emails link to a page that opens the Cortex app
(`cortex://reset-password?token=...&server=<CORTEX_SERVER_PUBLIC_URL>`): the new password is chosen
in the app because the server never sees it (see the key model in [docs/api.md](docs/api.md#key-model)).

Account emails (verification, email change, password reset) and vault invites are written to the
`email_outbox` table in the same transaction as the action. After the commit a dispatcher claims
the row, sends it outside the transaction and records the result. Failed sends are retried after
1m, 5m, 30m, 2h and 6h; after 6 attempts (or a permanent rejection) the row is marked failed. A
sweep on boot and every hour picks up anything left behind, sent rows are deleted after 7 days and
failed rows after 30.

Drivers:

- `none` (default) disables email. Email verification and password reset are off, email changes
  apply immediately with the password, and invites only work through their link
  (`GET /vaults/v1/invites` returns an empty list).
- `log` prints messages to the server log, which is handy in development.
- `resend` sends through the [Resend](https://resend.com) API with an idempotency key per outbox
  row. Use it on hosts that block SMTP ports.
- `smtp` sends through any SMTP server.

```bash
CORTEX_SERVER_PUBLIC_URL=https://sync.example.com
CORTEX_MAIL_DRIVER=resend
CORTEX_MAIL_RESEND_API_KEY=re_...
CORTEX_MAIL_FROM="Cortex <no-reply@example.com>"

# or
CORTEX_MAIL_DRIVER=smtp
CORTEX_MAIL_HOST=smtp.example.com
CORTEX_MAIL_PORT=587
CORTEX_MAIL_USERNAME=...
CORTEX_MAIL_PASSWORD=...
CORTEX_MAIL_FROM="Cortex <no-reply@example.com>"
```

With mail enabled, email-addressed invites (listing and accepting by `invite_id`) and vaults with
`allowed_email_domains` require a verified email address (`403 email_not_verified`).

### Running several instances

Set `CORTEX_DEPLOYMENT=cluster` and any number of replicas can run behind a load balancer without
sticky sessions. Events, presence, collaboration relay, revocations and background jobs coordinate
through PostgreSQL. Run `cortex-sync migrate up` before rolling out a new version, and set
`CORTEX_SERVER_DRAIN_DELAY` (for example `5s`) so the load balancer sees `/ready` fail before
streams are closed during a rolling deploy.

To try it locally with two replicas and Mailpit:

```bash
CORTEX_MAIL_DRIVER=smtp CORTEX_MAIL_HOST=mailpit CORTEX_MAIL_PORT=1025 CORTEX_MAIL_TLS_MODE=none \
  docker compose --profile replicas up -d
```

The replicas listen on `:8080` and `:8081`; captured emails are at `http://localhost:8025`.
