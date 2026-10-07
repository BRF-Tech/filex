#!/bin/bash
# The migrations through the real CLI (the build's bin/filex) on each engine
# of MIG_ENGINES, every one on a database made empty for this job:
# up, then MIG_DOWN steps down, then up again. Green when every step exits 0,
# each down removes exactly one applied migration, and the second up brings
# back as many as the first. MIG_DSN_<engine> is the engine's DSN. Go image,
# unprivileged.
. /w/chain/job/common.sh
cd /w/run || exit 1
BIN=/w/src/bin/filex
DOWNS="${MIG_DOWN:-3}"
bad=0
report=""

applied() {
  "$BIN" migrate status > "$OUT/status-$1.out" 2>&1
  grep -E -- '-- [0-9]+_' "$OUT/status-$1.out" | grep -vc 'Pending'
}

step() {
  local engine="$1" op="$2" n="$3"
  "$BIN" migrate "$op" > "$OUT/$engine-$n-$op.out" 2>&1
  local rc=$?
  [ $rc -eq 0 ] || { echo "$engine: migrate $op (step $n) exited $rc"; tail -5 "$OUT/$engine-$n-$op.out"; bad=1; }
  return $rc
}

for engine in ${MIG_ENGINES:-sqlite postgres mysql}; do
  dsn_var="MIG_DSN_$engine"
  export FILEX_DB_DRIVER="$engine" FILEX_DB_DSN="${!dsn_var:-}" FILEX_DATA_DIR="/w/run/tmp/migrate-$engine"
  [ -n "$FILEX_DB_DSN" ] || { echo "$engine: no $dsn_var"; bad=1; continue; }
  mkdir -p "$FILEX_DATA_DIR"
  step "$engine" up 1 || continue
  first=$(applied "$engine-1")
  counts="$first"
  for i in $(seq 1 "$DOWNS"); do
    step "$engine" down "$((i + 1))" || break
    now=$(applied "$engine-$((i + 1))")
    counts="$counts>$now"
    [ "$now" -eq $((first - i)) ] || { echo "$engine: after down $i, $now applied (expected $((first - i)))"; bad=1; }
  done
  step "$engine" up "$((DOWNS + 2))"
  last=$(applied "$engine-last")
  counts="$counts>$last"
  [ "$last" -eq "$first" ] || { echo "$engine: up again left $last applied (first up: $first)"; bad=1; }
  echo "$engine: applied $counts"
  report="$report $engine:$counts"
done
summary "migrate=$bad$report"
exit $bad
