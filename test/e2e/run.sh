#!/bin/sh
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
cleanup() {
	status=$?
	for pid in "$work"/*.pid; do
		[ -f "$pid" ] && kill "$(cat "$pid")" 2>/dev/null || true
	done
	if [ "$status" -ne 0 ]; then
		for log in "$work"/*.log; do
			[ -f "$log" ] || continue
			echo "== $log"
			tail -n 50 "$log"
		done
	fi
	rm -rf "$work"
}
trap cleanup EXIT

export CORTEX_SERVER_ENV=production
export CORTEX_DEPLOYMENT=cluster
export CORTEX_PROTECTION=off
export CORTEX_DATABASE_URL="${CORTEX_E2E_DATABASE_URL:-postgres://cortex:cortex@127.0.0.1:5432/cortex_sync?sslmode=disable}"
export CORTEX_STORAGE_BACKEND=local
export CORTEX_STORAGE_ENDPOINT="${CORTEX_E2E_S3_ENDPOINT:-127.0.0.1:9000}"
export CORTEX_STORAGE_ACCESS_KEY=minioadmin
export CORTEX_STORAGE_SECRET_KEY=minioadmin
export CORTEX_STORAGE_BUCKET=cortex-snapshots
export CORTEX_AUTH_ACCESS_TOKEN_SECRET=e2e-secret-that-is-at-least-32-characters
export CORTEX_RATE_LIMIT_REQUESTS_PER_SECOND=1000
export CORTEX_RATE_LIMIT_BURST=1000
export CORTEX_RATE_LIMIT_SENSITIVE_REQUESTS_PER_SECOND=100
export CORTEX_RATE_LIMIT_SENSITIVE_BURST=100
export CORTEX_SERVER_DRAIN_DELAY=2s
export CORTEX_COLLAB_FLUSH_INTERVAL=1s
export CORTEX_JOBS_SWEEP_INTERVAL=2s

port_a=${CORTEX_E2E_PORT:-8080}
port_b=$((port_a + 1))
port_minimal=$((port_a + 2))
port_single=$((port_a + 3))

for port in "$port_a" "$port_b" "$port_minimal" "$port_single"; do
	if curl -sf "http://127.0.0.1:$port/health" >/dev/null; then
		echo "port $port is already serving; stop the running sync server first"
		exit 1
	fi
done

go build -o "$work/cortex-sync" "$root/cmd/server"
CORTEX_SERVER_PUBLIC_URL=http://127.0.0.1 "$work/cortex-sync" migrate up

start() {
	name=$1
	port=$2
	shift 2
	env CORTEX_SERVER_PORT="$port" CORTEX_SERVER_PUBLIC_URL="http://127.0.0.1:$port" "$@" \
		"$work/cortex-sync" >"$work/$name.log" 2>&1 &
	echo $! >"$work/$name.pid"
}

smtp="CORTEX_MAIL_DRIVER=smtp CORTEX_MAIL_HOST=127.0.0.1 CORTEX_MAIL_PORT=1025 CORTEX_MAIL_TLS_MODE=none"
start a "$port_a" $smtp
start b "$port_b" $smtp
start minimal "$port_minimal" CORTEX_MAIL_DRIVER=none CORTEX_TENANCY_MODE=multi CORTEX_FEATURES=
start single "$port_single" CORTEX_DEPLOYMENT=single CORTEX_DATABASE_MIGRATE_ON_START=true $smtp

for port in "$port_a" "$port_b" "$port_minimal" "$port_single"; do
	tries=0
	until curl -sf "http://127.0.0.1:$port/ready" >/dev/null; do
		tries=$((tries + 1))
		[ "$tries" -lt 60 ] || { echo "instance on :$port not ready"; exit 1; }
		sleep 0.5
	done
done

cd "$root"
CORTEX_E2E_URL_A="http://127.0.0.1:$port_a" \
	CORTEX_E2E_URL_B="http://127.0.0.1:$port_b" \
	CORTEX_E2E_URL_MINIMAL="http://127.0.0.1:$port_minimal" \
	CORTEX_E2E_DRAIN_B="kill $(cat "$work/b.pid")" \
	go test -tags e2e ./test/e2e/... -count=1 -race -v "$@"

single="http://127.0.0.1:$port_single"
CORTEX_E2E_URL_A="$single" \
	CORTEX_E2E_URL_B="$single" \
	CORTEX_E2E_URL_MINIMAL="http://127.0.0.1:$port_minimal" \
	go test -tags e2e ./test/e2e/... -count=1 -race -v -run 'TestSyncContracts|TestGuestScope|TestGroupsAndPathAccess|TestKeysPreloginAndReset|TestNoteShares' "$@"
