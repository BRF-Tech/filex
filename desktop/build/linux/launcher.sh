#!/bin/sh
# filex-app - the Linux launcher of the filex desktop app.
#
# The Electron binary is `<this file>-bin` beside it (filex-app-bin).
# desktop/scripts/linux-launcher.cjs puts the two in place when electron-builder
# packs a Linux build, so the .deb, the .rpm, the AppImage and the snap all
# start here: /usr/bin/filex-app, the menu entry, the AppImage's AppRun and the
# snap's command.sh name this file.
#
# What it is for: Chromium's sandbox, the wall between the pages the app shows
# and the account it runs as. Chromium builds it from user namespaces or, where
# those are not allowed, from the setuid helper `chrome-sandbox` beside the
# binary (the .deb and the .rpm install it setuid root; an AppImage and a snap
# cannot). Since Ubuntu 23.10 an unprivileged program may use user namespaces
# only if an AppArmor profile allows it, so an AppImage with no profile has
# neither, and Chromium stops before it shows a window, saying why only on a
# terminal nobody is looking at. This asks the same question first and, when
# the answer is no, tells the person what to do and exits.
#
# It NEVER starts the app without its sandbox, and adds no --no-sandbox of its
# own. A --no-sandbox the person typed is passed through: their decision.
#
# POSIX sh on purpose: it runs on every distribution, in the AppImage and
# inside the snap, before anything of the app's own is loaded.

self=$(readlink -f "$0" 2>/dev/null) || self=$0
[ -n "$self" ] || self=$0
here=$(dirname "$self")
bin="$self-bin"

if [ ! -x "$bin" ]; then
  echo "filex-app: $bin is missing; the installation is incomplete." >&2
  exit 127
fi

# Chromium will not ask for a sandbox at all.
for a in "$@"; do
  [ "$a" = "--no-sandbox" ] && exec "$bin" "$@"
done

# The setuid helper is in place: Chromium uses it where namespaces are refused.
helper="$here/chrome-sandbox"
if [ -u "$helper" ] && [ "$(stat -c %u "$helper" 2>/dev/null)" = "0" ]; then
  exec "$bin" "$@"
fi

kind=other
if [ -n "${SNAP:-}" ]; then
  case "$self" in "$SNAP"/*) kind=snap ;; esac
fi
[ "$kind" = other ] && [ -n "${APPIMAGE:-}" ] && kind=appimage

if [ "$kind" = snap ]; then
  # Inside a snap the question is snapd's: is the plug that lets the app build
  # the sandbox (browser-support with allow-sandbox, electron-builder.yml)
  # connected? `unshare` cannot be asked there: the snap's AppArmor profile
  # does not let it run at all (measured: "Permission denied", exit 126, even
  # with the plug connected). Only a plain "not connected" (exit 1, nothing
  # said) stops the app; an older snapd that cannot answer leaves it to Chromium.
  if command -v snapctl >/dev/null 2>&1; then
    answer=$(snapctl is-connected browser-sandbox 2>&1)
    rc=$?
    [ "$rc" = 1 ] && [ -z "$answer" ] || exec "$bin" "$@"
  else
    exec "$bin" "$@"
  fi
else
  # Can this process create a user namespace and map itself into it? That is
  # the step Chromium's namespace sandbox takes first (it writes uid_map), and
  # the one AppArmor refuses. Without `unshare` there is nothing to ask:
  # Chromium decides on its own.
  if ! command -v unshare >/dev/null 2>&1; then
    exec "$bin" "$@"
  fi
  if unshare -Ur true >/dev/null 2>&1; then
    exec "$bin" "$@"
  fi
fi

# ---- the sandbox cannot be built: say what to do, then stop ----------------

case "${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}" in
  tr*) lang=tr ;;
  *) lang=en ;;
esac

docs="https://docs.filex.sh/DESKTOP#appimage-on-recent-ubuntu"
[ "$kind" = snap ] && docs="https://docs.filex.sh/DESKTOP#the-snap-and-the-sandbox"

msg=$(mktemp "${TMPDIR:-/tmp}/filex-sandbox.XXXXXX" 2>/dev/null) || msg=""
say() {
  if [ -n "$msg" ]; then printf '%s\n' "$@" >> "$msg"; fi
  printf '%s\n' "$@" >&2
}

profile_lines() {
  say "" \
    "  sudo tee /etc/apparmor.d/filex-appimage > /dev/null <<'EOF'" \
    "  abi <abi/4.0>," \
    "  include <tunables/global>" \
    "  profile filex-appimage @{HOME}/**/filex-desktop-*.AppImage flags=(unconfined) {" \
    "    userns," \
    "    include if exists <local/filex-appimage>" \
    "  }" \
    "  EOF" \
    "  sudo apparmor_parser -r /etc/apparmor.d/filex-appimage" \
    ""
}

