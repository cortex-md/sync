#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

env_value() {
	sed -n "s/^$1=//p" .env | tail -n 1
}

mkdir -p backups
file="backups/cortex-sync-$(date -u +%Y%m%dT%H%M%SZ).dump"
docker compose exec -T postgres pg_dump -U cortex -d cortex_sync -Fc > "$file"

bucket=$(env_value CORTEX_BACKUP_BUCKET)
if [ -n "$bucket" ]; then
	endpoint=$(env_value CORTEX_BACKUP_ENDPOINT)
	access_key=$(env_value CORTEX_BACKUP_ACCESS_KEY)
	secret_key=$(env_value CORTEX_BACKUP_SECRET_KEY)
	docker run --rm \
		-v "$PWD/backups:/backups:ro" \
		-e RCLONE_CONFIG_R2_TYPE=s3 \
		-e RCLONE_CONFIG_R2_PROVIDER=Cloudflare \
		-e RCLONE_CONFIG_R2_ENDPOINT="${endpoint:-$(env_value CORTEX_STORAGE_ENDPOINT)}" \
		-e RCLONE_CONFIG_R2_ACCESS_KEY_ID="${access_key:-$(env_value CORTEX_STORAGE_ACCESS_KEY)}" \
		-e RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="${secret_key:-$(env_value CORTEX_STORAGE_SECRET_KEY)}" \
		-e RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true \
		rclone/rclone:1.68.2 copyto "/$file" "r2:$bucket/postgres/$(basename "$file")"
fi

keep=$(env_value CORTEX_BACKUP_KEEP_LOCAL)
ls -1t backups/*.dump | tail -n +"$((${keep:-3} + 1))" | xargs -r rm -f
echo "$file"
