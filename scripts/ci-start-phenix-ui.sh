#!/usr/bin/env bash
# CI only: starts `bin/phenix ui` on 127.0.0.1:${E2E_PORT:-3197} in the background and waits until it is alive.
# Usage: scripts/ci-start-phenix-ui.sh <bolt-store-path> <log-file> [phenix ui flags...]

set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <bolt-store-path> <log-file> [phenix ui flags...]" >&2
  exit 2
fi

store="$1"
log="$2"
shift 2
port="${E2E_PORT:-3197}"

cd "$(dirname "$0")/.."

./bin/phenix ui --store.endpoint "bolt://${store}" \
  --listen-endpoint "127.0.0.1:${port}" "$@" &> "$log" &

# /healthz answers without credentials in every auth mode
for _ in $(seq 30); do
  if curl -sf --max-time 2 "http://127.0.0.1:${port}/healthz" > /dev/null; then
    exit 0
  fi
  sleep 1
done

echo "phenix ui did not answer on port ${port} within 30 s; last log lines:" >&2
tail -50 "$log" >&2
exit 1
