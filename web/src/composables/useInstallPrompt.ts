// PWA install + update orchestration for the standalone filex web app.
//
// Adapted from fishapp-mobile's src/composables/useInstallPrompt.ts — the
// browser-quirk handling (beforeinstallprompt capture, standalone detection,
// the Chrome/Edge-vs-iOS-Safari split) is the same well-trodden shape. Extended
// here with the service-worker update flow so a new deploy prompts the user to
// reload instead of silently serving a stale bundle from cache.
//
// ⚠ This lives in web/ (the standalone SPA) ONLY, never in packages/core — the
// embeddable explorer must not surface an install prompt inside its host apps
// (work.example.com "Dosyalar", fishapp). See vite.config.ts for the matching SW
// scope guard. (The settings dialog is core's, and it draws an install row
// only when this app's host hands it one - `useAppInstall` below.)
import { computed, readonly, ref, shallowRef, type ComputedRef, type Ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { viewPrefsSlot } from '@brftech/filex-core';
import { registerAppServiceWorker, type AppServiceWorker } from '@/lib/serviceWorker';

// The Chromium-only event fired when the app meets installability criteria.
interface BeforeInstallPromptEvent extends Event {
  readonly platforms: string[];
  prompt(): Promise<void>;
  readonly userChoice: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>;
}

/**
 * DISMISSAL — "I have seen this", remembered AGAINST THE PERSON.
 *
 * ⚠⚠ It used to be `localStorage` and only `localStorage`, and the reason was
 * a bug one layer down rather than a decision. The per-user document at
 * `GET/PUT /api/files/manager/view-prefs` is opaque to the server by design
 * (`handlers/viewprefs.go` validates and bounds it, and does not look inside),
 * but its client owner — `packages/core/src/lib/viewPrefs.ts` — rebuilt the
 * whole thing from three keys on every save, so anything anybody else wrote
 * was erased by the next column drag. Measured, one resize:
 *
 *     loaded: {"on":true,"f":{},"c":{},"install":{"dismissed":1}}
 *     saved:  {"on":true,"f":{},"c":{"w":{"size":130},"hidden":[]}}
 *
 * That module now carries unknown keys through untouched and hands out a
 * namespaced slot (`viewPrefsSlot`), so the flag can live where it belongs:
 * with the ACCOUNT. Closing the card on one machine closes it on the others,
 * in a private window, and after site data is cleared.
 *
 * ⚠ `localStorage` DOES NOT GO AWAY, for two cases that are not the same:
 *
 *   1. NOBODY SIGNED IN. A public share link and an app-token embed have no
 *      account to remember anything for — the document's `load` answers null
 *      and the slot writes nothing, for ever. The browser flag is the only
 *      place a dismissal can go, so that is where it goes.
 *   2. A DISMISSAL THAT ALREADY EXISTS. Everybody who closed this card before
 *      today has the browser flag and nothing in their document. It is read as
 *      a dismissal and never re-asked.
 *
 * ⚠ The legacy flag is read but never PROMOTED into the document, deliberately.
 * Copying it up would take the one genuinely bad property of browser storage —
 * a shared machine handing the next account the previous one's dismissal — and
 * make it permanent and portable for that second person. Left alone it stays
 * what it is: an old flag in one browser, which every fresh dismissal now
 * outlives.
 *
 * ⚠⚠ TWO OFFERS, TWO FLAGS (task #190). A PC is offered the DESKTOP APP, a
 * phone or a tablet the web app ITSELF, and they used to share the one
 * `dismissed` flag - so closing the desktop chip on a PC, which follows the
 * account to every device, also closed the phone's offer before it had ever
 * been shown. `dismissed` keeps its name and its meaning (the desktop offer,
 * and every dismissal already stored); the phone's offer is `app`.
 *
 * ⚠ The browser flag stays ONE flag for both: it lives in one browser, and a
 * browser is one kind of device, so it was set on the only offer that browser
 * ever showed.
 */
const DISMISS_KEY = 'filex.installPrompt.dismissed';

/** The document's corner for this feature. One top-level key, `install`:
 *  `dismissed` for the desktop app's offer, `app` for the web app's own. */
const installSlot = viewPrefsSlot<{ dismissed?: boolean; app?: boolean }>('install');

function legacyDismissed(): boolean {
  try {
    return typeof localStorage !== 'undefined' && localStorage.getItem(DISMISS_KEY) === '1';
  } catch {
    /* private mode / blocked site data — no flag is a perfectly good answer. */
    return false;
  }
}

/** The display modes an INSTALLED app runs in. A browser tab is `browser`. */
const INSTALLED_DISPLAY_MODES = ['standalone', 'fullscreen', 'minimal-ui', 'window-controls-overlay'];

/** True when the app is already running as an installed PWA (any platform). */
export function detectStandalone(): boolean {
  if (typeof window === 'undefined') return false;
  const displayInstalled =
    typeof matchMedia === 'function' &&
    INSTALLED_DISPLAY_MODES.some((m) => matchMedia(`(display-mode: ${m})`).matches);
  // iOS Safari doesn't support display-mode; it exposes navigator.standalone.
  const iosStandalone =
    typeof navigator !== 'undefined' && (navigator as unknown as { standalone?: boolean }).standalone === true;
  return displayInstalled || iosStandalone;
}

/** iOS (iPhone/iPad) — where there is no beforeinstallprompt and the user must
 *  add to home screen manually via the Share sheet. iPadOS 13+ masquerades as
 *  Mac, so also treat a touch-capable "Mac" as iOS. */
function detectIOS(): boolean {
  if (typeof navigator === 'undefined') return false;
  const ua = navigator.userAgent;
  const iOSDevice = /iPad|iPhone|iPod/.test(ua);
  const iPadOS = ua.includes('Macintosh') && 'ontouchend' in document;
  return iOSDevice || iPadOS;
}

/** Which desktop build to offer, or null on mobile / inside the desktop app.
 *
 * On a PC the useful thing to install is the DESKTOP APP, not a browser PWA:
 * it is the only build that syncs folders to disk and keeps running in the
 * tray. So a PC visitor is pointed at the real installer for their OS. */
export type DesktopPlatform = 'windows' | 'linux' | 'mac';

export function detectDesktopPlatform(): DesktopPlatform | null {
  if (typeof navigator === 'undefined') return null;
  // Inside the Electron shell there is nothing to install.
  if ((window as unknown as { filexDesktop?: unknown }).filexDesktop) return null;
  const ua = navigator.userAgent;
  // Phones and tablets get the PWA path instead — including iPadOS, which
  // reports a Macintosh UA.
  if (/Android|iPhone|iPad|iPod/i.test(ua)) return null;
  if (ua.includes('Macintosh') && 'ontouchend' in document) return null;
  if (/Windows NT/i.test(ua)) return 'windows';
  if (/Mac OS X/i.test(ua)) return 'mac';
  if (/Linux|X11/i.test(ua)) return 'linux';
  return null;
}

/* ── which processor ───────────────────────────────────────────────────────
 *
 * Windows and Linux builds come in two architectures since 0.48.1, and the
 * wrong one is not a slower download, it is a worse app: an x64 build on an
 * Arm Windows laptop runs under emulation, and on Linux arm64 it does not run
 * at all.
 *
 * ⚠⚠ The user-agent string CANNOT say so. Chromium froze it: Windows on Arm
 * reports "Windows NT 10.0; Win64; x64" and Linux arm64 "X11; Linux x86_64",
 * so an "x64" in it means nothing. Chromium answers the real question through
 * User-Agent Client Hints (`navigator.userAgentData.getHighEntropyValues`,
 * `architecture` + `bitness`), which is asynchronous. Firefox and Safari have
 * no client hints; there the string's only trustworthy signal is a POSITIVE
 * Arm one (Firefox on Linux arm64 says "Linux aarch64"), and everything else
 * is "unknown", never a guessed x64.
 *
 * Unknown is a real answer and the rows say so: they offer x64 first, each
 * with the arm64 build of the same file beside it (`other`). A detected
 * machine gets its own build first, with the other one beside it all the same,
 * because a browser that reports the wrong architecture (an x64 browser under
 * emulation) must not leave its user with no way to the right file. */
export type DesktopArch = 'x64' | 'arm64';

/** The answer client hints give, as a build name. Only a 64-bit machine has a
 *  build; anything else (32-bit, an architecture the hints do not name) is
 *  unknown. */
export function archFromHints(architecture?: string | null, bitness?: string | null): DesktopArch | null {
  if (bitness !== '64') return null;
  if (architecture === 'arm') return 'arm64';
  if (architecture === 'x86') return 'x64';
  return null;
}

/** The one thing a browser WITHOUT client hints can tell: that it runs on
 *  Arm, from the user-agent string or `navigator.platform` ("Linux aarch64"). */
export function archFromUserAgent(ua: string, platform = ''): DesktopArch | null {
  return /\b(aarch64|arm64|armv8)/i.test(`${ua} ${platform}`) ? 'arm64' : null;
}

interface ClientHints {
  getHighEntropyValues?(hints: string[]): Promise<{ architecture?: string; bitness?: string }>;
}

/** This machine's architecture, as well as the browser can tell; null when it
 *  cannot. Never throws: a refused or failing hints call is "unknown". */
export async function detectDesktopArch(
  nav: { userAgent?: string; platform?: string; userAgentData?: ClientHints } | undefined =
    typeof navigator === 'undefined' ? undefined : (navigator as never),
): Promise<DesktopArch | null> {
  if (!nav) return null;
  const hints = nav.userAgentData;
  if (hints && typeof hints.getHighEntropyValues === 'function') {
    try {
      const v = await hints.getHighEntropyValues(['architecture', 'bitness']);
      return archFromHints(v?.architecture, v?.bitness);
    } catch {
      return null;
    }
  }
  return archFromUserAgent(nav.userAgent ?? '', nav.platform ?? '');
}

/* ── the download catalogue ────────────────────────────────────────────────
 *
 * ⚠⚠ ONE LIST, RENDERED TWICE. This used to live inside InstallPrompt.vue,
 * which was fine while the banner was the only way to reach a download. It is
 * not any more: Settings → Preferences now offers the same builds, for the
 * person who closed the reminder by accident or who is signing in from a
 * machine they have just bought. Two copies of "which file, what it does to
 * your computer, how big it is" would drift the first time a build is renamed
 * or a size changes — and a stale download row is not a missing feature, it is
 * a 404 with the product's name on it. So the entries live here and both
 * surfaces read them; only the markup around them differs.
 *
 * (The duplication gate — `web/tests/quality/duplication.test.ts` — exists to
 * catch exactly the second copy this avoids.)
 */

/** Where the installers live. One constant so the offer and the docs cannot
 *  drift apart. */
export const DESKTOP_RELEASES_URL = 'https://github.com/BRF-Tech/filex/releases/latest';
// ⚠ Asset names carry NO version, on purpose: `releases/latest/download/<name>`
// only resolves for a fixed filename, so a versioned one would send every
// visitor to a 404 the moment a new release went out.
const DL = `${DESKTOP_RELEASES_URL}/download`;

/** The desktop app's Microsoft Store page. The same product id as
 *  desktop/src/channel.ts `STORE_IDS.msstore` (a test holds the two equal). */
export const MSSTORE_URL = 'https://apps.microsoft.com/detail/9PKXDJLVZWXW';

/** The desktop app's Snap Store page. The snap is named by desktop/src/channel.ts
 *  `LINUX_APP_NAME` (a test holds the two equal). */
export const SNAP_STORE_URL = 'https://snapcraft.io/filex-app';

/** The Homebrew tap the macOS cask `filex-app` lives in. Its README carries the
 *  one-line install; the row's hint repeats it. */
export const HOMEBREW_TAP_URL = 'https://github.com/BRF-Tech/homebrew-filex';

/**
 * The release files, by kind and architecture: the names electron-builder
 * gives them (`${productName}-desktop-${arch}.${ext}`, desktop/
 * electron-builder.yml), where `${arch}` is each format's own spelling - x64
 * and arm64 for an .exe, x86_64 for an AppImage and an .rpm, amd64 for a
 * .deb, aarch64 for an arm64 .rpm.
 *
 * ⚠⚠ ONE TABLE. The prompt, Settings, docs/DESKTOP.md and filex.sh all link
 * these files, and web/tests/composables/installDownloads.test.ts holds every
 * one of those links to this table and the table to the files a release must
 * carry (scripts/release/plan.mjs `releaseAssets`), so a rename fails a test
 * instead of becoming a 404.
 */
export const DESKTOP_ASSETS = {
  win_setup: { x64: 'filex-desktop-x64.exe', arm64: 'filex-desktop-arm64.exe' },
  win_portable: { x64: 'filex-desktop-portable-x64.exe', arm64: 'filex-desktop-portable-arm64.exe' },
  appimage: { x64: 'filex-desktop-x86_64.AppImage', arm64: 'filex-desktop-arm64.AppImage' },
  deb: { x64: 'filex-desktop-amd64.deb', arm64: 'filex-desktop-arm64.deb' },
  rpm: { x64: 'filex-desktop-x86_64.rpm', arm64: 'filex-desktop-aarch64.rpm' },
  dmg: { arm64: 'filex-desktop-arm64.dmg' },
} as const;

export type DesktopAssetKind = keyof typeof DESKTOP_ASSETS;

/** The download address of one release file. */
export function desktopAssetUrl(name: string): string {
  return `${DL}/${name}`;
}

export interface DesktopDownload {
  /** What the file is, named the way it is named on the release page. */
  label: string;
  /** What it does to the machine, and roughly how big it is. */
  hint: string;
  href: string;
  /** The processor the file is built for. Absent on a store row: the store
   *  (and snapd) picks the build for the machine itself. */
  arch?: DesktopArch;
  /** The same file for the other processor, offered beside it. */
  other?: { arch: DesktopArch; label: string; href: string };
}

/** The human name of a platform — not translated, because "Windows", "Linux"
 *  and "macOS" are the same word in every catalogue we ship. */
export function desktopPlatformLabel(p: DesktopPlatform | null): string {
  return p === 'windows' ? 'Windows' : p === 'linux' ? 'Linux' : 'macOS';
}

/** What this visitor can actually download, said plainly.
 *
 *  ⚠ "Download for Windows" on its own is the complaint this replaces: it did
 *  not say whether it was an installer or a portable build, and the link went
 *  to a release page listing ten files. Each entry names the file, what it
 *  does to the machine, and roughly how big it is.
 *
 *  `arch` is this machine's processor when the browser could tell
 *  (`detectDesktopArch`), null when it could not. A file built per processor
 *  is offered for that one, or for x64 when unknown, with the label saying
 *  which, and with the other processor's file beside it (`other`).
 *
 *  ⚠ Takes the translate function rather than calling `useI18n()` itself, so
 *  it stays a plain function a test can drive with a stub and a caller outside
 *  a component setup can still use. */
export function desktopDownloadsFor(
  platform: DesktopPlatform | null,
  t: (key: string) => string,
  arch: DesktopArch | null = null,
): DesktopDownload[] {
  const mine: DesktopArch = arch ?? 'x64';
  const theirs: DesktopArch = mine === 'x64' ? 'arm64' : 'x64';
  /** A row for a file built per processor: this machine's, the other beside it. */
  const built = (kind: Exclude<DesktopAssetKind, 'dmg'>, key: string): DesktopDownload => ({
    label: `${t(`install.dl.${key}`)} · ${mine}`,
    hint: t(`install.dl.${key}_hint`),
    href: desktopAssetUrl(DESKTOP_ASSETS[kind][mine]),
    arch: mine,
    other: {
      arch: theirs,
      label: t(`install.dl.other_${theirs}`),
      href: desktopAssetUrl(DESKTOP_ASSETS[kind][theirs]),
    },
  });
  if (platform === 'windows') {
    return [
      // First since 2026-09-26, when the listing went live: the one Windows
      // build Microsoft signs (no SmartScreen prompt) and the Store updates.
      // The Store hands an Arm PC the arm64 package itself (one bundle).
      {
        label: t('install.dl.win_store'),
        hint: t('install.dl.win_store_hint'),
        href: MSSTORE_URL,
      },
      built('win_setup', 'win_setup'),
      // ⚠ A machine you may not install software on is a real case, not an
      // edge one, and it was the only platform with no answer for it: the
      // AppImage and the mac .zip already run unextracted. The hint has to say
      // what it costs, because "portable" reads as strictly better until you
      // find out it never updates.
      built('win_portable', 'win_portable'),
    ];
  }
  if (platform === 'linux') {
    return [
      // First, as the Store is on Windows: the one-click install from a
      // software centre (Ubuntu's App Center finds it), updated by snapd,
      // which also picks the x64 or the arm64 snap by itself.
      {
        label: t('install.dl.linux_snap'),
        hint: t('install.dl.linux_snap_hint'),
        href: SNAP_STORE_URL,
      },
      built('appimage', 'appimage'),
      built('deb', 'deb'),
      built('rpm', 'rpm'),
    ];
  }
  // ⚠ Apple Silicon only, and unsigned: the CI runner's arch is the artifact's
  // arch (macos-15 = arm64), and there is no Developer ID yet, so the first
  // launch is a Gatekeeper "Open Anyway"; the hint says so up front instead
  // of letting the user find out from a dialog that reads like a virus alert.
  // Homebrew second: the Mac app cannot update itself until it is signed, and
  // `brew upgrade` is the one thing that keeps a Mac copy current.
  return [
    {
      label: t('install.dl.dmg'),
      hint: t('install.dl.dmg_hint'),
      href: desktopAssetUrl(DESKTOP_ASSETS.dmg.arm64),
      arch: 'arm64',
    },
    {
      label: t('install.dl.mac_brew'),
      hint: t('install.dl.mac_brew_hint'),
      href: HOMEBREW_TAP_URL,
    },
  ];
}

/**
 * The catalogue, wired to the current locale and this machine's platform —
 * what both the reminder and the settings pane render.
 *
 * ⚠ Deliberately does NOT include the offer's own state (dismissed, standalone,
 * a pending service-worker update). Settings must show the downloads whether or
 * not the reminder was closed; that is the entire point of putting them there.
 */
export function useDesktopDownloads(): {
  platform: DesktopPlatform | null;
  platformLabel: ComputedRef<string>;
  /** This machine's processor; null until (and unless) the browser says. */
  arch: Readonly<Ref<DesktopArch | null>>;
  downloads: ComputedRef<DesktopDownload[]>;
  releasesUrl: string;
} {
  const { t } = useI18n();
  const platform = detectDesktopPlatform();
  // The synchronous answer first (the user-agent's Arm signal), so the rows
  // are right from the first paint wherever that is all there is; Chromium's
  // client hints arrive a moment later and settle it.
  const arch = ref<DesktopArch | null>(
    typeof navigator === 'undefined' ? null : archFromUserAgent(navigator.userAgent, navigator.platform),
  );
  if (platform === 'windows' || platform === 'linux') {
    void detectDesktopArch().then((a) => {
      if (a) arch.value = a;
    });
  }
  return {
    platform,
    platformLabel: computed(() => desktopPlatformLabel(platform)),
    arch: readonly(arch),
    downloads: computed(() => desktopDownloadsFor(platform, t, arch.value)),
    releasesUrl: DESKTOP_RELEASES_URL,
  };
}

/* ── the browser's own install offer, captured once for the page ──────────
 *
 * ⚠⚠ Captured at BOOT, at module level, not by a component (task #190).
 *
 * The banner's own `onMounted` used to listen, and what it listened for is a
 * one-shot: Chromium fires `beforeinstallprompt` once per page load, when its
 * checks pass, and replays it to nobody - a listener attached later has
 * missed it. The saved event was also the banner's private state, so the
 * settings dialog (the offer's permanent home, the owner's ruling of
 * 2026-09-13) could not reach it.
 *
 * `main.ts` calls `captureInstallPrompt()` before the app mounts, and every
 * reader - the banner and the settings row - shares the one saved event.
 *
 * ⚠ `preventDefault()` is what turns Android Chrome's own mini-infobar off.
 * That is right only because this app then shows an offer of its own on a
 * phone: the band on the file list (InstallPrompt.vue) and the row in the
 * settings. Before #190 neither appeared on a phone - the corner chip stepped
 * aside behind the list's upload button (lib/keepClear) and the settings had
 * no row for a phone - so a phone was shown NOTHING: Chrome's offer
 * suppressed, ours never drawn. The browser menu's "Install app" was the only
 * way left.
 */
const deferredPrompt = shallowRef<BeforeInstallPromptEvent | null>(null);
/** This page runs as the installed app, or was just installed from. */
const standalone = ref(false);

/**
 * "The app is installed on this device", remembered in THIS browser.
 *
 * ⚠ A browser tab on a phone that has the app installed is not told so:
 * Chromium simply stops firing `beforeinstallprompt`, and a page without the
 * event cannot tell "installed already" from "not offered yet". Since #190 a
 * phone without the event is shown the browser menu's way in (`menu`), so
 * without this flag every tab opened after the install would offer it again.
 * Set by `appinstalled` and by a page that runs as the app (on Android the
 * installed app and Chrome share the site's storage); cleared the moment the
 * browser offers an install again, which means it was uninstalled. A fact
 * about this device, so the browser is the right place for it, unlike a
 * dismissal (see the top of this file).
 */
const INSTALLED_KEY = 'filex.installPrompt.installed';
const installedHere = ref(false);

function rememberInstalled(on: boolean): void {
  installedHere.value = on;
  try {
    if (typeof localStorage === 'undefined') return;
    if (on) localStorage.setItem(INSTALLED_KEY, '1');
    else localStorage.removeItem(INSTALLED_KEY);
  } catch {
    /* Blocked site data: this page knows, the next one asks again. */
  }
}

function installedFlag(): boolean {
  try {
    return typeof localStorage !== 'undefined' && localStorage.getItem(INSTALLED_KEY) === '1';
  } catch {
    return false;
  }
}
let captureTarget: Window | null = null;
/** The page's service worker, registered by the first banner (see below). */
let appWorker: AppServiceWorker | null = null;

function onBeforeInstallPrompt(e: Event): void {
  // Stop Chrome's mini-infobar so we can present install on our terms.
  e.preventDefault();
  deferredPrompt.value = e as BeforeInstallPromptEvent;
  // The browser offers an install: whatever this device had, it has no more.
  if (installedHere.value) rememberInstalled(false);
}

function onAppInstalled(): void {
  deferredPrompt.value = null;
  standalone.value = true;
  rememberInstalled(true);
}

/** Start listening for the browser's install offer. Call it once, as early as
 *  the page can (main.ts, before the mount); calling it again does nothing. */
export function captureInstallPrompt(
  target: Window | undefined = typeof window === 'undefined' ? undefined : window,
): void {
  if (captureTarget || !target) return;
  captureTarget = target;
  standalone.value = detectStandalone();
  if (standalone.value) rememberInstalled(true);
  else installedHere.value = installedFlag();
  target.addEventListener('beforeinstallprompt', onBeforeInstallPrompt);
  target.addEventListener('appinstalled', onAppInstalled);
}

/** Test seam: forget the saved event, the listeners and the service worker,
 *  so one test cannot hand the next a saved event. Not used by the app. */
export function __resetInstallPrompt(): void {
  captureTarget?.removeEventListener('beforeinstallprompt', onBeforeInstallPrompt);
  captureTarget?.removeEventListener('appinstalled', onAppInstalled);
  captureTarget = null;
  deferredPrompt.value = null;
  standalone.value = false;
  installedHere.value = false;
  appWorker = null;
}

/** Inside the Electron shell (its editor window loads this SPA, with
 *  `window.filexDesktop` set by desktop/src/preload-editor.cts): there is no
 *  web app to install there. */
function inDesktopShell(): boolean {
  if (typeof window === 'undefined') return false;
  const w = window as unknown as { filexDesktop?: unknown; filexApp?: { isDesktop?: boolean } };
  if (w.filexDesktop || w.filexApp?.isDesktop === true) return true;
  return typeof navigator !== 'undefined' && /Electron\//i.test(navigator.userAgent ?? '');
}

/**
 * How THIS device installs the web app:
 *
 *   · `prompt` - the browser has offered an install to the page (Android
 *     Chrome, Samsung Internet, Edge): one button opens its own dialog;
 *   · `ios`    - iPhone and iPad, where no browser offers one to a page: the
 *     Share sheet's "Add to Home Screen" (any iOS browser since 16.4);
 *   · `menu`   - a phone or a tablet whose browser has not offered one (yet):
 *     its own menu still installs the page (Chrome offers it to the page only
 *     after a tap and about 30 seconds on the site);
 *   · `null`   - nothing to offer: running as the installed app already,
 *     installed on this device (`installed`, see INSTALLED_KEY), inside the
 *     desktop shell, or on a PC, which is offered the DESKTOP app instead
 *     (`detectDesktopPlatform`).
 */
export type AppInstallState = 'prompt' | 'ios' | 'menu' | null;

export function appInstallState(o: {
  standalone: boolean;
  desktopShell: boolean;
  desktop: DesktopPlatform | null;
  canPrompt: boolean;
  ios: boolean;
  installed?: boolean;
}): AppInstallState {
  if (o.standalone || o.desktopShell || o.desktop !== null) return null;
  if (o.canPrompt) return 'prompt';
  if (o.installed) return null;
  if (o.ios) return 'ios';
  return 'menu';
}

/** The browser's install dialog. ⚠ The event is single-use (a second
 *  `prompt()` throws), so it is let go BEFORE the dialog is awaited: a second
 *  tap while the dialog is up finds nothing to call. */
async function promptInstall(): Promise<'accepted' | 'dismissed' | 'unavailable'> {
  const evt = deferredPrompt.value;
  if (!evt) return 'unavailable';
  deferredPrompt.value = null;
  try {
    await evt.prompt();
    const { outcome } = await evt.userChoice;
    // Installed: say so at once rather than wait for `appinstalled`, which
    // comes a moment later - in between the band would fall back to the
    // browser menu's way and offer the install that was just accepted.
    if (outcome === 'accepted') rememberInstalled(true);
    return outcome;
  } catch {
    return 'unavailable';
  }
}

export interface AppInstall {
  /** How this device installs the web app; null when there is nothing to offer. */
  state: ComputedRef<AppInstallState>;
  /** Running as the installed app, or installed from this page just now. */
  isStandalone: Readonly<Ref<boolean>>;
  isIOS: boolean;
  /** Open the browser's own install dialog (state `prompt`). */
  promptInstall: () => Promise<'accepted' | 'dismissed' | 'unavailable'>;
}

/**
 * The web app's own install, for whoever offers it: the banner below and the
 * settings dialog's row (UserSettingsModal → core's `host.installApp`).
 *
 * ⚠ No dismissal here, on purpose: the settings row is the home that stays
 * after the reminder is closed for good, so it must not read that flag.
 */
export function useAppInstall(): AppInstall {
  captureInstallPrompt();
  const ios = detectIOS();
  const desktop = detectDesktopPlatform();
  const desktopShell = inDesktopShell();
  return {
    state: computed(() =>
      appInstallState({
        standalone: standalone.value,
        desktopShell,
        desktop,
        canPrompt: deferredPrompt.value !== null,
        ios,
        installed: installedHere.value,
      }),
    ),
    isStandalone: readonly(standalone),
    isIOS: ios,
    promptInstall,
  };
}

/* ── the service worker: registered once for the page ─────────────────── */

function appServiceWorker(): AppServiceWorker {
  if (appWorker) return appWorker;

  // ⚠⚠ In dev, tear down any service worker that is still registered from a
  // previous run, and do it before anything else asks the network.
  //
  // The dev SW is opt-in now (FILEX_DEV_SW=1, see web/vite.config.ts) because
  // it precaches a manifest of BUILT hashed filenames while the dev server
  // serves unhashed module URLs: every request missed, two console lines each,
  // about a hundred lines before the app painted. But turning the plugin off
  // does not remove a worker a browser has ALREADY registered — that one keeps
  // running, keeps logging, and keeps answering navigations from a precache
  // that no longer matches the code being served, which shows up as a blank
  // page. So the app unregisters it itself rather than asking a person to go
  // and clear their site data.
  const sw = typeof navigator === 'undefined' ? undefined : navigator.serviceWorker;
  if (import.meta.env.DEV && sw && typeof sw.getRegistrations === 'function') {
    void sw
      .getRegistrations()
      .then((regs) => {
        for (const r of regs) void r.unregister();
      })
      .catch(() => {
        /* nothing registered, or no worker API: nothing to tear down */
      });
  }

  // Service-worker update state (registerType: 'prompt' in vite.config.ts).
  // Registered at the address the base path gives it (lib/serviceWorker).
  // ⚠ Once per page: the banner is the only caller today, but a second one
  // would otherwise register the worker a second time.
  appWorker = registerAppServiceWorker({
    onRegisteredSW(url) {
      console.debug('[pwa] service worker registered:', url);
    },
  });
  return appWorker;
}

export function useInstallPrompt() {
  const app = useAppInstall();
  /* ⚠⚠ COMPUTEDS, not values read once at setup. The document arrives from
   * the network a moment after this composable runs, so a value snapshotted
   * here would be "not dismissed" for every person on every load — the card
   * would flash before the answer landed, or simply show. `slot.get()` reads
   * reactive state inside `lib/viewPrefs`, so these settle by themselves the
   * moment the document is there. */
  const dismissedLocally = ref(legacyDismissed());
  const desktopDismissed = computed(
    () => dismissedLocally.value || installSlot.get()?.dismissed === true,
  );
  const appDismissed = computed(() => dismissedLocally.value || installSlot.get()?.app === true);

  const { needRefresh, updateServiceWorker } = appServiceWorker();

  // Chrome/Edge/Android: the browser offered a native install → show a button.
  // ⚠ Never on a PC (`useAppInstall` answers null there): the desktop app
  // wins, and offering both at once asks the user to choose between two
  // things that sound identical.
  const canPromptInstall = computed(() => app.state.value === 'prompt' && !appDismissed.value);

  // iOS Safari: no native prompt exists → show manual "Add to Home Screen" help.
  const showIOSInstructions = computed(() => app.state.value === 'ios' && !appDismissed.value);

  // A phone whose browser has not offered an install (yet): its own menu
  // does it. ⚠ Shown, not left out (the owner's ruling, 2026-10-06: a phone
  // gets the band the PC's desktop download has, from the first page): Chrome
  // offers the install to a page only after a tap and about 30 seconds on
  // the site, so on a first visit to the sign-in page there is no event yet.
  // The band turns into the Install button the moment the event arrives.
  const showMenuInstructions = computed(() => app.state.value === 'menu' && !appDismissed.value);

  // PC visitors: offer the native desktop app instead of a browser install.
  const desktopPlatform = ref(detectDesktopPlatform());
  const showDesktopDownload = computed(
    () => desktopPlatform.value !== null && !app.isStandalone.value && !desktopDismissed.value,
  );

  // Anything to show at all? Drives whether the banner mounts.
  const shouldOfferInstall = computed(
    () =>
      canPromptInstall.value ||
      showIOSInstructions.value ||
      showMenuInstructions.value ||
      showDesktopDownload.value,
  );

  /** Hide the offer and remember it so we don't nag on every load.
   *
   *  ⚠ The offer on screen decides WHICH flag: the desktop app's on a PC, the
   *  web app's own everywhere else (see the dismissal note at the top).
   *
   *  ⚠ One place or the other, never both. With an account behind the session
   *  the flag goes in the person's document and follows them; with no account
   *  — a share link, an app-token embed — it goes in this browser, which is
   *  the only place there is. Writing both would put the dismissal back in the
   *  browser for everyone and hand it to the next person on a shared machine,
   *  which is the whole thing this move undoes. */
  function dismiss(): void {
    const desktopOffer = showDesktopDownload.value;
    if (installSlot.ready() && installSlot.persistable()) {
      const next = { ...(installSlot.get() ?? {}) };
      if (desktopOffer) next.dismissed = true;
      else next.app = true;
      installSlot.set(next);
      return;
    }
    dismissedLocally.value = true;
    try {
      if (typeof localStorage !== 'undefined') localStorage.setItem(DISMISS_KEY, '1');
    } catch {
      /* Blocked site data: the card stays closed for this page load and comes
         back on the next one. Nothing better is available here. */
    }
  }

  /** Apply a pending service-worker update and reload into the fresh bundle. */
  function reloadForUpdate(): void {
    void updateServiceWorker(true);
  }

  return {
    isStandalone: app.isStandalone,
    isIOS: computed(() => app.isIOS),
    canPromptInstall,
    desktopPlatform,
    showDesktopDownload,
    showIOSInstructions,
    showMenuInstructions,
    shouldOfferInstall,
    needRefresh,
    promptInstall: app.promptInstall,
    dismiss,
    reloadForUpdate,
  };
}
