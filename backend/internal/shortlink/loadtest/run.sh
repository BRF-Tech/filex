#!/usr/bin/env bash
# One load run of an app's public route (README.md beside this file): boots a
# filex binary on a fresh data directory, sets the link shortener example up
# under /l, runs H3 then H4 against it, stops it and removes the directory.
#
#   run.sh <filex binary> <shortlinks.wasm> <filex-app.json> <loadtest binary> <out dir>
#
# Linux only (the runner reads filex's CPU time from /proc). The machine
# should be otherwise idle: compare two builds by running this twice, one
# after the other, on the same machine.
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "usage: run.sh <filex binary> <shortlinks.wasm> <filex-app.json> <loadtest binary> <out dir>" >&2
  exit 2
fi
BIN=$1
WASM=$2
MANIFEST=$3
LT=$4
OUT=$5
PORT=${LOADTEST_PORT:-18080}
POOL=${FILEX_APP_ROUTE_POOL:-4}

mkdir -p "$OUT"
DATA=$(mktemp -d /tmp/filex-loadtest.XXXXXX)
case "$DATA" in
  /tmp/filex-loadtest.?*) ;;
  *) echo "unexpected data directory: $DATA" >&2; exit 1 ;;
esac

export FILEX_ADMIN_EMAIL=loadtest@example.com
export FILEX_ADMIN_PASSWORD=loadtest-not-a-secret

env FILEX_LISTEN="127.0.0.1:$PORT" \
  FILEX_DATA_DIR="$DATA/data" \
  FILEX_PUBLIC_URL="http://127.0.0.1:$PORT" \
  FILEX_DEFAULT_LOCALE=en \
  FILEX_SECRET_KEY=loadtest-key-not-a-secret \
  FILEX_UPDATE_CHECK=0 \
  FILEX_APP_ROUTE_POOL="$POOL" \
  "$BIN" serve >"$OUT/filex.log" 2>&1 &
PID=$!

stop() {
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  echo "removing $DATA"
  rm -rf -- "$DATA"
}
trap stop EXIT

for _ in $(seq 1 100); do
  if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null

{
  echo "binary: $BIN, sha256 $(sha256sum "$BIN" | cut -d' ' -f1)"
  echo "host: $(hostname), $(nproc) CPUs, load $(cut -d' ' -f1-3 /proc/loadavg), FILEX_APP_ROUTE_POOL=$POOL"
} | tee "$OUT/run.txt"

"$LT" setup -url "http://127.0.0.1:$PORT" -wasm "$WASM" -manifest "$MANIFEST" | tee -a "$OUT/run.txt"

# LOADTEST_H3_ARGS / LOADTEST_H4_ARGS: flags added to a scenario's run (a
# shorter -duration while trying the script out); the README's numbers are
# taken without them.
status=0
# shellcheck disable=SC2086
"$LT" run -url "http://127.0.0.1:$PORT" -scenario H3 -pid "$PID" -json "$OUT/h3.json" ${LOADTEST_H3_ARGS:-} | tee -a "$OUT/run.txt" || status=1
# shellcheck disable=SC2086
"$LT" run -url "http://127.0.0.1:$PORT" -scenario H4 -pid "$PID" -json "$OUT/h4.json" ${LOADTEST_H4_ARGS:-} | tee -a "$OUT/run.txt" || status=1
# H4 again with the door's pages replaced by the administrator's templates
# (rendered in the sandbox), from visitors of its own.
"$LT" edit -url "http://127.0.0.1:$PORT" | tee -a "$OUT/run.txt"
# shellcheck disable=SC2086
"$LT" run -url "http://127.0.0.1:$PORT" -scenario H4 -offset 16384 -label H4-edited -pid "$PID" -json "$OUT/h4-edited.json" ${LOADTEST_H4_ARGS:-} | tee -a "$OUT/run.txt" || status=1
exit "$status"
