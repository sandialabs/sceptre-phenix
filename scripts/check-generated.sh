#!/usr/bin/env bash
# Runs `make generate` twice and fails on a missing or empty output, differing bytes,
# or a changed tracked file; only the CLI reference (make cli-docs) is tracked.

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

cli_paths=(docs/content/cli docs/mkdocs.yml)
cli_state() {
  git status --porcelain --untracked-files=all -- "${cli_paths[@]}"
  git diff HEAD --binary -- "${cli_paths[@]}" | sha256
  git ls-files -z --others --exclude-standard -- "${cli_paths[@]}" |
    xargs -0 -r cat -- | sha256
}

cli_state > "$tmp/cli-before"
for run in 1 2; do
  echo "==> Generating the CLI reference (run $run of 2)"
  make cli-docs
  cli_state > "$tmp/cli-after"
  if ! cmp -s "$tmp/cli-before" "$tmp/cli-after"; then
    echo "Error: the CLI reference does not match the phenix commands; regenerating changed:" >&2
    git status --porcelain --untracked-files=all -- "${cli_paths[@]}" >&2
    echo "Run 'make cli-docs' and commit the result." >&2
    exit 1
  fi
done

echo "Generated outputs are reproducible, and the CLI reference is current:"
sed 's/^/  /' "$tmp/run1.sha256"
