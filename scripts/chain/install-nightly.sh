#!/usr/bin/env bash
# Install the nightly run of the test chain on a build host (task #175):
# its own directory, its own checkout of the private repository, the settings
# file, and two systemd timers - the run at night and the report in the
# morning. What the run does: scripts/chain/nightly.mjs and
# docs/CONTRIBUTING.md -> "The nightly run".
#
#   sudo bash scripts/chain/install-nightly.sh --root /var/lib/filex-nightly \
#        --env /etc/filex-nightly.env --remote <clone URL of the repository> \
#        [--branch main] [--at "01:00 Europe/Istanbul"] [--report-at "07:00 Europe/Istanbul"] \
#        [--enable] [--dry-run]
#
# It never overwrites the settings file and never deletes anything:
#   1. copies nightly.mjs, nightly-lib.mjs, report.mjs and env.mjs from THIS
#      checkout to <root>/bin (the run executes those copies, never the
#      checkout it resets every night - re-run this script to update them);
#   2. clones --remote into <root>/src when it is not there yet (a remote
#      that needs a credential: an SSH deploy key, or an https URL with a
#      read-only token - git keeps it in <root>/src/.git/config, made 0600);
#   3. writes the settings file from scripts/chain/nightly.env.example with
#      CHAIN_ROOT filled in, when there is none yet - edit it before the
#      first night: CHAIN_LOCK, the notification keys, the registry
#      credential (NIGHTLY_PUBLISH=auto pushes every green night) and the
#      live S3 bucket are yours to set;
#   4. installs filex-nightly.{service,timer} and
#      filex-nightly-report.{service,timer} into /etc/systemd/system and
#      reloads systemd. With --enable it also enables and starts both timers;
#      without, it prints the command.
#
# --dry-run prints every step and changes nothing. Linux, root, systemd,
# Docker, git, flock, node 20 or later, python3 (the export of the nightly
# build).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT=""
ENV_FILE=""
REMOTE=""
BRANCH="main"
AT="01:00 Europe/Istanbul"
REPORT_AT="07:00 Europe/Istanbul"
UNIT_DIR="/etc/systemd/system"
ENABLE=0
DRY=0

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "${BASH_SOURCE[0]}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --root) ROOT="${2:?--root needs a directory}"; shift 2 ;;
    --env) ENV_FILE="${2:?--env needs a file}"; shift 2 ;;
    --remote) REMOTE="${2:?--remote needs a URL}"; shift 2 ;;
    --branch) BRANCH="${2:?--branch needs a name}"; shift 2 ;;
    --at) AT="${2:?--at needs a time}"; shift 2 ;;
    --report-at) REPORT_AT="${2:?--report-at needs a time}"; shift 2 ;;
    --unit-dir) UNIT_DIR="${2:?--unit-dir needs a directory}"; shift 2 ;;
    --enable) ENABLE=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option $1" >&2; usage >&2; exit 2 ;;
  esac
done

