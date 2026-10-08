// What the "Install the desktop app" prompt offers on each platform.
//
// ⚠ Windows leads with the Microsoft Store since the listing went live
// (2026-09-26): the one Windows build Microsoft signs, so no SmartScreen
// prompt, installed and updated by the Store. The installer and the portable
// .exe stay, for machines without the Store.
// ⚠ Linux leads the same way with the Snap Store (live 2026-09-26, stable),
// and offers the .rpm the release has always attached; macOS offers the
// Homebrew tap after the .dmg - `brew upgrade` is what keeps a Mac copy
// current. The AUR is NOT offered: its package is unpublished, and a row that
// leads nowhere is worse than no row. Nor is winget: the prompt offers the
// desktop app, and the desktop app's winget package (BRFTech.filex-app) still
// waits for its first review; only the CLI's (BRFTech.filex) is on winget,
// since 0.53.0. WINGET below says which one is live and holds the pages to it.
// ⚠ 0.50: the processor. Windows and Linux files come as x64 and arm64; the
// browser's answer (client hints, or Firefox's "aarch64") picks which one a
// row offers, and the other one sits beside it. Every link to a release file,
// here, in DESKTOP.md, CLI.md and on filex.sh, is held to one table
// (DESKTOP_ASSETS) and that table to the files a release must carry.
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  DESKTOP_ASSETS,
  HOMEBREW_TAP_URL,
  MSSTORE_URL,
  SNAP_STORE_URL,
  archFromHints,
  archFromUserAgent,
  desktopDownloadsFor,
  detectDesktopArch,
  detectDesktopPlatform,
  type DesktopArch,
} from '@/composables/useInstallPrompt';
import { releaseAssets } from '../../../scripts/release/plan.mjs';

const REPO = path.resolve(__dirname, '../../..');
const t = (k: string) => k;
/** The catalogue key of a row: its label without the " · x64" the row adds. */
const key = (label: string) => label.split(' · ')[0];
const RELEASE = new Set<string>(releaseAssets('0.0.0'));
const DOWNLOAD = 'https://github.com/BRF-Tech/filex/releases/latest/download/';
const ALL_ASSETS = Object.values(DESKTOP_ASSETS).flatMap((byArch) => Object.values(byArch) as string[]);

/**
 * Which winget package installs today - measured, not assumed. 2026-10-08:
 * microsoft/winget-pkgs merged "New version: BRFTech.filex 0.53.0" (#448254,
 * manifests/b/BRFTech/filex/0.53.0), while "New version: BRFTech.filex-app
 * 0.53.0" (#448327) still waits for a moderator's first review. When the
 * desktop package merges, set it to true: the tests below then name every
 * line that still calls it "in review" (README, docs/DESKTOP.md, filex.sh).
 */
const WINGET: Record<'BRFTech.filex' | 'BRFTech.filex-app', boolean> = {
  'BRFTech.filex': true,
  'BRFTech.filex-app': false,
};

