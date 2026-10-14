#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

version="${1:?usage: deploy.sh <version>}"
export CORTEX_SYNC_VERSION="$version"
domain=$(sed -n 's/^CORTEX_SYNC_DOMAIN=//p' .env)
replicas=$(sed -n 's/^CORTEX_SYNC_REPLICAS=//p' .env)

services="sync-1"
if [ "${replicas:-1}" = "2" ]; then
	export COMPOSE_PROFILES=replicas
	services="sync-1 sync-2"
fi

docker compose pull migrate $services
docker compose up -d postgres
docker compose run --rm migrate
for service in $services; do
	docker compose up -d --no-deps "$service"
	until [ "$(docker inspect -f '{{.State.Health.Status}}' "$(docker compose ps -q "$service")")" = healthy ]; do
		sleep 2
	done
done
docker compose up -d caddy
docker image prune -f >/dev/null

curl -fsS "https://$domain/ready" >/dev/null
echo "deployed $version"
