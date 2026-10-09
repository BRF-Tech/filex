#!/usr/bin/env bash
# An installed copy of the previous release updates to this build, on Linux.
#
#   desktop-upgrade-linux.sh --old <dir> --new <dir> --label <name> --shots <dir>
#                            --deb amd64|arm64 --img x86_64|arm64 --snap amd64|arm64
#
# <dir> holds the packages under their Release names (filex-desktop-<deb>.deb,
# filex-desktop-<img>.AppImage, filex-desktop-<snap>.snap).
#
#   .deb      the previous release's .deb installed, opened (sandboxed) and
#             closed, a marker left in the app's user data
#             (~/.config/@brftech/filex-desktop); then this build's .deb
#             installed over it the way the app's updater does it
#             (electron-updater's DebUpdater: `dpkg -i`, then
#             `apt-get install -f` if that fails). After: one filex package,
#             this version, /usr/bin/filex-app still the launcher, the
#             setuid sandbox helper still setuid root and no AppArmor profile
#             of electron-builder's (desktop/build/linux/after-install.tpl),
#             the marker and the files the previous version wrote still there,
#             the app opens sandboxed, and its user data is still in one place.
#   AppImage  replaced in place, as electron-updater's AppImageUpdater does
#             (same file name, new file moved over the old): the image at
#             that path then says this version.
#   snap      the previous release's snap installed, opened (confined) and
#             closed, markers in $SNAP_USER_DATA and $SNAP_USER_COMMON; then
#             this build's installed over it (a new revision, as a refresh
#             makes; 0.54 -> 0.55 is core20 -> core24). After: this version,
#             both markers carried over, the app opens confined, and keeps
#             its user data where the previous revision kept it.
#
# Everything is removed again at the end, so the checks after this one start
# from a clean machine.
#
# ⚠ Why (0.55, #68): the desktop packages moved from electron-builder 24 to
# 26 and the snap from core20 to core24. A fresh install of each package is
# checked; what an installed copy goes through on an update (the new
# package's scripts run over the old one's files, snapd's copy of the user's
# data to a revision on another base) was checked nowhere.
set -euo pipefail

old='' new='' label='' shots='' deb='' img='' snap=''
while [ $# -gt 0 ]; do
  case "$1" in
    --old) old=$2; shift 2 ;;
    --new) new=$2; shift 2 ;;
    --label) label=$2; shift 2 ;;
    --shots) shots=$2; shift 2 ;;
    --deb) deb=$2; shift 2 ;;
    --img) img=$2; shift 2 ;;
    --snap) snap=$2; shift 2 ;;
    *) echo "unknown argument $1" >&2; exit 2 ;;
  esac
done
for v in old new label shots deb img snap; do
  [ -n "${!v}" ] || { echo "missing --$v" >&2; exit 2; }
done
old=$(realpath "$old")
new=$(realpath "$new")
mkdir -p "$shots"
shots=$(realpath "$shots")
here=$(cd "$(dirname "$0")" && pwd)

fail() { echo "::error title=desktop upgrade ($label)::$*"; exit 1; }
look() { xvfb-run -a -s "-screen 0 1280x800x24" node "$here"/desktop-look.mjs "$@"; }
user_data_dirs() { find "$1" -maxdepth 6 -type d -path '*/@brftech/filex-desktop' 2>/dev/null | sort; }
count() { printf '%s' "$1" | grep -c . || true; }
app_version() { printf '%s' "$1" | tr '~' '-'; }
token=$(cat /proc/sys/kernel/random/uuid)
MARK=filex-upgrade-check.txt

# ── .deb ───────────────────────────────────────────────────────────────────
olddeb="$old/filex-desktop-$deb.deb"
newdeb="$new/filex-desktop-$deb.deb"
[ -f "$olddeb" ] || fail "the previous release has no $(basename "$olddeb")"
[ -f "$newdeb" ] || fail "this build has no $(basename "$newdeb")"
oldv=$(dpkg-deb -f "$olddeb" Version)
newv=$(dpkg-deb -f "$newdeb" Version)
echo "previous release: $oldv  this build: $newv"
if [ "$oldv" = "$newv" ]; then
  echo "::warning title=desktop upgrade ($label)::the previous release and this build are both $newv: the same version installs over itself"
