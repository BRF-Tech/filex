# Sourced by the installers of the build host's timers
# (scripts/chain/install-nightly.sh, scripts/chain/install-langpacks.sh,
# scripts/train/install-auto.sh): whether a job runner on this host starts a
# job already, so the installer does not give it a systemd timer as well.
#
# ⚠ Task #362 (2026-11-18): the build host's jobs - the nightly, its report,
# the language packs' night, the release round and its report - moved from
# their systemd timers to the host's job runner (brf-watchdog), which starts
# the jobs its list names (one job id a line, `#` starts a comment) from its
# own copy of `main` and logs each one to /var/log/brf-jobs/<id>.log. An
# installer run with --enable after that turned the timer back on, and every
# job ran twice a night (the runner warns about a job with a timer of its
# own). --runner-list names another list; a host without one is left as it was.

JOB_RUNNER_LIST_DEFAULT=/etc/brf-watchdog/jobs.enabled

# runner_jobs <list> <id>...: the ids the list names, one a line (none when
# there is no list).
runner_jobs() {
  local list="$1" id
  shift
  [ -r "$list" ] || return 0
  for id in "$@"; do
    if sed -e 's/#.*//' -e 's/[[:space:]]//g' "$list" | grep -qxF -- "$id"; then echo "$id"; fi
  done
  return 0
}

# runner_refuse <installer> <list> <id>...: when --enable meets jobs the
# runner starts, say so and why the timers stay off.
runner_refuse() {
  local who="$1" list="$2"
  shift 2
  echo "$who: $list names $* - the job runner on this host starts them, and a timer would start each one a second time." >&2
  echo "$who: the units are installed and left off. Take the jobs off $list first to hand them back to systemd." >&2
}

# runner_hints <installer> <id>...: how to look at and start jobs the runner owns.
runner_hints() {
  local who="$1" id
  shift
  echo "$who: the job runner on this host starts $* (not the timers):"
  echo "$who:   docker exec brf-watchdog watchdog jobs status"
  for id in "$@"; do
    echo "$who:   docker exec brf-watchdog watchdog jobs run $id; tail -f /var/log/brf-jobs/$id.log"
  done
}
