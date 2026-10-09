#!/bin/bash
# The AppImage's entry point (AppRun): electron-builder 24.13.3's own, as its
# Go app-builder rendered it for this app (no EULA: `appImage.license` is not
# set), kept when the packages moved to electron-builder 26 (0.55, #68).
# desktop/scripts/appimage-apprun.cjs writes it, with the executable's name
# filled in, into the packed app when an AppImage is built; electron-builder
# copies the packed app over its own AppRun.
#
# ⚠⚠ Why not 26's own: when `unshare -Ur true` fails (Ubuntu 23.10 and later
# with no AppArmor profile for the image) it starts the app with --no-sandbox
# in front of the person's arguments. The launcher (launcher.sh) passes a
# --no-sandbox through as the person's decision, so the AppImage would open
# with Chromium's sandbox off instead of refusing with the launcher's message,
# which is what every AppImage since 0.50 did. Checked by the release's
# "refuses --exe" step on Ubuntu 24.04, and by
# desktop/test/appimage-apprun.test.ts here.
# ⚠ The text between the double braces is filled in by appimage-apprun.cjs;
# nothing else in this file is a placeholder.
set -e

if [ ! -z "$DEBUG" ] ; then
  env
  set -x
fi

THIS="$0"
# http://stackoverflow.com/questions/3190818/
args=("$@")
NUMBER_OF_ARGS="$#"

if [ -z "$APPDIR" ] ; then
  # Find the AppDir. It is the directory that contains AppRun.
  # This assumes that this script resides inside the AppDir or a subdirectory.
  # If this script is run inside an AppImage, then the AppImage runtime likely has already set $APPDIR
  path="$(dirname "$(readlink -f "${THIS}")")"
  while [[ "$path" != "" && ! -e "$path/$1" ]]; do
    path=${path%/*}
  done
  APPDIR="$path"
fi

export PATH="${APPDIR}:${APPDIR}/usr/sbin:${PATH}"
export XDG_DATA_DIRS="./share/:/usr/share/gnome:/usr/local/share/:/usr/share/:${XDG_DATA_DIRS}"
export LD_LIBRARY_PATH="${APPDIR}/usr/lib:${LD_LIBRARY_PATH}"
export XDG_DATA_DIRS="${APPDIR}"/usr/share/:"${XDG_DATA_DIRS}":/usr/share/gnome/:/usr/local/share/:/usr/share/
export GSETTINGS_SCHEMA_DIR="${APPDIR}/usr/share/glib-2.0/schemas:${GSETTINGS_SCHEMA_DIR}"

BIN="$APPDIR/{{.ExecutableName}}"

if [ -z "$APPIMAGE_EXIT_AFTER_INSTALL" ] ; then
  trap atexit EXIT
fi

isEulaAccepted=1

atexit()
{
  if [ $isEulaAccepted == 1 ] ; then
    if [ $NUMBER_OF_ARGS -eq 0 ] ; then
      exec "$BIN"
    else
      exec "$BIN" "${args[@]}"
    fi
  fi
}

error()
{
  if [ -x /usr/bin/zenity ] ; then
    LD_LIBRARY_PATH="" zenity --error --text "${1}" 2>/dev/null
  elif [ -x /usr/bin/kdialog ] ; then
    LD_LIBRARY_PATH="" kdialog --msgbox "${1}" 2>/dev/null
  elif [ -x /usr/bin/Xdialog ] ; then
    LD_LIBRARY_PATH="" Xdialog --msgbox "${1}" 2>/dev/null
  else
    echo "${1}"
  fi
  exit 1
}

yesno()
{
  TITLE=$1
  TEXT=$2
  if [ -x /usr/bin/zenity ] ; then
    LD_LIBRARY_PATH="" zenity --question --title="$TITLE" --text="$TEXT" 2>/dev/null || exit 0
  elif [ -x /usr/bin/kdialog ] ; then
    LD_LIBRARY_PATH="" kdialog --title "$TITLE" --yesno "$TEXT" || exit 0
  elif [ -x /usr/bin/Xdialog ] ; then
    LD_LIBRARY_PATH="" Xdialog --title "$TITLE" --clear --yesno "$TEXT" 10 80 || exit 0
  else
    echo "zenity, kdialog, Xdialog missing. Skipping ${THIS}."
    exit 0
  fi
}

check_dep()
{
  DEP=$1
  if [ -z $(which "$DEP") ] ; then
    echo "$DEP is missing. Skipping ${THIS}."
    exit 0
  fi
}

if [ -z "$APPIMAGE" ] ; then
  APPIMAGE="$APPDIR/AppRun"
  # not running from within an AppImage; hence using the AppRun for Exec=
fi
