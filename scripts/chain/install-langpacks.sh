#!/usr/bin/env bash
# Install the language packs' nightly translation on the build host (task
# #177), next to the nightly test run it follows (install-nightly.sh): its
# own directory, the pack checkouts, the settings file and a systemd timer.
# What a night does: scripts/langpacks-nightly.mjs and docs/CONTRIBUTING.md ->
# "Translations and language packs".
#
#   sudo bash scripts/chain/install-langpacks.sh --root /var/lib/filex-langpacks \
#        --env /etc/filex-langpacks.env --git-name "<name>" --git-email "<email>" \
#        [--pack <tag>=<clone URL>]... [--init <tag>[,<tag>...]] \
#        [--at "03:30 Europe/Istanbul"] [--enable] [--dry-run]
#
# It never overwrites the settings file and never deletes anything:
#   1. copies scripts/langpacks-nightly.mjs and every module it imports from
#      THIS checkout to <root>/bin/scripts (the timer runs those copies - re-run
#      this script to update them);
#   2. for every --pack, clones the pack into <root>/filex-lang-<tag> when it
#      is not there yet; for every --init tag, makes an empty checkout there,
#      for a pack that has no clone URL: the maintainer pushes it in from
#      their own checkout (`git push nightly main`, CONTRIBUTING);
#   3. in every filex-lang-* checkout under <root>: the commit identity of the
#      nightly commits (--git-name, --git-email; one already set is kept) and
#      receive.denyCurrentBranch=updateInstead, so release day can push the
#      release commit back into the checked-out branch;
#   4. writes the settings file from scripts/langpacks-nightly.env.example
#      with LANGPACKS_ROOT filled in, when there is none yet - set the work
#      server, the project token file and the notification key before the
#      first night;
#   5. installs filex-langpacks.{service,timer} into /etc/systemd/system and
#      reloads systemd. With --enable it also enables and starts the timer;
#      without, it prints the command.
#
# It installs no agent: Claude Code is checked for, and named when missing.
# --dry-run prints every step and changes nothing. Linux, root, systemd, git,
# node 20 or later.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPTS="$(cd "$HERE/.." && pwd)"
ROOT=""
ENV_FILE=""
GIT_NAME=""
GIT_EMAIL=""
PACKS=()
INITS=""
AT="03:30 Europe/Istanbul"
UNIT_DIR="/etc/systemd/system"
ENABLE=0
DRY=0

# The driver and every module it imports, as paths under scripts/. A test
# (web/tests/i18n/langPacksNightly.test.ts) follows the imports and holds
# this list complete.
FILES="langpacks-nightly.mjs lib/langpacks-nightly.mjs lib/langpacks-prompt.md lib/langpacks.mjs lib/i18n-catalogue.mjs lib/notify.mjs lib/settings.mjs i18n-validate.mjs chain/env.mjs chain/nightly-lib.mjs release/engine.mjs train/cli.mjs"

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "${BASH_SOURCE[0]}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --root) ROOT="${2:?--root needs a directory}"; shift 2 ;;
    --env) ENV_FILE="${2:?--env needs a file}"; shift 2 ;;
    --git-name) GIT_NAME="${2:?--git-name needs a name}"; shift 2 ;;
    --git-email) GIT_EMAIL="${2:?--git-email needs an address}"; shift 2 ;;
    --pack) PACKS+=("${2:?--pack needs <tag>=<clone URL>}"); shift 2 ;;
    --init) INITS="${2:?--init needs a tag list}"; shift 2 ;;
    --at) AT="${2:?--at needs a time}"; shift 2 ;;
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

say() { echo "install-langpacks: $*"; }
run() {
  if [ "$DRY" = 1 ]; then
    echo "+ $*"
  else
    "$@"
  fi
}

[ "$(uname -s)" = Linux ] || { echo "Linux only" >&2; exit 2; }
if [ "$DRY" = 0 ] && [ "$(id -u)" != 0 ]; then
  echo "run it as root: the units go to $UNIT_DIR" >&2
  exit 2
fi
missing=""
for tool in git systemctl node; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ -z "$missing" ] || { echo "missing on this host:$missing" >&2; exit 2; }
NODE="$(command -v node)"
major="$("$NODE" -p 'process.versions.node.split(".")[0]')"
[ "$major" -ge 20 ] || { echo "node $major: the translation needs node 20 or later" >&2; exit 2; }

say "root $ROOT, settings $ENV_FILE, run at $AT"

