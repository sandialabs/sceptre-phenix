#!/usr/bin/env bash
#
# CI only: starts `bin/phenix ui` in the background on 127.0.0.1 and waits
# until it answers API requests. The Frontend workflow's e2e and perf jobs
# use it with the binary from the shared build job.
#
# Usage: scripts/ci-start-phenix-ui.sh <bolt-store-path> <log-file>
#
# The port is E2E_PORT, default 3000 (CI runners have it free; locally,
# follow the port rule in AGENTS.md and start the server yourself).

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <bolt-store-path> <log-file>" >&2
  exit 2
fi

store="$1"
log="$2"
port="${E2E_PORT:-3000}"

cd "$(dirname "$0")/.."

./bin/phenix ui --store.endpoint "bolt://${store}" \
  --listen-endpoint "127.0.0.1:${port}" &> "$log" &

for _ in $(seq 30); do
  if curl -sf --max-time 2 "http://127.0.0.1:${port}/api/v1/experiments" > /dev/null; then
    exit 0
  fi
  sleep 1
done

echo "phenix ui did not answer on port ${port} within 30 s; last log lines:" >&2
tail -50 "$log" >&2
exit 1
