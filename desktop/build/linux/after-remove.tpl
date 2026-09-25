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
exit 0