describe('the desktop downloads the install prompt offers', () => {
  it('leads with the Microsoft Store on Windows, then the installer and the portable build', () => {
    const d = desktopDownloadsFor('windows', t);
    expect(d.map((x) => key(x.label))).toEqual(['install.dl.win_store', 'install.dl.win_setup', 'install.dl.win_portable']);
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
    expect(d.map((x) => key(x.label))).toEqual([
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
    // The prompt offers the desktop app, so winget waits for BRFTech.filex-app;
    // the CLI's package being live does not put a winget row here.
    const nowhere = WINGET['BRFTech.filex-app'] ? /aur\.archlinux\.org/ : /winget|aur\.archlinux\.org/;
    for (const p of ['windows', 'linux', 'mac'] as const) {
      for (const a of [null, 'x64', 'arm64'] as const) {
        for (const x of desktopDownloadsFor(p, t, a)) {
          expect(x.href, `${p}/${a}: ${x.label}`).not.toMatch(nowhere);
          if (x.other) expect(x.other.href, `${p}/${a}: ${x.label} (other)`).not.toMatch(nowhere);
        }
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

describe('the processor a row offers', () => {
  const files = (platform: 'windows' | 'linux', arch: DesktopArch | null) =>
    desktopDownloadsFor(platform, t, arch)
      .filter((x) => x.arch)
      .map((x) => [x.href.slice(DOWNLOAD.length), x.other?.href.slice(DOWNLOAD.length)]);

  it('an Arm Windows machine is offered the arm64 installer and portable build, x64 beside them', () => {
    expect(files('windows', 'arm64')).toEqual([
      ['filex-desktop-arm64.exe', 'filex-desktop-x64.exe'],
      ['filex-desktop-portable-arm64.exe', 'filex-desktop-portable-x64.exe'],
    ]);
  });

  it('an Arm Linux machine is offered the arm64 AppImage, .deb and .rpm, x64 beside them', () => {
    expect(files('linux', 'arm64')).toEqual([
      ['filex-desktop-arm64.AppImage', 'filex-desktop-x86_64.AppImage'],
      ['filex-desktop-arm64.deb', 'filex-desktop-amd64.deb'],
      ['filex-desktop-aarch64.rpm', 'filex-desktop-x86_64.rpm'],
    ]);
  });

  it('an x64 machine, and one the browser could not tell, are offered x64 with arm64 beside it', () => {
    for (const a of ['x64', null] as const) {
      expect(files('windows', a)).toEqual([
        ['filex-desktop-x64.exe', 'filex-desktop-arm64.exe'],
        ['filex-desktop-portable-x64.exe', 'filex-desktop-portable-arm64.exe'],
      ]);
      expect(files('linux', a)).toEqual([
        ['filex-desktop-x86_64.AppImage', 'filex-desktop-arm64.AppImage'],
        ['filex-desktop-amd64.deb', 'filex-desktop-arm64.deb'],
        ['filex-desktop-x86_64.rpm', 'filex-desktop-aarch64.rpm'],
      ]);
    }
  });

  it('says on the row which processor it is for, and on the link beside it the other one', () => {
    const row = desktopDownloadsFor('windows', t, 'arm64')[1];
    expect(row.label).toBe('install.dl.win_setup · arm64');
    expect(row.other).toMatchObject({ arch: 'x64', label: 'install.dl.other_x64' });
    const unknown = desktopDownloadsFor('linux', t, null)[1];
    expect(unknown.label).toBe('install.dl.appimage · x64');
    expect(unknown.other).toMatchObject({ arch: 'arm64', label: 'install.dl.other_arm64' });
  });

  it('leaves the store rows alone: the Store and snapd pick the build themselves', () => {
    for (const a of ['x64', 'arm64', null] as const) {
      for (const p of ['windows', 'linux'] as const) {
        const store = desktopDownloadsFor(p, t, a)[0];
        expect(store.href).toBe(p === 'windows' ? MSSTORE_URL : SNAP_STORE_URL);
        expect(store.arch).toBeUndefined();
        expect(store.other).toBeUndefined();
      }
    }
  });

  it('keeps macOS to its one Apple Silicon build whatever the browser says', () => {
    for (const a of ['x64', 'arm64', null] as const) {
      const mac = desktopDownloadsFor('mac', t, a);
      expect(mac[0].href).toBe(`${DOWNLOAD}filex-desktop-arm64.dmg`);
      expect(mac.some((x) => x.other)).toBe(false);
    }
  });

  it('has the words for the link beside a row in English and Turkish', () => {
    for (const loc of ['en', 'tr']) {
      const m = JSON.parse(readFileSync(path.resolve(__dirname, `../../src/locales/${loc}.json`), 'utf8'));
      for (const k of ['other_x64', 'other_arm64']) {
        expect(m.install.dl[k], `${loc}: install.dl.${k}`).toBeTruthy();
      }
      expect(m.install.dl.other_x64, loc).toContain('x64');
      expect(m.install.dl.other_arm64, loc).toContain('arm64');
    }
  });
});

/* ── what the browser says ───────────────────────────────────────────────── */

const UA = {
  winChrome: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36',
  winFirefox: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0',
  linuxChrome: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36',
  linuxFirefoxArm: 'Mozilla/5.0 (X11; Linux aarch64; rv:131.0) Gecko/20100101 Firefox/131.0',
  linuxFirefoxX64: 'Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0',
  macSafari: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
  macChrome: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36',
  iPhone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
  android: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36',
};

const hints = (architecture: string, bitness: string) => ({
  getHighEntropyValues: vi.fn(async (asked: string[]) => {
    expect(asked).toEqual(expect.arrayContaining(['architecture', 'bitness']));
    return { architecture, bitness };
  }),
});

/** Every case both detectors are held to: what the browser exposes, and the
 *  answer. `touch` is an iPad asking for the desktop site. */
interface Case {
  name: string;
  nav: { userAgent: string; platform?: string; userAgentData?: unknown };
  touch?: boolean;
  platform: 'windows' | 'linux' | 'mac' | null;
  arch: DesktopArch | null;
}
const CASES: Case[] = [
  { name: 'Windows on Arm, Chrome (client hints say arm/64)', nav: { userAgent: UA.winChrome, platform: 'Win32', userAgentData: hints('arm', '64') }, platform: 'windows', arch: 'arm64' },
  { name: 'Windows x64, Edge (client hints say x86/64)', nav: { userAgent: UA.winChrome, platform: 'Win32', userAgentData: hints('x86', '64') }, platform: 'windows', arch: 'x64' },
  { name: 'Windows 32-bit (x86/32): no build, unknown', nav: { userAgent: UA.winChrome, platform: 'Win32', userAgentData: hints('x86', '32') }, platform: 'windows', arch: null },
  { name: 'Windows, Firefox (no client hints)', nav: { userAgent: UA.winFirefox, platform: 'Win32' }, platform: 'windows', arch: null },
  { name: 'Linux arm64, Chromium (UA frozen to x86_64, hints say arm)', nav: { userAgent: UA.linuxChrome, platform: 'Linux x86_64', userAgentData: hints('arm', '64') }, platform: 'linux', arch: 'arm64' },
  { name: 'Linux arm64, Firefox (says aarch64)', nav: { userAgent: UA.linuxFirefoxArm, platform: 'Linux aarch64' }, platform: 'linux', arch: 'arm64' },
  { name: 'Linux x64, Firefox (an x86_64 string is not trusted)', nav: { userAgent: UA.linuxFirefoxX64, platform: 'Linux x86_64' }, platform: 'linux', arch: null },
  { name: 'Mac, Safari (always "Intel Mac OS X")', nav: { userAgent: UA.macSafari, platform: 'MacIntel' }, platform: 'mac', arch: null },
  { name: 'Apple Silicon Mac, Chrome', nav: { userAgent: UA.macChrome, platform: 'MacIntel', userAgentData: hints('arm', '64') }, platform: 'mac', arch: 'arm64' },
  { name: 'iPad asking for the desktop site', nav: { userAgent: UA.macSafari, platform: 'MacIntel' }, touch: true, platform: null, arch: null },
  { name: 'iPhone', nav: { userAgent: UA.iPhone, platform: 'iPhone' }, platform: null, arch: null },
  { name: 'Android', nav: { userAgent: UA.android, platform: 'Linux armv8l', userAgentData: hints('', '') }, platform: null, arch: null },
];

describe('the processor, from what the browser says', () => {
  it('reads client hints as a build name, and only a 64-bit one', () => {
    expect(archFromHints('arm', '64')).toBe('arm64');
    expect(archFromHints('x86', '64')).toBe('x64');
    expect(archFromHints('x86', '32')).toBeNull();
    expect(archFromHints('arm', '32')).toBeNull();
    expect(archFromHints('', '')).toBeNull();
    expect(archFromHints(undefined, undefined)).toBeNull();
  });

  it('trusts only an Arm signal in the user-agent string, never a frozen "x64"', () => {
    expect(archFromUserAgent(UA.linuxFirefoxArm)).toBe('arm64');
    expect(archFromUserAgent('', 'Linux aarch64')).toBe('arm64');
    expect(archFromUserAgent(UA.winChrome, 'Win32')).toBeNull();
    expect(archFromUserAgent(UA.linuxChrome, 'Linux x86_64')).toBeNull();
    expect(archFromUserAgent(UA.macSafari, 'MacIntel')).toBeNull();
  });

  it.each(CASES.map((c) => [c.name, c] as const))('%s', async (_name, c) => {
    expect(await detectDesktopArch(c.nav as never)).toBe(c.arch);
  });

  it('asks the hints for the architecture and the bitness, and nothing that identifies the person', async () => {
    const data = hints('arm', '64');
    await detectDesktopArch({ userAgent: UA.winChrome, userAgentData: data } as never);
    expect(data.getHighEntropyValues).toHaveBeenCalledWith(['architecture', 'bitness']);
  });

  it('a hints call that fails is "unknown", not an error', async () => {
    const failing = { getHighEntropyValues: () => Promise.reject(new Error('NotAllowedError')) };
    expect(await detectDesktopArch({ userAgent: UA.winChrome, userAgentData: failing } as never)).toBeNull();
    expect(await detectDesktopArch(undefined)).toBeNull();
  });
});

describe('the platform, from what the browser says', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete (document as unknown as { ontouchend?: unknown }).ontouchend;
  });

  it.each(CASES.map((c) => [c.name, c] as const))('%s', (_name, c) => {
    vi.stubGlobal('navigator', c.nav);
    if (c.touch) (document as unknown as { ontouchend: unknown }).ontouchend = null;
    expect(detectDesktopPlatform()).toBe(c.platform);
  });
});

/* ── every link to a release file is to a file the release carries ───────── */

/** Every `releases/latest/download/<name>` a page links. */
function linkedFiles(text: string): string[] {
  return [...text.matchAll(/github\.com\/BRF-Tech\/filex\/releases\/latest\/download\/([A-Za-z0-9._-]+)/g)].map((m) => m[1]);
}

describe('download links lead to files a release carries', () => {
  it('the table names only files the release check requires', () => {
    for (const f of ALL_ASSETS) expect(RELEASE.has(f), `${f} is not in scripts/release/plan.mjs releaseAssets`).toBe(true);
  });

  it('the prompt links only files from the table, for every platform and processor', () => {
    for (const p of ['windows', 'linux', 'mac'] as const) {
      for (const a of [null, 'x64', 'arm64'] as const) {
        for (const x of desktopDownloadsFor(p, t, a)) {
          for (const href of [x.href, x.other?.href].filter(Boolean) as string[]) {
            if (!href.startsWith(DOWNLOAD)) continue;
            expect(ALL_ASSETS, `${p}/${a}: ${href}`).toContain(href.slice(DOWNLOAD.length));
          }
        }
      }
    }
  });

  it('every file in the table is offered somewhere in the prompt', () => {
    const offered = new Set<string>();
    for (const p of ['windows', 'linux', 'mac'] as const) {
      for (const a of [null, 'arm64'] as const) {
        for (const x of desktopDownloadsFor(p, t, a)) {
          offered.add(x.href.slice(DOWNLOAD.length));
          if (x.other) offered.add(x.other.href.slice(DOWNLOAD.length));
        }
      }
    }
    expect(ALL_ASSETS.filter((f) => !offered.has(f))).toEqual([]);
  });

  it('docs/DESKTOP.md links every desktop file of the table, and no other', () => {
    const linked = linkedFiles(readFileSync(path.join(REPO, 'docs', 'DESKTOP.md'), 'utf8'));
    const desktop = linked.filter((f) => f.startsWith('filex-desktop-'));
    expect(desktop.filter((f) => !ALL_ASSETS.includes(f))).toEqual([]);
    expect(ALL_ASSETS.filter((f) => !desktop.includes(f))).toEqual([]);
  });

  it('docs/CLI.md links only files the release check requires, x64 and arm64 for every platform', () => {
    const linked = linkedFiles(readFileSync(path.join(REPO, 'docs', 'CLI.md'), 'utf8'));
    expect(linked.filter((f) => !RELEASE.has(f))).toEqual([]);
    for (const f of ['filex-linux-amd64', 'filex-linux-arm64', 'filex-darwin-amd64', 'filex-darwin-arm64', 'filex-windows-amd64.exe', 'filex-windows-arm64.exe']) {
      expect(linked, `docs/CLI.md does not link ${f}`).toContain(f);
    }
  });
});

/* ── winget: each page says where each package stands ────────────────────── */

type WingetId = keyof typeof WINGET;

/** `winget install <id>` and nothing longer: BRFTech.filex is a prefix of BRFTech.filex-app. */
const wingetInstall = (id: WingetId) => new RegExp(`winget install ${id.split('.').join('[.]')}(?![A-Za-z0-9_.-])`);

/** Words a line uses for a package winget does not find yet. */
const NOT_YET = /not installable yet|in review|waiting for|does not find it|will be/i;

/** The sentences of a page that give `winget install <id>`: blocks split at blank lines, a
 *  table row on its own, and each of those at the end of a sentence (". ", "; ", ": "). */
function wingetSentences(text: string, id: WingetId): string[] {
  const re = wingetInstall(id);
  return text
    .replace(/\r/g, '')
    .split(/\n[ \t]*\n/)
    .flatMap((block) => (block.trimStart().startsWith('|') ? block.split('\n') : [block]))
    .flatMap((block) => block.split(/(?<=[.;:])\s+/))
    .filter((s) => re.test(s));
}

describe('winget: every page offers a live package as working and a waiting one as "in review"', () => {
  const PAGES: Array<[string, WingetId[]]> = [
    ['README.md', ['BRFTech.filex', 'BRFTech.filex-app']],
    ['docs/CLI.md', ['BRFTech.filex']],
    ['docs/DESKTOP.md', ['BRFTech.filex-app']],
  ];

  it.each(PAGES.flatMap(([page, ids]) => ids.map((id) => [page, id] as const)))('%s: %s', (page, id) => {
    const said = wingetSentences(readFileSync(path.join(REPO, page), 'utf8'), id);
    expect(said, `${page} never gives "winget install ${id}"`).not.toEqual([]);
    for (const s of said) {
      if (WINGET[id]) expect(s, `${page} still calls ${id} not installable`).not.toMatch(NOT_YET);
      else expect(s, `${page} offers ${id} as if winget found it`).toMatch(NOT_YET);
    }
  });
});

/* ── filex.sh ────────────────────────────────────────────────────────────── */

// `site/` is withheld from the public export: there the page is not a surface.
const SITE = path.join(REPO, 'site', 'index.html');
const sitePresent = existsSync(SITE);

describe.skipIf(!sitePresent)('filex.sh lists the same files and marks them by the same rules', () => {
  // ⚠ Guarded read: describe.skipIf still runs this callback.
  const html = sitePresent ? readFileSync(SITE, 'utf8') : '';

  /** The download chips: href and the processor each says it is for. */
  const chips = [...html.matchAll(/<a class="dl-arch" data-arch="(x64|arm64)" href="([^"]+)"/g)].map((m) => ({
    arch: m[1] as DesktopArch,
    file: m[2].startsWith(DOWNLOAD) ? m[2].slice(DOWNLOAD.length) : m[2],
  }));

  it('links every desktop file of the table, each under the processor the table files it under', () => {
    expect(chips.map((c) => c.file).sort()).toEqual([...ALL_ASSETS].sort());
    for (const c of chips) {
      const entry = Object.values(DESKTOP_ASSETS).find((byArch) => (Object.values(byArch) as string[]).includes(c.file));
      expect((entry as Record<string, string>)[c.arch], `${c.file} is marked ${c.arch}`).toBe(c.file);
    }
  });

  it('links no release file the release check does not require', () => {
    expect(linkedFiles(html).filter((f) => !RELEASE.has(f))).toEqual([]);
  });

  it('gives a live winget package as a command and a waiting one only in the "in review" row', () => {
    const from = html.indexOf('<div class="install-row install-soon">');
    const to = html.indexOf('<div class="dl" id="downloads">');
    expect(from, 'site/index.html has no "install-soon" row').toBeGreaterThan(-1);
    expect(to, 'site/index.html has no downloads block').toBeGreaterThan(from);
    const soon = html.slice(from, to);
    const rest = html.slice(0, from) + html.slice(to);
    expect(soon).toMatch(/in review/);
    for (const id of Object.keys(WINGET) as WingetId[]) {
      const re = wingetInstall(id);
      if (WINGET[id]) {
        expect(rest, `filex.sh does not give "winget install ${id}"`).toMatch(re);
        expect(soon, `filex.sh still lists ${id} as in review`).not.toMatch(re);
      } else {
        expect(soon, `filex.sh does not list ${id} as in review`).toMatch(re);
        expect(rest, `filex.sh offers ${id} as if winget found it`).not.toMatch(re);
      }
    }
  });

  /** The page's detection script, run on its own with a stub window. */
  function siteDetect(): {
    platformOf(nav: unknown, touch: boolean): string | null;
    archOf(nav: unknown): Promise<string | null>;
  } {
    const src = /<script id="dl-detect">([\s\S]*?)<\/script>/.exec(html)?.[1];
    expect(src, 'site/index.html has no <script id="dl-detect">').toBeTruthy();
    const win: Record<string, unknown> = {};
    vm.runInNewContext(src!, { window: win, Promise });
    return win.filexDownloads as never;
  }

  it.each(CASES.map((c) => [c.name, c] as const))('%s', async (_name, c) => {
    const site = siteDetect();
    expect(site.platformOf(c.nav, !!c.touch)).toBe(c.platform);
    expect(await site.archOf(c.nav)).toBe(await detectDesktopArch(c.nav as never));
  });

  it('a hints call that fails is "unknown" on the page too', async () => {
    const failing = { getHighEntropyValues: () => Promise.reject(new Error('NotAllowedError')) };
    expect(await siteDetect().archOf({ userAgent: UA.winChrome, userAgentData: failing })).toBeNull();
  });
});