fi
if dpkg-query -W filex-app > /dev/null 2>&1; then fail "the machine is not clean: filex-app is installed before the test"; fi
conf="$HOME/.config/@brftech/filex-desktop"
[ ! -e "$conf" ] || fail "the machine is not clean: $conf exists before the test"

sudo apt-get update -qq
sudo apt-get install -y -qq xvfb
sudo apt-get install -y "$olddeb"
[ "$(dpkg-query -W -f='${Version}' filex-app)" = "$oldv" ] || fail "the previous .deb did not install as $oldv"
look --exe /usr/bin/filex-app --out "$shots/$label-upgrade-deb-previous.png" --port 9340 --expect-sandbox
[ -d "$conf" ] || { user_data_dirs "$HOME"; fail "the previous release wrote no user data to $conf"; }
printf '%s' "$token" > "$conf/$MARK"
kept=()
for n in 'Local State' Preferences desktop-state.bin logs; do
  if [ -e "$conf/$n" ]; then kept+=("$n"); fi
done
echo "user data before the update: $(ls -A "$conf" | tr '\n' ' ')"

if ! sudo dpkg -i "$newdeb"; then
  echo 'dpkg -i failed; apt-get install -f, as the updater does'
  sudo apt-get install -f -y
fi

[ "$(dpkg-query -W -f='${Version}' filex-app)" = "$newv" ] || fail "after the update dpkg says filex-app $(dpkg-query -W -f='${Version}' filex-app), this build is $newv"
installed=$(dpkg-query -W -f='${Package} ${Version} ${db:Status-Abbrev}\n' 'filex*' 2>/dev/null | awk '$3 ~ /^ii/' || true)
echo "installed: $installed"
[ "$(count "$installed")" = 1 ] || fail "after the update more than one filex package is installed: $installed"
[ "$(readlink -f /usr/bin/filex-app)" = /opt/filex/filex-app ] || fail "/usr/bin/filex-app leads to $(readlink -f /usr/bin/filex-app), not /opt/filex/filex-app"
head -1 /opt/filex/filex-app | grep -qx '#!/bin/sh' || fail '/opt/filex/filex-app is not the launcher'
if ! { [ -u /opt/filex/chrome-sandbox ] && [ "$(stat -c %u /opt/filex/chrome-sandbox)" = 0 ]; }; then
  fail "the update left /opt/filex/chrome-sandbox $(stat -c '%A %U' /opt/filex/chrome-sandbox): not setuid root"
fi
[ ! -e /etc/apparmor.d/filex-app ] || fail 'the update installed /etc/apparmor.d/filex-app (electron-builder 26 after-install)'
[ "$(cat "$conf/$MARK" 2>/dev/null)" = "$token" ] || fail "the update lost the user's data: $conf/$MARK"
for n in ${kept[@]+"${kept[@]}"}; do
  [ -e "$conf/$n" ] || fail "the update deleted $n from $conf"
done
look --exe /usr/bin/filex-app --out "$shots/$label-upgrade-deb-updated.png" --port 9341 --expect-sandbox
[ "$(cat "$conf/$MARK" 2>/dev/null)" = "$token" ] || fail "the updated app lost $conf/$MARK"
places=$(user_data_dirs "$HOME/.config")
[ "$(count "$places")" = 1 ] || fail "the user data is in more than one place: $places"
echo "user data after the update: $(ls -A "$conf" | tr '\n' ' ')"
sudo apt-get purge -y filex-app
rm -rf "$HOME/.config/@brftech"
echo "### Desktop update ($label, .deb): $oldv -> $newv, one package, sandbox helper setuid, user data kept" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

