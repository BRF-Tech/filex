// What the "Install the desktop app" prompt offers on each platform.
//
// ⚠ Windows leads with the Microsoft Store since the listing went live
// (2026-09-26): the one Windows build Microsoft signs, so no SmartScreen
// prompt, installed and updated by the Store. The installer and the portable
// .exe stay, for machines without the Store.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { MSSTORE_URL, desktopDownloadsFor } from '@/composables/useInstallPrompt';

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
});
