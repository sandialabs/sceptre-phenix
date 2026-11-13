#!/usr/bin/env bash
# Reports govulncheck (go [module dir...]), npm audit (npm [project dir...]) or grype (grype <report.json> <title>)
# findings as Markdown, to $GITHUB_STEP_SUMMARY when set; exits 0 for none found, 1 for some, 2 when a tool fails.

set -euo pipefail

cd "$(dirname "$0")/.."

summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"
status=0

# found records a result: 1 for findings, 2 for a tool failure, which wins.
found() {
  if [ "$1" -gt "$status" ]; then
    status="$1"
  fi
}

# fenced prints its input inside a collapsed details block titled $1.
fenced() {
  printf '<details><summary>%s</summary>\n\n```text\n' "$1"
  cat
  printf '```\n\n</details>\n\n'
}

go_report() {
  local dirs=("$@") dir rc report

  if [ "${#dirs[@]}" -eq 0 ]; then
    dirs=(src/go examples/go)
  fi

  for dir in "${dirs[@]}"; do
    rc=0
    report="$(cd "$dir" && govulncheck ./... 2>&1)" || rc=$?
    printf '%s\n' "$report" >&2

    {
      printf "### govulncheck: \`%s\`\n\n" "$dir"

      case "$rc" in
        0) printf "No known vulnerability reaches this module's code.\n\n" ;;
        3) printf "**Known vulnerabilities reach this module's code.**\n\n"; found 1 ;;
        *) printf '**govulncheck failed (exit %s).**\n\n' "$rc"; found 2 ;;
      esac

      printf '%s\n' "$report" | fenced "govulncheck output"
    } >> "$summary"
  done
}

npm_report() {
  local dirs=("$@") dir report

  if [ "${#dirs[@]}" -eq 0 ]; then
    dirs=(src/js src/js/e2e src/js/perf)
  fi

  for dir in "${dirs[@]}"; do
    report="$(cd "$dir" && npm audit --json 2> /dev/null)" || true

    {
      printf "### npm audit: \`%s\`\n\n" "$dir"

      if ! jq -e '.metadata.vulnerabilities' <<< "$report" > /dev/null 2>&1; then
        printf '**npm audit failed.**\n\n'
        printf '%s\n' "$report" | fenced "npm audit output"
        found 2

        continue
      fi

      jq -r '.metadata.vulnerabilities |
        "\(.critical) critical, \(.high) high, \(.moderate) moderate, \(.low) low and \(.info) info.\n"' <<< "$report"

      if [ "$(jq '.metadata.vulnerabilities.total' <<< "$report")" -gt 0 ]; then
        found 1
        printf '| Package | Severity | Vulnerable versions | Via | Direct | Fix |\n| --- | --- | --- | --- | --- | --- |\n'
        jq -r '.vulnerabilities[] | [
            .name, .severity, .range, (.via | map(if type == "object" then .url else . end) | unique | join(" ")),
            (if .isDirect then "yes" else "no" end),
            (.fixAvailable | if type == "object" then "\(.name)@\(.version)" elif . then "yes" else "no" end)
          ] | map(tostring | gsub("\\|"; "\\|")) | "| " + join(" | ") + " |"' <<< "$report"
        printf '\n'
      fi
    } >> "$summary"
  done
}

# grype_report summarizes at most 200 matches, worst first, to stay inside the
# job summary's size limit.
grype_report() {
  local report="$1" title="$2" total

  if ! total="$(jq -e '(.matches // []) | length' "$report" 2> /dev/null)"; then
    printf "### %s\n\n**No grype report at \`%s\`.**\n\n" "$title" "$report" >> "$summary"
    found 2

    return
  fi

  {
    printf '### %s\n\n' "$title"
    jq -r '[(.matches // [])[].vulnerability.severity] | group_by(.) | map("\(length) \(.[0])") |
      if length == 0 then "No known vulnerabilities." else join(", ") + "." end' "$report"
    printf '\n'

    if [ "$total" -gt 0 ]; then
      found 1
      printf '| Vulnerability | Severity | Package | Version | Type | Fixed in |\n| --- | --- | --- | --- | --- | --- |\n'
      jq -r '
        {"Critical": 0, "High": 1, "Medium": 2, "Low": 3, "Negligible": 4} as $rank |
        (.matches // []) | sort_by($rank[.vulnerability.severity] // 5, .artifact.name) | .[:200][] | [
          .vulnerability.id, .vulnerability.severity, .artifact.name, .artifact.version, .artifact.type,
          (.vulnerability.fix.versions // [] | join(", "))
        ] | map(tostring | gsub("\\|"; "\\|")) | "| " + join(" | ") + " |"' "$report"
      printf '\n'

      if [ "$total" -gt 200 ]; then
        printf 'Showing the 200 worst of %s matches.\n\n' "$total"
      fi
    fi
  } >> "$summary"
}

case "${1:-}" in
  go) shift; go_report "$@" ;;
  npm) shift; npm_report "$@" ;;
  grype) [ "$#" -eq 3 ] || { echo "usage: $0 grype <report.json> <title>" >&2; exit 2; }; grype_report "$2" "$3" ;;
  *) echo "usage: $0 go|npm|grype ..." >&2; exit 2 ;;
esac

exit "$status"
