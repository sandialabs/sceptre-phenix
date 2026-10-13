#!/usr/bin/env bash
#
# Checks that `make generate` is reproducible: it regenerates every derived
# source (protobuf bindings, store mocks, RBAC policy, API reference) twice
# from scratch and fails if
#
#   - an output is missing or empty,
#   - the two runs produce different bytes (a non-deterministic generator), or
#   - generating changed a tracked file or left a file git does not ignore.
#
# All generated outputs are gitignored build products, so a generator that
# writes anywhere else is a bug. Runs in CI (ci.yml) and locally with
# `make check-generated`.

set -euo pipefail

cd "$(dirname "$0")/.."

# Keep this list in sync with the generators behind `make generate`.
outputs() {
  printf '%s\n' \
    src/go/store/mock.go \
    src/go/web/rbac/known_policy.go \
    src/go/web/public/docs/index.html
  # one binding per .proto file
  for proto in src/go/web/proto/*.proto; do
    printf '%s\n' "${proto%.proto}.pb.go"
  done
}

if command -v sha256sum > /dev/null; then
  sha256() { sha256sum "$@"; }
else
  sha256() { shasum -a 256 "$@"; }
fi

# A snapshot of the working tree as git sees it: changed and untracked paths,
# plus the content of every change to a tracked file.
tree_state() {
  git status --porcelain --untracked-files=all
  git diff HEAD --binary | sha256
}

regenerate() {
  local f
  while IFS= read -r f; do
    rm -f "$f"
  done < <(outputs)
  make generate
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

tree_state > "$tmp/before"
if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
  echo "Note: the working tree has local changes; checking only that generating does not add to them." >&2
fi

echo "==> Generating (run 1 of 2)"
regenerate

failed=0
while IFS= read -r f; do
  if [ ! -s "$f" ]; then
    echo "Error: '$f' is missing or empty after 'make generate'." >&2
    failed=1
  fi
done < <(outputs)
if [ "$failed" -ne 0 ]; then
  echo "Fix the generator, or update the output list in $0 if it moved." >&2
  exit 1
fi

files=()
while IFS= read -r f; do
  files+=("$f")
done < <(outputs)
sha256 "${files[@]}" > "$tmp/run1.sha256"

echo "==> Generating (run 2 of 2)"
regenerate

if ! sha256 -c --quiet "$tmp/run1.sha256" > "$tmp/check.log" 2>&1; then
  echo "Error: 'make generate' is not deterministic. These outputs differ between two runs:" >&2
  sed -n 's/^\(.*\): FAILED.*$/  non-deterministic generator: \1/p' "$tmp/check.log" >&2
  echo "Make the generator's output stable (sort maps, drop timestamps, pin tool versions)." >&2
  exit 1
fi

tree_state > "$tmp/after"
if ! cmp -s "$tmp/before" "$tmp/after"; then
  echo "Error: 'make generate' changed tracked files or left files git does not ignore:" >&2
  git status --porcelain --untracked-files=all >&2
  git diff --stat >&2
  echo "Generators must write only gitignored outputs. Ignore new outputs in .gitignore," >&2
  echo "or stop the generator from rewriting tracked files." >&2
  exit 1
fi

echo "Generated outputs are reproducible:"
sed 's/^/  /' "$tmp/run1.sha256"
