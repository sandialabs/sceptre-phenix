#!/usr/bin/env bash
# Rejects manual edits to CHANGELOG.md (which Releasaurus manages).
# Allows Releasaurus release branches (releasaurus-release-*).
set -euo pipefail

branch="${GITHUB_HEAD_REF:-$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)}"
case "$branch" in
  releasaurus-release-*)
    exit 0
    ;;
esac

fail() {
  echo "error: CHANGELOG.md is managed by Releasaurus; do not edit it by hand." >&2
  echo "       Write Conventional Commit subjects instead (override locally with PREK_SKIP=no-changelog-edit)." >&2
  exit 1
}

# In CI pull_request runs (actions/checkout fetches refs/pull/<n>/merge with
# fetch-depth: 2), compare the PR merge commit against its base parent (HEAD^1).
if [[ "${GITHUB_EVENT_NAME:-}" == "pull_request" ]] && git rev-parse --verify -q HEAD^2 >/dev/null 2>&1; then
  # Allow the bootstrap PR that introduces releasaurus.toml before it lands on base.
  if ! git cat-file -e "HEAD^1:releasaurus.toml" 2>/dev/null; then
    exit 0
  fi
  if [[ -n "$(git diff --name-only HEAD^1 HEAD -- CHANGELOG.md)" ]]; then
    fail
  fi
  exit 0
fi

# Locally (pre-commit staged changes or `prek run --all-files` / `make check`):
# fail only if CHANGELOG.md is modified, added, or deleted in the index or working tree.
if [[ -n "$(git status --porcelain -- CHANGELOG.md)" ]]; then
  fail
fi
