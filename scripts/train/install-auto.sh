#!/usr/bin/env bash
# Install the autonomous release round's timers on the build host (task
# #165): the round runs in the release workspace (a container on this host)
# every half hour and does something only when a release is queued
# (scripts/train/auto.mjs request); the morning report goes out at 07:00.
# What the round does: scripts/train/auto.mjs and docs/CONTRIBUTING.md ->
# "The autonomous release round".
#
#   sudo bash scripts/train/install-auto.sh --root /var/lib/filex-auto \
#        --env /etc/filex-release-auto.env --container <the workspace container> \
#        [--user <user in it>] [--checkout <private checkout in it>] \
#        [--notify-from <a settings file with CHAIN_NOTIFY_* or FILEX_NOTIFY_*>] \
#        [--every "*:0/30"] [--report-at "07:00 Europe/Istanbul"] [--enable] [--dry-run]
#
# It never overwrites the settings file and never deletes anything:
#   1. copies auto-host.mjs and what it imports (scripts/lib/notify.mjs,
#      scripts/lib/settings.mjs, scripts/chain/env.mjs, scripts/release/engine.mjs)
#      from THIS checkout to <root>/bin, in the same layout (re-run this script
#      to update them);
#   2. writes the settings file when there is none yet: the container, the
#      user, the checkout, and the notification keys copied from
#      --notify-from (the URL and the key FILE's path - never a key);
#   3. installs filex-release-auto.{service,timer} and
#      filex-release-auto-report.{service,timer} into /etc/systemd/system and
#      reloads systemd. With --enable it also enables and starts both timers;
#      without, it prints the command.
#
# --dry-run prints every step and changes nothing. Linux, root, systemd,
# Docker, node 20 or later.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPTS="$(cd "$HERE/.." && pwd)"
ROOT=""
ENV_FILE=""
CONTAINER=""
WS_USER=""
CHECKOUT=""
NOTIFY_FROM=""
EVERY="*:0/30"
REPORT_AT="07:00 Europe/Istanbul"
UNIT_DIR="/etc/systemd/system"
ENABLE=0
DRY=0

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "${BASH_SOURCE[0]}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --root) ROOT="${2:?--root needs a directory}"; shift 2 ;;
    --env) ENV_FILE="${2:?--env needs a file}"; shift 2 ;;
    --container) CONTAINER="${2:?--container needs a name}"; shift 2 ;;
    --user) WS_USER="${2:?--user needs a name}"; shift 2 ;;
    --checkout) CHECKOUT="${2:?--checkout needs a path}"; shift 2 ;;
    --notify-from) NOTIFY_FROM="${2:?--notify-from needs a file}"; shift 2 ;;
    --every) EVERY="${2:?--every needs a calendar spec}"; shift 2 ;;
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

say() { echo "install-auto: $*"; }
run() {
  if [ "$DRY" = 1 ]; then
    echo "+ $*"
  else
    "$@"
  fi
}

[ "$(uname -s)" = Linux ] || { echo "Linux only" >&2; exit 2; }
if [ "$DRY" = 0 ] && [ "$(id -u)" != 0 ]; then
  echo "run it as root: the units go to $UNIT_DIR and the round runs docker exec" >&2
  exit 2
fi
missing=""
for tool in docker systemctl node; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ -z "$missing" ] || { echo "missing on this host:$missing" >&2; exit 2; }
NODE="$(command -v node)"
major="$("$NODE" -p 'process.versions.node.split(".")[0]')"
[ "$major" -ge 20 ] || { echo "node $major: the round's host side needs node 20 or later" >&2; exit 2; }

say "root $ROOT, settings $ENV_FILE, a round $EVERY, the report at $REPORT_AT"

run mkdir -p "$ROOT/bin/train" "$ROOT/bin/lib" "$ROOT/bin/chain" "$ROOT/bin/release"
run install -m 0644 "$SCRIPTS/train/auto-host.mjs" "$ROOT/bin/train/auto-host.mjs"
run install -m 0644 "$SCRIPTS/lib/notify.mjs" "$ROOT/bin/lib/notify.mjs"
run install -m 0644 "$SCRIPTS/lib/settings.mjs" "$ROOT/bin/lib/settings.mjs"
run install -m 0644 "$SCRIPTS/chain/env.mjs" "$ROOT/bin/chain/env.mjs"
run install -m 0644 "$SCRIPTS/release/engine.mjs" "$ROOT/bin/release/engine.mjs"

