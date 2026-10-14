# Production deploy

One small Linux server runs the whole stack with Docker Compose:

| Service | What it does | Memory limit |
| --- | --- | --- |
| `caddy` | HTTPS on `CORTEX_SYNC_DOMAIN` with automatic certificates, reverse proxy and WebSockets | 64 MB |
| `postgres` | Postgres 16 tuned for a small server | 256 MB |
| `migrate` | Runs `cortex-sync migrate up` before every deploy, then exits | — |
| `sync-1` | The sync server | 256 MB |
| `sync-2` | Optional second replica for rolling deploys (`CORTEX_SYNC_REPLICAS=2`) | 256 MB |

Encrypted files live in Cloudflare R2, so the server only stores the database. A 1 GB server
(AWS Lightsail, a Hetzner VPS or an Oracle Cloud Always Free VM) is enough to start. Add 1 GB of
swap on servers with 1 GB of RAM or less.

## 1. Cloudflare R2

1. Create two buckets: `cortex-snapshots` (vault files) and `cortex-backups` (database dumps).
2. Create an R2 API token with **Object Read & Write** on both buckets. Note the access key, the
   secret key and the S3 endpoint `https://<account-id>.r2.cloudflarestorage.com`.
3. On `cortex-backups`, add a lifecycle rule that deletes objects after 30 days.

## 2. Server

1. Create an Ubuntu 24.04 server (ARM or x86; the image is published for both) and open ports
   22, 80 and 443.
2. Point `CORTEX_SYNC_DOMAIN` (for example `api.cortex-md.tech`) to the server's public IP with an
   `A` record. If the domain is on Cloudflare, keep the record **DNS only** (grey cloud) so Caddy
   can issue the certificate.
3. Install Docker and the compose plugin:

   ```sh
   curl -fsSL https://get.docker.com | sh
   sudo usermod -aG docker "$USER"
   ```

4. Add swap on small servers:

   ```sh
   sudo fallocate -l 1G /swapfile && sudo chmod 600 /swapfile
   sudo mkswap /swapfile && sudo swapon /swapfile
   echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
   ```

5. Copy this folder to the server as `~/cortex-sync` (the CI deploy job keeps it up to date
   afterwards):

   ```sh
   scp -r deploy/production user@server:~/cortex-sync
   ```

6. Create `~/cortex-sync/.env` from `env.example` and fill in every empty value. Generate secrets
   with `openssl rand -hex 32`.

The container image `ghcr.io/cortex-md/sync` must be public (GitHub → organization packages →
`sync` → Package settings → Change visibility). Otherwise run `docker login ghcr.io` on the server
with a token that has `read:packages`.

## 3. Mail with Gmail

1. Use a dedicated Google account and turn on 2-Step Verification.
2. Create an App Password at <https://myaccount.google.com/apppasswords>.
3. Set `CORTEX_MAIL_USERNAME` to the account address, `CORTEX_MAIL_PASSWORD` to the 16 characters
   without spaces and `CORTEX_MAIL_FROM` to `Cortex <the-account@gmail.com>`.

Gmail sends from the account address and allows about 500 messages a day. Password reset and
email verification turn on automatically once mail is configured.

## 4. Stripe

1. Create the product and its recurring price, then copy the price id to
   `CORTEX_SUBSCRIPTION_STRIPE_PRICE_ID` and the secret key to
   `CORTEX_SUBSCRIPTION_STRIPE_SECRET_KEY`.
2. Add a webhook endpoint at `https://<CORTEX_SYNC_DOMAIN>/webhooks/stripe` with the events
   `checkout.session.completed`, `customer.subscription.created`, `customer.subscription.updated`
   and `customer.subscription.deleted`. Copy its signing secret to
   `CORTEX_SUBSCRIPTION_STRIPE_WEBHOOK_SECRET`.

## 5. First deploy

```sh
cd ~/cortex-sync
sh scripts/deploy.sh 0.1.0
```

`deploy.sh` pulls the image, starts Postgres, runs the migrations, starts the sync containers one
at a time (waiting for `/ready`), starts Caddy and checks `https://<domain>/ready`. Versions are the
release tag without the `v`.

## 6. Automatic deploys

Pushing a `vX.Y.Z` tag runs CI, publishes `ghcr.io/cortex-md/sync:X.Y.Z` and, when the repository
variable `DEPLOY_HOST` is set, deploys it:

| GitHub setting | Value |
| --- | --- |
| Variable `DEPLOY_HOST` | Server IP or host name |
| Variable `DEPLOY_USER` | SSH user in the `docker` group |
| Variable `DEPLOY_DIR` | Folder with `.env` (default `cortex-sync`, relative to the user's home) |
| Secret `DEPLOY_SSH_KEY` | Private key of a deploy-only SSH key pair (add the public key to `~/.ssh/authorized_keys`) |
| Secret `DEPLOY_KNOWN_HOSTS` | Output of `ssh-keyscan <server>` |

The job runs in the `production` environment, so required reviewers can be added there to approve
each deploy. It uploads `compose.yaml`, `Caddyfile` and `scripts/`, never `.env`.

Pushes to `main` only publish the `main` and `sha-<commit>` images; they are not deployed.

## 7. Backups

```sh
crontab -e
0 3 * * * sh ~/cortex-sync/scripts/backup-postgres.sh >> ~/cortex-sync/backups/backup.log 2>&1
```

Every run writes a `pg_dump` to `backups/`, uploads it to `CORTEX_BACKUP_BUCKET` on R2 and keeps
the last `CORTEX_BACKUP_KEEP_LOCAL` dumps on the server. Check a dump with:

```sh
sh scripts/restore-check.sh backups/<file>.dump
```

To restore on a new server, run the first deploy, stop `sync-1`, then:

```sh
docker compose exec -T postgres pg_restore -U cortex -d cortex_sync --clean --if-exists < backups/<file>.dump
docker compose up -d sync-1
```

## Moving to another server

Files are on R2 and the database is in the latest dump, so moving is: prepare the new server
(steps 2 and 5), restore the dump, then point the DNS record at the new IP.
