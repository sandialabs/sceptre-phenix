#!/usr/bin/env bash
# Fails on src/go functions unreachable from main or a test, except those listed with a reason
# in src/go/deadcode-allow.txt as "<package path> <function>". Needs `make generate` output.
# Runs as the go-deadcode prek hook (Lint workflow) and as `make deadcode` in src/go.

set -euo pipefail

# deadcode is pinned here only
DEADCODE_VERSION=v0.38.0

cd "$(dirname "$0")/../src/go"

allowed="$(sed -e 's/#.*//' -e 's/[[:space:]]*$//' -e '/^$/d' deadcode-allow.txt)"

found="$(GOFLAGS=-mod=mod go run "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}" -test \
  -f '{{range .Funcs}}{{$.Path}} {{.Name}}{{"\n"}}{{end}}' ./...)"

dead="$(grep -vxF -f <(printf '%s\n' "$allowed") <<< "$found" || true)"
stale="$(grep -vxF -f <(printf '%s\n' "$found") <<< "$allowed" || true)"

status=0

if [ -n "$dead" ]; then
  echo "Unreachable Go functions; remove them, or list them with the reason in src/go/deadcode-allow.txt:" >&2
  echo "  ${dead//$'\n'/$'\n'  }" >&2
  status=1
fi

if [ -n "$stale" ]; then
  echo "src/go/deadcode-allow.txt lists functions that are now reachable or gone; remove them:" >&2
  echo "  ${stale//$'\n'/$'\n'  }" >&2
  status=1
fi

exit "$status"