name_note() {
  base=$(basename "$APPIMAGE")
  case "$APPIMAGE" in
    /home/*/*|/root/*) inhome=1 ;;
    *) inhome=0 ;;
  esac
  case "$base" in
    filex-desktop-*.AppImage) named=1 ;;
    *) named=0 ;;
  esac
  [ "$inhome" = 1 ] && [ "$named" = 1 ] && return 0
  if [ "$lang" = tr ]; then
    say "Profil, ev klasörünüzdeki filex-desktop-*.AppImage adlı dosyaları kapsar." \
      "Bu dosya: $APPIMAGE" \
      "Önce onu ev klasörünüze taşıyın ve sürümdeki adını geri verin." ""
  else
    say "The profile covers files named filex-desktop-*.AppImage in your home folder." \
      "This one is: $APPIMAGE" \
      "Move it into your home folder and give it back its release name first." ""
  fi
}

if [ "$lang" = tr ]; then
  title="filex güvenli biçimde açılamıyor"
  say "filex, Chromium'un kum havuzu olmadan açılmaz." \
    "Uygulamanın gösterdiği sayfalar bu kum havuzunda çalışır; bu sistem onu kurmaya izin vermiyor." ""
  case "$kind" in
    appimage)
      say "Ubuntu 23.10 ve sonrası, kullanıcı ad alanlarını yalnız bir AppArmor profili olan" \
        "programlara açar. AppImage için bir kez şu profili kurun, sonra filex'i yeniden açın:"
      profile_lines
      name_note
      say "Ya da kendi kum havuzu yardımcısını getiren .deb paketini kurun." ;;
    snap)
      say "Snap'in kum havuzu bağlantısı kurulu değil. Bir kez şunu çalıştırın, sonra filex'i yeniden açın:" \
        "" "  sudo snap connect ${SNAP_INSTANCE_NAME:-${SNAP_NAME:-filex-app}}:browser-sandbox" "" ;;
    *)
      say "Bu sistem kullanıcı ad alanlarına izin vermiyor ve chrome-sandbox yardımcısı" \
        "root sahipli, setuid (4755) değil. .deb ya da .rpm paketini kurun; ikisi de" \
        "yardımcıyı doğru kurar." "" ;;
  esac
  say "Ayrıntı: $docs"
else
  title="filex cannot open safely"
  say "filex does not start without Chromium's sandbox." \
    "The pages the app shows run inside it, and this system does not let it be built." ""
  case "$kind" in
    appimage)
      say "Ubuntu 23.10 and later allow user namespaces only to programs with an AppArmor" \
        "profile. Add this one for the AppImage once, then open filex again:"
      profile_lines
      name_note
      say "Or install the .deb, which brings its own sandbox helper." ;;
    snap)
      say "The snap's sandbox connection is not in place. Run this once, then open filex again:" \
        "" "  sudo snap connect ${SNAP_INSTANCE_NAME:-${SNAP_NAME:-filex-app}}:browser-sandbox" "" ;;
    *)
      say "This system does not allow user namespaces, and the chrome-sandbox helper is not" \
        "owned by root with the setuid bit (4755). Install the .deb or the .rpm: both set" \
        "the helper up." "" ;;
  esac
  say "Details: $docs"
fi

# A window for a double-click, wherever there is a tool to draw one. The
# AppImage's own library path must not reach a system program.
shown=0
if [ -n "$msg" ] && [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]; then
  if command -v zenity >/dev/null 2>&1; then
    LD_LIBRARY_PATH="" zenity --text-info --title="$title" --filename="$msg" --width=720 --height=460 >/dev/null 2>&1
    case $? in 0|1) shown=1 ;; esac
  fi
  if [ "$shown" = 0 ] && command -v kdialog >/dev/null 2>&1; then
    LD_LIBRARY_PATH="" kdialog --title "$title" --textbox "$msg" 720 460 >/dev/null 2>&1 && shown=1
  fi
  if [ "$shown" = 0 ] && command -v xdg-open >/dev/null 2>&1; then
    LD_LIBRARY_PATH="" xdg-open "$docs" >/dev/null 2>&1 && shown=1
  fi
fi
[ -n "$msg" ] && rm -f "$msg"
# 78 (EX_CONFIG): "this system is not set up for it", told apart from a crash.
exit 78
