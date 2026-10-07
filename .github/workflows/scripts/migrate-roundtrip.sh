#!/usr/bin/env bash
# The migrations through the real CLI, for ci.yml's Databases job:
#
#   MIG_DSN_sqlite=… MIG_DSN_postgres=… MIG_DSN_mysql=… \
#     bash .github/workflows/scripts/migrate-roundtrip.sh <filex binary>
#
# On each engine of MIG_ENGINES (default sqlite postgres mysql), on a database
# made empty for it: every migration up, MIG_DOWN steps down (default 3), up
# again. Green when every step exits 0, each down removes exactly one applied
# migration, and the second up brings back as many as the first. The build
# host's chain runs the same round trip (scripts/chain/job/migrate.sh); a
# down migration that does not undo its up is found here, not by somebody
# rolling a server back.
set -uo pipefail
BIN="${1:?usage: migrate-roundtrip.sh <filex binary>}"
DOWNS="${MIG_DOWN:-3}"
WORK="${RUNNER_TEMP:-/tmp}/migrate-roundtrip"
mkdir -p "$WORK"
bad=0
report=""

applied() {
  "$BIN" migrate status > "$WORK/status-$1.out" 2>&1
  grep -E -- '-- [0-9]+_' "$WORK/status-$1.out" | grep -vc 'Pending'
}

step() {
  local engine="$1" op="$2" n="$3"
  "$BIN" migrate "$op" > "$WORK/$engine-$n-$op.out" 2>&1
  local rc=$?
  if [ $rc -ne 0 ]; then
    echo "::error::$engine: migrate $op (step $n) exited $rc"
    tail -5 "$WORK/$engine-$n-$op.out"
    bad=1
  fi
  return $rc
}

for engine in ${MIG_ENGINES:-sqlite postgres mysql}; do
  dsn_var="MIG_DSN_$engine"
  export FILEX_DB_DRIVER="$engine" FILEX_DB_DSN="${!dsn_var:-}" FILEX_DATA_DIR="$WORK/data-$engine"
  if [ -z "$FILEX_DB_DSN" ]; then
    echo "::error::$engine: no $dsn_var"
    bad=1
    continue
  fi
  mkdir -p "$FILEX_DATA_DIR"
  step "$engine" up 1 || continue
  first=$(applied "$engine-1")
  counts="$first"
  for i in $(seq 1 "$DOWNS"); do
    step "$engine" down "$((i + 1))" || break
    now=$(applied "$engine-$((i + 1))")
    counts="$counts>$now"
    if [ "$now" -ne $((first - i)) ]; then
      echo "::error::$engine: after down $i, $now applied (expected $((first - i)))"
      bad=1
    fi
  done
  step "$engine" up "$((DOWNS + 2))"
  last=$(applied "$engine-last")
  counts="$counts>$last"
  if [ "$last" -ne "$first" ]; then
    echo "::error::$engine: up again left $last applied (the first up: $first)"
    bad=1
  fi
  echo "$engine: applied $counts"
  report="$report $engine:$counts"
done
echo "### Migrations up, down $DOWNS, up:$report" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
exit $bad
