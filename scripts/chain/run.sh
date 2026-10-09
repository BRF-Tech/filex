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
#
# The hooks (task #194). Once the lock is held, and before run.mjs measures
# the host it has, run.sh runs CHAIN_PAUSE_CMD - a command that frees the host
# for the run, such as one that shuts a virtual machine down. When that
# exited 0, it runs CHAIN_RESUME_CMD once when the run ends, however it ends:
# green, red, a setup error, or stopped by a signal: TERM, INT and HUP reach
# run.mjs as TERM (it runs in the background of this shell, which starts it
# with INT ignored), it stops the run, and the resume runs after it.
# Both come from the settings file (`--print-hooks` shows them) and run with
# `bash -c`, still under the lock, without the lock's file descriptor (a
# daemon a hook starts cannot keep the lock), in a session of their own
# (setsid: no terminal, and a signal to the run's process group - a stop of
# the nightly run - does not cut a hook short; a systemd stop signals every
# process of the unit, so a hook that must finish ignores TERM itself), at
# most CHAIN_HOOK_TIMEOUT_S seconds each (then TERM to its process group, and
# KILL 30 s later). The resume hook gets the run's exit code in CHAIN_EXIT. A
# pause that fails or times out is logged, the run goes on without it and
# nothing is resumed; run.mjs writes what the pause did to chain.log and
# result.json (`host.pause`).
set -u
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for a in "$@"; do
  case "$a" in
    --plan|--help|-h|--print-lock|--print-hooks) exec node "$DIR/run.mjs" "$@" ;;
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
mapfile -t HOOKS < <(node "$DIR/run.mjs" --print-hooks "$@")
[ "${#HOOKS[@]}" -eq 3 ] || exit 2
HOOK_TIMEOUT="${HOOKS[0]}"
PAUSE_CMD="${HOOKS[1]}"
RESUME_CMD="${HOOKS[2]}"
case "$HOOK_TIMEOUT" in
  ''|*[!0-9]*) echo "CHAIN_HOOK_TIMEOUT_S=$HOOK_TIMEOUT is not a whole number of seconds"; exit 2 ;;
esac

HELD=none
if [ "$LOCK" != none ]; then
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
  HELD="$LOCK"
fi

stamp() { date -u +%FT%TZ; }

HOOK_RC=0
HOOK_SECS=0
hook() {
  local name="$1" cmd="$2" t0 pid now killed=0 again
  t0="$(date +%s)"
  echo "$(stamp) $name hook: $cmd"
  setsid bash -c "$cmd" </dev/null 9>&- &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    now="$(date +%s)"
    if [ "$HOOK_TIMEOUT" -gt 0 ] && [ $((now - t0)) -ge "$HOOK_TIMEOUT" ]; then
      if [ "$killed" = 0 ]; then
        kill -s TERM -- "-$pid" 2>/dev/null
        killed="$now"
      elif [ $((now - killed)) -ge 30 ]; then
        kill -s KILL -- "-$pid" 2>/dev/null
      fi
    fi
    sleep 1
  done
  wait "$pid"
  HOOK_RC=$?
  if [ "$HOOK_RC" -gt 128 ]; then
    wait "$pid" 2>/dev/null
    again=$?
    if [ "$again" -ne 127 ]; then
      HOOK_RC=$again
    fi
  fi
  if [ "$killed" != 0 ]; then
    HOOK_RC=124
  fi
  HOOK_SECS=$(( $(date +%s) - t0 ))
  case "$HOOK_RC" in
    0) echo "$(stamp) $name hook: done in ${HOOK_SECS}s" ;;
    124) echo "$(stamp) $name hook: timed out after ${HOOK_SECS}s (CHAIN_HOOK_TIMEOUT_S=$HOOK_TIMEOUT)" ;;
    *) echo "$(stamp) $name hook: failed (exit $HOOK_RC) after ${HOOK_SECS}s" ;;
  esac
  return "$HOOK_RC"
}

RC=0
STOP=""
CHILD=""
PAUSED=0
RESUMED=0
on_signal() {
  STOP="$1"
  if [ -n "$CHILD" ]; then
    kill -s TERM "$CHILD" 2>/dev/null
  fi
}
resume_once() {
  if [ "$PAUSED" != 1 ] || [ "$RESUMED" = 1 ]; then
    return 0
  fi
  RESUMED=1
  if [ -z "$RESUME_CMD" ]; then
    echo "$(stamp) no CHAIN_RESUME_CMD: what the pause hook stopped stays stopped"
    return 0
  fi
  export CHAIN_EXIT="$RC"
  hook resume "$RESUME_CMD" || true
}
trap 'on_signal TERM' TERM
trap 'on_signal INT' INT
trap 'on_signal HUP' HUP
trap resume_once EXIT

PAUSE_STATUS=""
if [ -n "$PAUSE_CMD" ]; then
  if hook pause "$PAUSE_CMD"; then
    PAUSED=1
    PAUSE_STATUS=paused
  else
    case "$HOOK_RC" in
      124) PAUSE_STATUS=timeout ;;
      *) PAUSE_STATUS=failed ;;
    esac
    echo "$(stamp) the run goes on without the pause, and nothing will be resumed"
  fi
fi

if [ -z "$STOP" ]; then
  CHAIN_LOCK_HELD="$HELD" CHAIN_PAUSE_STATUS="$PAUSE_STATUS" CHAIN_PAUSE_EXIT="$HOOK_RC" CHAIN_PAUSE_SECS="$HOOK_SECS" \
    node "$DIR/run.mjs" "$@" &
  CHILD=$!
  if [ -n "$STOP" ]; then
    kill -s TERM "$CHILD" 2>/dev/null
  fi
  while :; do
    wait "$CHILD"
    RC=$?
    if [ "$RC" -gt 128 ] && kill -0 "$CHILD" 2>/dev/null; then
      continue
    fi
    break
  done
  if [ "$RC" -gt 128 ]; then
    wait "$CHILD" 2>/dev/null
    LAST=$?
    if [ "$LAST" -ne 127 ]; then
      RC=$LAST
    fi
  fi
else
  case "$STOP" in
    INT) RC=130 ;;
    HUP) RC=129 ;;
    *) RC=143 ;;
  esac
  echo "$(stamp) stopped on SIG$STOP before the run started"
fi
resume_once
exit "$RC"
