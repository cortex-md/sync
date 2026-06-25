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

This starts PostgreSQL, runs migrations, starts lightweight MinIO S3-compatible storage, and starts
the sync server. The server is available at `http://localhost:8080`, and the local S3 endpoint is
available at `localhost:9000`.

You can check the sync server logs with:

```bash
docker logs cortex-sync
```

### Local development env

Create a private `.env.local` when you need optional development credentials such as Abacate Pay or
Discord DevOps webhooks:

```bash
cp .env.local.example .env.local
```

`make run`, `make docker-up`, `make docker-down`, and `make docker-reset` load `.env.local` by
default. You can point Make at another file with `ENV_FILE`:

```bash
make ENV_FILE=.env.abacate docker-up
```

If you run Docker Compose directly, pass the file explicitly:

```bash
docker compose --env-file .env.local up -d
```

### Storage

The sync server has one storage backend choice.

```bash
CORTEX_STORAGE_BACKEND=local # local MinIO for development
CORTEX_STORAGE_BACKEND=r2    # Cloudflare R2 for production
CORTEX_STORAGE_BACKEND=s3    # generic S3-compatible storage
```

For host development with local MinIO, run Docker Compose and start the server normally:

```bash
docker compose up -d postgres minio
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
./scripts/bootstrap-vps.sh
./scripts/deploy.sh v0.1.0
```

The production template runs Caddy for HTTPS, PostgreSQL in a Docker volume, and stores encrypted
snapshots in Cloudflare R2. Read [docs/self-hosting.md](docs/self-hosting.md) before deploying.
