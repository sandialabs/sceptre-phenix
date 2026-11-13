#!/usr/bin/env bash
# `make fuzz`: runs every Go fuzz target in src/go for FUZZTIME (default 30s) each, after `make generate`,
# and fails once all have run if any failed; a failing input is saved in its package's testdata/fuzz/.

set -euo pipefail

FUZZTIME="${FUZZTIME:-30s}"

cd "$(dirname "$0")/../src/go"

failed=()

while IFS= read -r file; do
  pkg="./$(dirname "$file")"

  while IFS= read -r target; do
    echo "::group::${target} (${pkg}, ${FUZZTIME})"

    if ! go test -run '^$' -fuzz "^${target}\$" -fuzztime "$FUZZTIME" "$pkg"; then
      failed+=("${target} (${pkg})")
    fi

    echo "::endgroup::"
  done < <(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' "$file")
done < <(git ls-files --cached --others --exclude-standard -- '*_test.go' | xargs grep -l '^func Fuzz' | sort)

if [ "${#failed[@]}" -gt 0 ]; then
  printf 'Fuzz targets that failed:\n' >&2
  printf '  %s\n' "${failed[@]}" >&2
  exit 1
fi
