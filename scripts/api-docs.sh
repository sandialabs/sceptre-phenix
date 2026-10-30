#!/usr/bin/env bash
# Renders and lints the OpenAPI spec and config schemas with one pinned Redocly CLI.
# Every API reference build calls this script, so change the version only here.

set -euo pipefail

REDOCLY_CLI_VERSION="${REDOCLY_CLI_VERSION:-2.53.2}"

redocly() {
  npx --yes "@redocly/cli@${REDOCLY_CLI_VERSION}" "$@"
}

usage() {
  echo "usage: $0 build <openapi.yml> <out.html>" >&2
  echo "       $0 lint <openapi.yml>" >&2
  echo "       $0 lint-schemas [<schema.yaml>...]  (default: every embedded config schema)" >&2
  exit 2
}

if ! command -v npx > /dev/null; then
  echo "Error: 'npx' not found. Install Node.js 24+ to build the API reference." >&2
  exit 1
fi

cmd="${1:-}"

case "$cmd" in
  build)
    [ "$#" -eq 3 ] || usage
    redocly build-docs "$2" --output="$3" --title='phenix API'
    ;;
  lint)
    [ "$#" -eq 2 ] || usage
    # The `spec` ruleset checks that the document is valid OpenAPI; the
    # stricter default ruleset flags style issues the spec has not adopted.
    redocly lint --extends=spec "$2"
    ;;
  lint-schemas)
    shift
    types="$(cd "$(dirname "$0")/../src/go/types" && pwd)"
    if [ "$#" -eq 0 ]; then
      set -- "$types"/schemas/*.yaml "$types"/version/schemas/*.yaml
    fi
    redocly lint --config="$types/redocly.yaml" "$@"
    ;;
  *)
    usage
    ;;
esac
