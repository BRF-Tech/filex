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
// scope guard.
import { computed, onBeforeUnmount, onMounted, readonly, ref, type ComputedRef } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRegisterSW } from 'virtual:pwa-register/vue';
import { viewPrefsSlot } from '@brftech/filex-core';

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
 */
const DISMISS_KEY = 'filex.installPrompt.dismissed';

/** The document's corner for this feature. One top-level key, `install`. */
const installSlot = viewPrefsSlot<{ dismissed?: boolean }>('install');

function legacyDismissed(): boolean {
  try {
    return typeof localStorage !== 'undefined' && localStorage.getItem(DISMISS_KEY) === '1';
  } catch {
    /* private mode / blocked site data — no flag is a perfectly good answer. */
    return false;
  }
}

/** True when the app is already running as an installed PWA (any platform). */
function detectStandalone(): boolean {
  if (typeof window === 'undefined') return false;
  const displayStandalone =
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(display-mode: standalone)').matches;
  // iOS Safari doesn't support display-mode; it exposes navigator.standalone.
  const iosStandalone = (window.navigator as unknown as { standalone?: boolean }).standalone === true;
  return displayStandalone || iosStandalone;
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

export interface DesktopDownload {
  /** What the file is, named the way it is named on the release page. */
  label: string;
  /** What it does to the machine, and roughly how big it is. */
  hint: string;
  href: string;
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
 *  ⚠ Takes the translate function rather than calling `useI18n()` itself, so
 *  it stays a plain function a test can drive with a stub and a caller outside
 *  a component setup can still use. */
export function desktopDownloadsFor(
  platform: DesktopPlatform | null,
  t: (key: string) => string,
): DesktopDownload[] {
  if (platform === 'windows') {
    return [
      {
        label: t('install.dl.win_setup'),
        hint: t('install.dl.win_setup_hint'),
        href: `${DL}/filex-desktop-x64.exe`,
      },
      // ⚠ A machine you may not install software on is a real case, not an
      // edge one — and it was the only platform with no answer for it: the
      // AppImage and the mac .zip already run unextracted. The hint has to say
      // what it costs, because "portable" reads as strictly better until you
      // find out it never updates.
      {
        label: t('install.dl.win_portable'),
        hint: t('install.dl.win_portable_hint'),
        href: `${DL}/filex-desktop-portable-x64.exe`,
      },
    ];
  }
  if (platform === 'linux') {
    return [
      {
        label: t('install.dl.appimage'),
        hint: t('install.dl.appimage_hint'),
        href: `${DL}/filex-desktop-x86_64.AppImage`,
      },
      {
        label: t('install.dl.deb'),
        hint: t('install.dl.deb_hint'),
        href: `${DL}/filex-desktop-amd64.deb`,
      },
    ];
  }
  // ⚠ Apple Silicon only, and unsigned: the CI runner's arch is the artifact's
  // arch (macos-14 = arm64), and there is no Developer ID yet, so the first
  // launch is a Gatekeeper "Open Anyway" — the hint says so up front instead
  // of letting the user find out from a dialog that reads like a virus alert.
  return [
    {
      label: t('install.dl.dmg'),
      hint: t('install.dl.dmg_hint'),
      href: `${DL}/filex-desktop-arm64.dmg`,
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
  downloads: ComputedRef<DesktopDownload[]>;
  releasesUrl: string;
} {
  const { t } = useI18n();
  const platform = detectDesktopPlatform();
  return {
    platform,
    platformLabel: computed(() => desktopPlatformLabel(platform)),
    downloads: computed(() => desktopDownloadsFor(platform, t)),
    releasesUrl: DESKTOP_RELEASES_URL,
  };
}

export function useInstallPrompt() {
  const deferredPrompt = ref<BeforeInstallPromptEvent | null>(null);
  const isStandalone = ref(detectStandalone());
  const isIOS = ref(detectIOS());
  /* ⚠⚠ A COMPUTED, not a ref read once at setup. The document arrives from the
   * network a moment after this composable runs, so a value snapshotted here
   * would be "not dismissed" for every person on every load — the card would
   * flash before the answer landed, or simply show. `slot.ready()` and
   * `slot.get()` both read reactive state inside `lib/viewPrefs`, so this
   * settles by itself the moment the document is there. */
  const dismissedLocally = ref(legacyDismissed());
  const dismissed = computed(
    () => dismissedLocally.value || installSlot.get()?.dismissed === true,
  );

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
  if (import.meta.env.DEV && typeof navigator !== 'undefined' && navigator.serviceWorker) {
    void navigator.serviceWorker.getRegistrations().then((regs) => {
      for (const r of regs) void r.unregister();
    });
  }

  // Service-worker update state (registerType: 'prompt' in vite.config.ts).
  const { needRefresh, updateServiceWorker } = useRegisterSW({
    onRegisteredSW(url) {
      console.debug('[pwa] service worker registered:', url);
    },
  });

  // Chrome/Edge/Android: the browser offered a native install → show a button.
  const canPromptInstall = computed(
    () =>
      deferredPrompt.value !== null &&
      !isStandalone.value &&
      !dismissed.value &&
      // On a PC the desktop app wins: offering both at once asks the user to
      // choose between two things that sound identical.
      detectDesktopPlatform() === null,
  );

  // iOS Safari: no native prompt exists → show manual "Add to Home Screen" help.
  const showIOSInstructions = computed(
    () => isIOS.value && !isStandalone.value && !dismissed.value,
  );

  // PC visitors: offer the native desktop app instead of a browser install.
  const desktopPlatform = ref(detectDesktopPlatform());
  const showDesktopDownload = computed(
    () => desktopPlatform.value !== null && !isStandalone.value && !dismissed.value,
  );

  // Anything to show at all? Drives whether the banner mounts.
  const shouldOfferInstall = computed(
    () => canPromptInstall.value || showIOSInstructions.value || showDesktopDownload.value,
  );

  function onBeforeInstallPrompt(e: Event) {
    // Stop Chrome's mini-infobar so we can present install on our terms.
    e.preventDefault();
    deferredPrompt.value = e as BeforeInstallPromptEvent;
  }

  function onAppInstalled() {
    deferredPrompt.value = null;
    isStandalone.value = true;
  }

  /** Trigger the native install dialog (Chrome/Edge/Android). Returns the
   *  user's choice; the event is single-use so it's cleared afterwards. */
  async function promptInstall(): Promise<'accepted' | 'dismissed' | 'unavailable'> {
    const evt = deferredPrompt.value;
    if (!evt) return 'unavailable';
    await evt.prompt();
    const { outcome } = await evt.userChoice;
    deferredPrompt.value = null;
    return outcome;
  }

  /** Hide the offer and remember it so we don't nag on every load.
   *
   *  ⚠ One place or the other, never both. With an account behind the session
   *  the flag goes in the person's document and follows them; with no account
   *  — a share link, an app-token embed — it goes in this browser, which is
   *  the only place there is. Writing both would put the dismissal back in the
   *  browser for everyone and hand it to the next person on a shared machine,
   *  which is the whole thing this move undoes. */
  function dismiss(): void {
    if (installSlot.ready() && installSlot.persistable()) {
      installSlot.set({ ...(installSlot.get() ?? {}), dismissed: true });
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
    updateServiceWorker(true);
  }

  onMounted(() => {
    window.addEventListener('beforeinstallprompt', onBeforeInstallPrompt);
    window.addEventListener('appinstalled', onAppInstalled);
  });

  onBeforeUnmount(() => {
    window.removeEventListener('beforeinstallprompt', onBeforeInstallPrompt);
    window.removeEventListener('appinstalled', onAppInstalled);
  });

  return {
    isStandalone: readonly(isStandalone),
    isIOS: readonly(isIOS),
    canPromptInstall,
    desktopPlatform,
    showDesktopDownload,
    showIOSInstructions,
    shouldOfferInstall,
    needRefresh,
    promptInstall,
    dismiss,
    reloadForUpdate,
  };
}