# 1. the driver and its modules
for f in $FILES; do
  [ -f "$SCRIPTS/$f" ] || { echo "scripts/$f is missing in this checkout" >&2; exit 1; }
  run install -D -m 0644 "$SCRIPTS/$f" "$ROOT/bin/scripts/$f"
done

# 2. the pack checkouts
run mkdir -p "$ROOT"
for spec in "${PACKS[@]+"${PACKS[@]}"}"; do
  tag="${spec%%=*}"
  url="${spec#*=}"
  [ -n "$tag" ] && [ -n "$url" ] && [ "$tag" != "$spec" ] || { echo "--pack $spec: write <tag>=<clone URL>" >&2; exit 2; }
  dir="$ROOT/filex-lang-$tag"
  if [ -d "$dir/.git" ]; then
    say "filex-lang-$tag is there already (its remotes are left as they are)"
  elif [ -e "$dir" ]; then
    echo "$dir exists and is not a git checkout: move it away first" >&2
    exit 1
  else
    run git clone --quiet "$url" "$dir"
  fi
done
for tag in ${INITS//,/ }; do
  dir="$ROOT/filex-lang-$tag"
  if [ -e "$dir" ]; then
    say "filex-lang-$tag is there already: not made again"
  else
    say "filex-lang-$tag: an empty checkout, to be filled by a push from the maintainer checkout (git push nightly main)"
    run git init --quiet --initial-branch=main "$dir"
  fi
done

# 3. what every pack checkout needs
found=0
for dir in "$ROOT"/filex-lang-*; do
  [ -d "$dir/.git" ] || continue
  found=1
  if [ -z "$(git -C "$dir" config --local user.email || true)" ]; then
    if [ -n "$GIT_EMAIL" ] && [ -n "$GIT_NAME" ]; then
      run git -C "$dir" config --local user.name "$GIT_NAME"
      run git -C "$dir" config --local user.email "$GIT_EMAIL"
    else
      say "$(basename "$dir") has no commit identity: pass --git-name and --git-email, or the nightly commits fail"
    fi
  fi
  run git -C "$dir" config --local receive.denyCurrentBranch updateInstead
done
[ "$found" = 1 ] || [ "$DRY" = 1 ] || say "no filex-lang-* checkout under $ROOT yet: --pack <tag>=<URL> or --init <tag>"

# 4. the settings
if [ -e "$ENV_FILE" ]; then
  say "settings file $ENV_FILE is there already: left as it is"
else
  say "writing $ENV_FILE from scripts/langpacks-nightly.env.example: set the work server, its token file and the notification key before the first night"
  if [ "$DRY" = 1 ]; then
    echo "+ sed 's|^LANGPACKS_ROOT=.*|LANGPACKS_ROOT=$ROOT|' $SCRIPTS/langpacks-nightly.env.example > $ENV_FILE"
  else
    mkdir -p "$(dirname "$ENV_FILE")"
    sed "s|^LANGPACKS_ROOT=.*|LANGPACKS_ROOT=$ROOT|" "$SCRIPTS/langpacks-nightly.env.example" > "$ENV_FILE"
    chmod 0600 "$ENV_FILE"
  fi
fi

# 5. the timer
render() {
  sed -e "s|@NODE@|$NODE|g" -e "s|@BIN@|$ROOT/bin|g" -e "s|@ENV@|$ENV_FILE|g" -e "s|@AT@|$AT|g" "$HERE/systemd/$1"
}
for unit in filex-langpacks.service filex-langpacks.timer; do
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
  systemd-analyze calendar "*-*-* $AT" >/dev/null || { echo "systemd cannot read the time \"$AT\"" >&2; exit 1; }
fi

if ! command -v claude >/dev/null 2>&1; then
  say "Claude Code is not installed on this host: install it (npm install -g @anthropic-ai/claude-code), then run the check below"
fi
if [ "$ENABLE" = 1 ]; then
  run systemctl enable --now filex-langpacks.timer
else
  say "not enabled. Check what a night needs, try one by hand, then enable the timer:"
  say "  $NODE $ROOT/bin/scripts/langpacks-nightly.mjs check --env $ENV_FILE --probe"
  say "  $NODE $ROOT/bin/scripts/langpacks-nightly.mjs run --env $ENV_FILE --dry-run --no-wait"
  say "  systemctl start filex-langpacks.service; journalctl -fu filex-langpacks.service"
  say "  systemctl enable --now filex-langpacks.timer"
fi
