// What the "Install the desktop app" prompt offers on each platform.
//
// ⚠ Windows leads with the Microsoft Store since the listing went live
// (2026-09-26): the one Windows build Microsoft signs, so no SmartScreen
// prompt, installed and updated by the Store. The installer and the portable
// .exe stay, for machines without the Store.
// ⚠ Linux leads the same way with the Snap Store (live 2026-09-26, stable),
// and offers the .rpm the release has always attached; macOS offers the
// Homebrew tap after the .dmg — `brew upgrade` is what keeps a Mac copy
// current. winget and the AUR are NOT offered: neither installs today (the
// winget PRs wait for review, the AUR package is unpublished), and a row that
// leads nowhere is worse than no row.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { HOMEBREW_TAP_URL, MSSTORE_URL, SNAP_STORE_URL, desktopDownloadsFor } from '@/composables/useInstallPrompt';

const t = (k: string) => k;

describe('the desktop downloads the install prompt offers', () => {
  it('leads with the Microsoft Store on Windows, then the installer and the portable build', () => {
    const d = desktopDownloadsFor('windows', t);
    expect(d.map((x) => x.label)).toEqual(['install.dl.win_store', 'install.dl.win_setup', 'install.dl.win_portable']);
    expect(d[0].href).toBe(MSSTORE_URL);
    expect(d[1].href).toMatch(/\/filex-desktop-x64\.exe$/);
  });

  it('names the same Store product as the desktop app does', () => {
    const channel = readFileSync(path.resolve(__dirname, '../../../desktop/src/channel.ts'), 'utf8');
    const id = /msstore:\s*'([0-9A-Z]{12})'/.exec(channel)?.[1];
    expect(id).toBeTruthy();
    expect(MSSTORE_URL).toBe(`https://apps.microsoft.com/detail/${id}`);
  });

  it('offers no Store link on Linux or macOS', () => {
    for (const p of ['linux', 'mac'] as const) {
      expect(desktopDownloadsFor(p, t).some((x) => x.href === MSSTORE_URL), p).toBe(false);
    }
  });

  it('has the Store words in English and Turkish', () => {
    for (const loc of ['en', 'tr']) {
      const m = JSON.parse(readFileSync(path.resolve(__dirname, `../../src/locales/${loc}.json`), 'utf8'));
      expect(m.install.dl.win_store, loc).toBeTruthy();
      expect(m.install.dl.win_store_hint, loc).toBeTruthy();
    }
  });

  it('leads with the Snap Store on Linux, then the AppImage, the .deb and the .rpm', () => {
    const d = desktopDownloadsFor('linux', t);
    expect(d.map((x) => x.label)).toEqual([
      'install.dl.linux_snap',
      'install.dl.appimage',
      'install.dl.deb',
      'install.dl.rpm',
    ]);
    expect(d[0].href).toBe(SNAP_STORE_URL);
    expect(d[3].href).toMatch(/\/filex-desktop-x86_64\.rpm$/);
  });

  it('names the same snap as the desktop app does', () => {
    const channel = readFileSync(path.resolve(__dirname, '../../../desktop/src/channel.ts'), 'utf8');
    const name = /LINUX_APP_NAME\s*=\s*'([a-z0-9-]+)'/.exec(channel)?.[1];
    expect(name).toBeTruthy();
    expect(SNAP_STORE_URL).toBe(`https://snapcraft.io/${name}`);
  });

  it('offers the Homebrew tap on macOS after the .dmg, and store rows nowhere else', () => {
    const mac = desktopDownloadsFor('mac', t);
    expect(mac.map((x) => x.label)).toEqual(['install.dl.dmg', 'install.dl.mac_brew']);
    expect(mac[1].href).toBe(HOMEBREW_TAP_URL);
    expect(desktopDownloadsFor('windows', t).some((x) => x.href === SNAP_STORE_URL || x.href === HOMEBREW_TAP_URL)).toBe(false);
    expect(desktopDownloadsFor('linux', t).some((x) => x.href === HOMEBREW_TAP_URL)).toBe(false);
  });

  it('offers no channel that does not install today', () => {
    for (const p of ['windows', 'linux', 'mac'] as const) {
      for (const x of desktopDownloadsFor(p, t)) {
        expect(x.href, `${p}: ${x.label}`).not.toMatch(/winget|aur\.archlinux\.org/);
      }
    }
  });

  it('has the Snap, .rpm and Homebrew words in English and Turkish', () => {
    for (const loc of ['en', 'tr']) {
      const m = JSON.parse(readFileSync(path.resolve(__dirname, `../../src/locales/${loc}.json`), 'utf8'));
      for (const k of ['linux_snap', 'linux_snap_hint', 'rpm', 'rpm_hint', 'mac_brew', 'mac_brew_hint']) {
        expect(m.install.dl[k], `${loc}: install.dl.${k}`).toBeTruthy();
      }
    }
  });
});
