#!/usr/bin/env bash
#
# Renders and lints the phenix OpenAPI spec with one pinned Redocly CLI.
#
# Every place that builds the API reference (the Makefiles, the Docker,
# Podman and JIT images, and the docs workflow) calls this script, so they
# all use the same Redocly version and produce the same HTML. Change the
# version here, and only here.
#
# Usage:
#   scripts/api-docs.sh build <openapi.yml> <out.html>
#   scripts/api-docs.sh lint  <openapi.yml>
#
# Needs Node.js (npx) and network access the first time a version is used.

set -euo pipefail

REDOCLY_CLI_VERSION="${REDOCLY_CLI_VERSION:-2.53.2}"

redocly() {
  npx --yes "@redocly/cli@${REDOCLY_CLI_VERSION}" "$@"
}

usage() {
  echo "usage: $0 build <openapi.yml> <out.html>" >&2
  echo "       $0 lint <openapi.yml>" >&2
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
  *)
    usage
    ;;
esac
