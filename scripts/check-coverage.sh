#!/usr/bin/env bash
# Fails when the core — internal/domain plus internal/app — falls below the bar docs/testing.md promises.
#
# Reads the profile `make test` writes and computes statement coverage for the core packages only; generated code,
# the Mongo adapter (covered by integration tests) and the wiring in cmd/api are deliberately left out of the number.
#
# With -coverpkg, every test binary reports every block, so the same block appears once per test package —
# blocks are keyed by position and a block counts as covered if any binary hit it.
#
#   ./scripts/check-coverage.sh [profile] [minimum-percent]
set -euo pipefail

PROFILE="${1:-cover.out}"
MIN="${2:-80}"

if [[ ! -f $PROFILE ]]; then
  echo "coverage profile $PROFILE not found — run make test first" >&2
  exit 1
fi

awk -v min="$MIN" '
  /^mode:/ { next }
  $1 ~ /\/internal\/(domain|app)\// && $1 !~ /\/apptest\// {
    stmts[$1] = $2
    if ($3 > 0) hit[$1] = 1
  }
  END {
    for (block in stmts) {
      total += stmts[block]
      if (block in hit) covered += stmts[block]
    }
    if (total == 0) { print "no core statements found in the profile" > "/dev/stderr"; exit 1 }
    pct = 100 * covered / total
    printf "core coverage (internal/domain + internal/app): %.1f%% — minimum %d%%\n", pct, min
    if (pct + 0.0001 < min) { exit 1 }
  }
' "$PROFILE"
