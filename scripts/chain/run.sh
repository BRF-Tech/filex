#!/usr/bin/env bash
# The test chain's entry point: take the build host's lock, then run
# scripts/chain/run.mjs while holding it. The lock is the file CHAIN_LOCK
# names (`--print-lock` shows it; `none` runs without one), held with flock(1)
# for the whole run - the same lock any other build on the host takes, so a
# chain waits for them and they wait for it. What the chain runs and how to
# configure it: docs/CONTRIBUTING.md -> "The whole chain on one Linux host".
#
#   bash scripts/chain/run.sh --plan                    # the jobs, the budget, the expected time
#   bash scripts/chain/run.sh --env ~/chain.env         # the full profile
#   bash scripts/chain/run.sh --profile targeted ...    # see run.mjs --help
#
# CHAIN_LOCK_WAIT_MIN (the process environment only) bounds the wait for the
# lock: a run that has not got it within that many minutes exits 3 without
# starting. Unset, it waits for as long as it takes. The nightly run sets it,
# so a release chain that holds the lock past the night does not push the
# nightly run into the morning (scripts/chain/nightly.mjs).
set -u
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for a in "$@"; do
  case "$a" in
    --plan|--help|-h|--print-lock) exec node "$DIR/run.mjs" "$@" ;;
  esac
done
WAIT="${CHAIN_LOCK_WAIT_MIN:-}"
if [ -n "$WAIT" ]; then
  case "$WAIT" in
    *[!0-9]*) echo "CHAIN_LOCK_WAIT_MIN=$WAIT is not a whole number of minutes"; exit 2 ;;
  esac
fi
LOCK="$(node "$DIR/run.mjs" --print-lock "$@")" || exit 2
[ -n "$LOCK" ] || exit 2
if [ "$LOCK" = none ]; then
  CHAIN_LOCK_HELD=none exec node "$DIR/run.mjs" "$@"
fi
mkdir -p "$(dirname "$LOCK")" || exit 2
exec 9>>"$LOCK" || exit 2
if ! flock -n 9; then
  echo "$(date -u +%FT%TZ) waiting for the lock $LOCK (another chain or build holds it)"
  if [ -n "$WAIT" ]; then
    if ! flock -w "$((WAIT * 60))" 9; then
      echo "$(date -u +%FT%TZ) the lock $LOCK was not free within $WAIT minutes: not started"
      exit 3
    fi
  else
    flock 9 || exit 2
  fi
fi
CHAIN_LOCK_HELD="$LOCK" exec node "$DIR/run.mjs" "$@"
