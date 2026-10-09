#!/bin/bash
# After-install script of the .deb and the .rpm (electron-builder.yml →
# deb.afterInstall, rpm.afterInstall): electron-builder 24.13.3's own, word
# for word, kept when the packages moved to electron-builder 26 (0.55, #68).
#
# ⚠⚠ Why not 26's own: it runs `unshare --user true` AS ROOT (which always
#   works, Ubuntu restricts unprivileged user namespaces only) and then takes
#   the setuid bit OFF chrome-sandbox, and installs an AppArmor profile,
#   /etc/apparmor.d/<executable>, for /opt/<product>/<executable>. Here that
#   path is build/linux/launcher.sh, a shell script, not the Electron binary
#   (filex-app-bin): an update from 0.54 would have swapped the sandbox every
#   .deb opened with since 0.50 (the setuid helper, measured on Ubuntu 24.04
#   by the release's "--expect-sandbox" check) for a profile on the wrong
#   file, and launcher.sh refuses to start when neither works. after-remove.tpl
#   never removed that profile either. Replacing the helper is its own change,
#   to be measured, not one to make by upgrading the builder.
# ⚠ This file is an electron-builder template: a dollar-brace variable is a
#   build-time macro and must be one it defines; shell variables go unbraced.
#   desktop/test/linux-desktop-entry.test.ts holds the setuid line and the
#   absence of an AppArmor step.

if type update-alternatives 2>/dev/null >&1; then
    # Remove previous link if it doesn't use update-alternatives
    if [ -L '/usr/bin/${executable}' -a -e '/usr/bin/${executable}' -a "`readlink '/usr/bin/${executable}'`" != '/etc/alternatives/${executable}' ]; then
        rm -f '/usr/bin/${executable}'
    fi
    update-alternatives --install '/usr/bin/${executable}' '${executable}' '/opt/${sanitizedProductName}/${executable}' 100 || ln -sf '/opt/${sanitizedProductName}/${executable}' '/usr/bin/${executable}'
else
    ln -sf '/opt/${sanitizedProductName}/${executable}' '/usr/bin/${executable}'
fi

# SUID chrome-sandbox for Electron 5+
chmod 4755 '/opt/${sanitizedProductName}/chrome-sandbox' || true

if hash update-mime-database 2>/dev/null; then
    update-mime-database /usr/share/mime || true
fi

if hash update-desktop-database 2>/dev/null; then
    update-desktop-database /usr/share/applications || true
fi