# One KEY=VALUE line of a settings file, or nothing.
setting_of() { sed -n "s/^$2=//p" "$1" | tail -n 1; }

if [ -e "$ENV_FILE" ]; then
  say "settings file $ENV_FILE is there already: left as it is"
else
  [ -n "$CONTAINER" ] || { echo "--container is required for the first install (there is no $ENV_FILE)" >&2; exit 2; }
  url=""; key=""; group=""
  if [ -n "$NOTIFY_FROM" ]; then
    [ -r "$NOTIFY_FROM" ] || { echo "--notify-from $NOTIFY_FROM cannot be read" >&2; exit 2; }
    url="$(setting_of "$NOTIFY_FROM" FILEX_NOTIFY_URL)"; [ -n "$url" ] || url="$(setting_of "$NOTIFY_FROM" CHAIN_NOTIFY_URL)"
    key="$(setting_of "$NOTIFY_FROM" FILEX_NOTIFY_KEY_FILE)"; [ -n "$key" ] || key="$(setting_of "$NOTIFY_FROM" CHAIN_NOTIFY_KEY_FILE)"
    group="$(setting_of "$NOTIFY_FROM" FILEX_NOTIFY_GROUP)"; [ -n "$group" ] || group="$(setting_of "$NOTIFY_FROM" CHAIN_NOTIFY_GROUP)"
  fi
  settings="$(cat <<EOF
# The build host's half of the autonomous release round
# (scripts/train/auto-host.mjs; written by scripts/train/install-auto.sh).
FILEX_AUTO_CONTAINER=$CONTAINER
FILEX_AUTO_USER=$WS_USER
FILEX_AUTO_CHECKOUT=$CHECKOUT
FILEX_AUTO_WRAP=with-gh-token
FILEX_AUTO_STATE_DIR=$ROOT
FILEX_NOTIFY_URL=$url
FILEX_NOTIFY_KEY_FILE=$key
FILEX_NOTIFY_GROUP=${group:-infra}
FILEX_NOTIFY_SOURCE=filex-release-auto
EOF
)"
  if [ "$DRY" = 1 ]; then
    say "would write $ENV_FILE:"
    printf '%s\n' "$settings" | sed 's/^/    /'
  else
    mkdir -p "$(dirname "$ENV_FILE")"
    printf '%s\n' "$settings" > "$ENV_FILE"
    chmod 0600 "$ENV_FILE"
    say "wrote $ENV_FILE"
  fi
fi

render() {
  sed -e "s|@NODE@|$NODE|g" -e "s|@BIN@|$ROOT/bin|g" -e "s|@ENV@|$ENV_FILE|g" \
      -e "s|@EVERY@|$EVERY|g" -e "s|@REPORT_AT@|$REPORT_AT|g" "$HERE/systemd/$1"
}
for unit in filex-release-auto.service filex-release-auto.timer filex-release-auto-report.service filex-release-auto-report.timer; do
  if [ "$DRY" = 1 ]; then
    echo "+ render scripts/train/systemd/$unit > $UNIT_DIR/$unit"
    render "$unit" | grep -E '^(ExecStart|OnCalendar)=' | sed 's/^/    /'
  else
    render "$unit" > "$UNIT_DIR/$unit"
    chmod 0644 "$UNIT_DIR/$unit"
  fi
done
run systemctl daemon-reload
if command -v systemd-analyze >/dev/null 2>&1; then
  systemd-analyze calendar "$EVERY" >/dev/null || { echo "systemd cannot read \"$EVERY\"" >&2; exit 1; }
  systemd-analyze calendar "*-*-* $REPORT_AT" >/dev/null || { echo "systemd cannot read the time \"$REPORT_AT\"" >&2; exit 1; }
fi

if [ "$ENABLE" = 1 ]; then
  run systemctl enable --now filex-release-auto.timer filex-release-auto-report.timer
else
  say "not enabled. Try it by hand first, then enable the timers:"
  say "  $NODE $ROOT/bin/train/auto-host.mjs run --env $ENV_FILE --dry-run"
  say "  systemctl start filex-release-auto.service; journalctl -fu filex-release-auto.service"
  say "  systemctl enable --now filex-release-auto.timer filex-release-auto-report.timer"
fi
