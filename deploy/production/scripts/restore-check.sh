#!/bin/sh
set -eu

dump="${1:?usage: restore-check.sh <file.dump>}"
name="cortex-restore-check-$$"

docker run -d --rm --name "$name" -e POSTGRES_PASSWORD=check postgres:16-alpine >/dev/null
trap 'docker stop "$name" >/dev/null' EXIT
until docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done

docker exec -i "$name" pg_restore -U postgres -d postgres --create --exit-on-error < "$dump"
docker exec "$name" psql -U postgres -d cortex_sync -Atc "select count(*) || ' users, ' || (select count(*) from vaults) || ' vaults' from users"
