# Sourced by every job script of scripts/chain, inside the job's container.
#
# The container sees the tree under test at /w/src, this run's directory at
# /w/run, scripts/chain at /w/chain (read-only) and the caches under /w/cache.
# JOB is the job's name; what the job keeps (test output, reports) goes to
# /w/run/out/$JOB. A job ends with one `SUMMARY ...` line, which run.mjs
# copies into the run's result, and exits non-zero when it is red.

set -u
set -f
OUT="/w/run/out/${JOB:?JOB is not set}"
mkdir -p "$OUT"

summary() { echo "SUMMARY $*"; }

# The packages of PKGS (go list patterns) minus those of EXCLUDE, as import
# paths, one per line. Run from /w/src/backend.
go_packages() {
  local list excl
  list=$(go list $PKGS) || return 1
  if [ -n "${EXCLUDE:-}" ]; then
    excl=$(go list $EXCLUDE) || return 1
    printf '%s\n' "$list" | grep -vxF -f <(printf '%s\n' "$excl")
  else
    printf '%s\n' "$list"
  fi
}

# pnpm as package.json pins it.
use_pnpm() {
  corepack enable >/dev/null 2>&1 && return 0
  npm i -g "$(node -p "require('/w/src/package.json').packageManager")" >/dev/null 2>&1
}