[ -n "$ROOT" ] && [ -n "$ENV_FILE" ] || { echo "--root and --env are required" >&2; exit 2; }
case "$ROOT" in /*) ;; *) echo "--root must be an absolute path" >&2; exit 2 ;; esac
case "$ENV_FILE" in /*) ;; *) echo "--env must be an absolute path" >&2; exit 2 ;; esac

say() { echo "install-nightly: $*"; }
run() {
  if [ "$DRY" = 1 ]; then
    echo "+ $*"
  else
    "$@"
  fi
}

[ "$(uname -s)" = Linux ] || { echo "Linux only" >&2; exit 2; }
if [ "$DRY" = 0 ] && [ "$(id -u)" != 0 ]; then
  echo "run it as root: the units go to $UNIT_DIR and the run starts containers" >&2
  exit 2
fi
missing=""
for tool in git flock docker systemctl python3 node; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ -z "$missing" ] || { echo "missing on this host:$missing" >&2; exit 2; }
NODE="$(command -v node)"
major="$("$NODE" -p 'process.versions.node.split(".")[0]')"
[ "$major" -ge 20 ] || { echo "node $major: the nightly run needs node 20 or later" >&2; exit 2; }

say "root $ROOT, settings $ENV_FILE, branch $BRANCH, run at $AT, report at $REPORT_AT"

run mkdir -p "$ROOT/bin" "$ROOT/nightly" "$ROOT/runs"
for f in nightly.mjs nightly-lib.mjs report.mjs env.mjs; do
  run install -m 0644 "$HERE/$f" "$ROOT/bin/$f"
done

if [ -d "$ROOT/src/.git" ]; then
  say "checkout $ROOT/src is there already (its remote is left as it is)"
elif [ -e "$ROOT/src" ]; then
  echo "$ROOT/src exists and is not a git checkout: move it away first" >&2
  exit 1
else
  [ -n "$REMOTE" ] || { echo "--remote is required for the first install (there is no $ROOT/src)" >&2; exit 2; }
  run git clone --quiet --branch "$BRANCH" "$REMOTE" "$ROOT/src"
  run chmod 0600 "$ROOT/src/.git/config"
fi

if [ -e "$ENV_FILE" ]; then
  say "settings file $ENV_FILE is there already: left as it is"
else
  say "writing $ENV_FILE from scripts/chain/nightly.env.example: set CHAIN_LOCK, the notification keys, the registry credential and the live S3 bucket before the first night"
  if [ "$DRY" = 1 ]; then
    echo "+ sed 's|^CHAIN_ROOT=.*|CHAIN_ROOT=$ROOT|; s|^NIGHTLY_BRANCH=.*|NIGHTLY_BRANCH=$BRANCH|' $HERE/nightly.env.example > $ENV_FILE"
  else
    mkdir -p "$(dirname "$ENV_FILE")"
    sed "s|^CHAIN_ROOT=.*|CHAIN_ROOT=$ROOT|; s|^NIGHTLY_BRANCH=.*|NIGHTLY_BRANCH=$BRANCH|" "$HERE/nightly.env.example" > "$ENV_FILE"
    chmod 0600 "$ENV_FILE"
  fi
fi

render() {
  sed -e "s|@NODE@|$NODE|g" -e "s|@BIN@|$ROOT/bin|g" -e "s|@ENV@|$ENV_FILE|g" \
      -e "s|@AT@|$AT|g" -e "s|@REPORT_AT@|$REPORT_AT|g" "$HERE/systemd/$1"
}
for unit in filex-nightly.service filex-nightly.timer filex-nightly-report.service filex-nightly-report.timer; do
  if [ "$DRY" = 1 ]; then
    echo "+ render scripts/chain/systemd/$unit > $UNIT_DIR/$unit"
    render "$unit" | grep -E '^(ExecStart|OnCalendar)=' | sed 's/^/    /'
  else
    render "$unit" > "$UNIT_DIR/$unit"
    chmod 0644 "$UNIT_DIR/$unit"
  fi
done
run systemctl daemon-reload
if command -v systemd-analyze >/dev/null 2>&1; then
  for when in "$AT" "$REPORT_AT"; do
    systemd-analyze calendar "*-*-* $when" >/dev/null || { echo "systemd cannot read the time \"$when\"" >&2; exit 1; }
  done
fi

if [ "$ENABLE" = 1 ]; then
  run systemctl enable --now filex-nightly.timer filex-nightly-report.timer
else
  say "not enabled. Check the settings, try a night by hand, then enable the timers:"
  say "  $NODE $ROOT/bin/nightly.mjs run --env $ENV_FILE --dry-run"
  say "  systemctl start filex-nightly.service; journalctl -fu filex-nightly.service"
  say "  systemctl enable --now filex-nightly.timer filex-nightly-report.timer"
fi