# ── AppImage ───────────────────────────────────────────────────────────────
oldimg="$old/filex-desktop-$img.AppImage"
newimg="$new/filex-desktop-$img.AppImage"
[ -f "$oldimg" ] || fail "the previous release has no $(basename "$oldimg")"
[ -f "$newimg" ] || fail "this build has no $(basename "$newimg")"
apps="$HOME/Apps/upgrade"
mkdir -p "$apps"
target="$apps/filex-desktop-$img.AppImage"
image_version() {
  local d
  d=$(mktemp -d)
  (cd "$d" && APPIMAGE_EXTRACT_AND_RUN=1 "$1" --appimage-extract '*.desktop' > /dev/null)
  sed -n 's/^X-AppImage-Version=//p' "$d"/squashfs-root/*.desktop | head -1
  rm -rf "$d"
}
cp "$oldimg" "$target"
chmod +x "$target"
v=$(image_version "$target")
[ "$v" = "$(app_version "$oldv")" ] || fail "the previous AppImage says $v, its release is $oldv"
cp "$newimg" "$target.new"
chmod +x "$target.new"
mv -f "$target.new" "$target"
v=$(image_version "$target")
[ "$v" = "$(app_version "$newv")" ] || fail "the AppImage at $target says $v after the update, this build is $newv"
[ ! -e "$target.new" ] || fail "the update left $target.new behind"
rm -rf "$apps"
echo "### Desktop update ($label, AppImage): $oldv -> $newv in place" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

# ── snap ───────────────────────────────────────────────────────────────────
oldsnap="$old/filex-desktop-$snap.snap"
newsnap="$new/filex-desktop-$snap.snap"
if [ ! -f "$oldsnap" ] || [ ! -f "$newsnap" ]; then
  echo "::warning title=desktop upgrade ($label)::no snap update checked: $( [ -f "$oldsnap" ] || printf 'the previous release has no %s ' "$(basename "$oldsnap")")$( [ -f "$newsnap" ] || printf 'this build has no %s' "$(basename "$newsnap")")"
  exit 0
fi
if snap list filex-app > /dev/null 2>&1; then fail 'the machine is not clean: the filex-app snap is installed before the test'; fi
rev() { snap list filex-app | awk 'NR==2 {print $3}'; }
ver() { snap list filex-app | awk 'NR==2 {print $2}'; }
sudo snap install --dangerous "$oldsnap"
rev1=$(rev)
[ "$(ver)" = "$(app_version "$oldv")" ] || fail "the previous snap says $(ver), its release is $oldv"
look --exe /snap/bin/filex-app --out "$shots/$label-upgrade-snap-previous.png" --port 9342 --timeout 150 --expect-snap-confinement
sdir="$HOME/snap/filex-app"
first=$(user_data_dirs "$sdir/$rev1")
[ "$(count "$first")" = 1 ] || { ls -laR "$sdir" 2> /dev/null | head -60 || true; fail "the previous snap's user data: ${first:-none found under $sdir/$rev1}"; }
rel=${first#"$sdir/$rev1/"}
echo "the snap keeps its user data in \$SNAP_USER_DATA/$rel"
printf '%s' "$token" > "$first/$MARK"
mkdir -p "$sdir/common"
printf '%s' "$token" > "$sdir/common/$MARK"

sudo snap install --dangerous "$newsnap"
rev2=$(rev)
snap list filex-app
[ "$rev2" != "$rev1" ] || fail "this build's snap did not install as a new revision (still $rev1)"
[ "$(ver)" = "$(app_version "$newv")" ] || fail "after the update the snap says $(ver), this build is $newv"
[ "$(readlink "$sdir/current")" = "$rev2" ] || fail "$sdir/current leads to $(readlink "$sdir/current"), not $rev2"
[ "$(cat "$sdir/$rev2/$rel/$MARK" 2>/dev/null)" = "$token" ] || fail "the refresh did not carry \$SNAP_USER_DATA over: no $sdir/$rev2/$rel/$MARK"
[ "$(cat "$sdir/common/$MARK" 2>/dev/null)" = "$token" ] || fail "the refresh lost \$SNAP_USER_COMMON: no $sdir/common/$MARK"
look --exe /snap/bin/filex-app --out "$shots/$label-upgrade-snap-updated.png" --port 9343 --timeout 150 --expect-snap-confinement
now=$(user_data_dirs "$sdir/$rev2")
[ "$now" = "$sdir/$rev2/$rel" ] || fail "the updated snap keeps its user data elsewhere: ${now:-none} (the previous revision used \$SNAP_USER_DATA/$rel)"
[ "$(cat "$sdir/$rev2/$rel/$MARK" 2>/dev/null)" = "$token" ] || fail "the updated app lost $sdir/$rev2/$rel/$MARK"
sudo snap remove --purge filex-app
rm -rf "$sdir"
echo "### Desktop update ($label, snap): $oldv ($rev1) -> $newv ($rev2), \$SNAP_USER_DATA and common kept" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
echo "updated $oldv -> $newv: .deb, AppImage and snap"
