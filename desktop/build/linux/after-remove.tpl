#!/bin/bash
# After-remove script of the .deb and the .rpm (electron-builder.yml →
# linux.afterRemove). Replaces electron-builder's own, which has two faults:
#
# ⚠ It hands `update-alternatives --remove` the LINK (/usr/bin/<name>) where
#   the command wants the TARGET. dpkg's update-alternatives happens to clean
#   up anyway once the target file is gone; Fedora's `alternatives` does not:
#   measured on Fedora with the filex-app .rpm, `dnf remove` reported
#   "%postun scriptlet failed, exit status 2" and left /usr/bin/filex-app
#   pointing at a deleted binary.
# ⚠ It runs on every call, upgrades included. An rpm upgrade runs the OLD
#   package's %postun after the new one's %post — with the right target it
#   would remove the link the new version has just made.
#
# $1 — rpm: how many versions of the package remain (0 = erased);
#      deb: remove | purge | upgrade | disappear | failed-upgrade | abort-*.
# ⚠ This file is an electron-builder template: a dollar-brace variable is a
#   build-time macro and must be one it defines; shell variables go unbraced.

case "$1" in
  0|remove|purge|disappear) ;;
  *) exit 0 ;;
esac

if type update-alternatives >/dev/null 2>&1; then
  update-alternatives --remove '${executable}' '/opt/${sanitizedProductName}/${executable}' >/dev/null 2>&1 || true
fi
# The after-install falls back to a plain `ln -sf` where there are no
# alternatives: remove the link if it is left pointing at nothing.
if [ -L '/usr/bin/${executable}' ] && [ ! -e '/usr/bin/${executable}' ]; then
  rm -f '/usr/bin/${executable}'
fi

# The kinds of file the apps on a person's filex servers open are registered
# by the RUNNING app, in that person's own data folder (src/os-assoc.ts): a
# hidden desktop entry and, for the extensions the system had no type for, a
# type package of filex's own. A package cannot see anyone's $XDG_DATA_HOME,
# so the default place is cleaned, in every home: exactly those two files,
# by name, and the caches rebuilt as their owner. (The entry carries TryExec,
# so a desktop ignores one left anywhere else once the app is gone.)
if type getent >/dev/null 2>&1; then
  getent passwd | while IFS=: read -r name _ uid _ _ home _; do
    case "$uid" in ''|*[!0-9]*) continue ;; esac
    [ "$uid" -ge 1000 ] || [ "$uid" -eq 0 ] || continue
    case "$home" in /?*) ;; *) continue ;; esac
    share="$home/.local/share"
    [ -d "$share" ] || continue
    entry="$share/applications/filex-app-openwith.desktop"
    types="$share/mime/packages/filex-app-openwith.xml"
    if [ -f "$entry" ]; then
      rm -f "$entry"
      if type runuser >/dev/null 2>&1 && type update-desktop-database >/dev/null 2>&1; then
        runuser -u "$name" -- update-desktop-database "$share/applications" >/dev/null 2>&1 || true
      fi
    fi
    if [ -f "$types" ]; then
      rm -f "$types"
      if type runuser >/dev/null 2>&1 && type update-mime-database >/dev/null 2>&1; then
        runuser -u "$name" -- update-mime-database "$share/mime" >/dev/null 2>&1 || true
      fi
    fi
  done
fi
exit 0
