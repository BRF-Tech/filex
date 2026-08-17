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
import { computed, onBeforeUnmount, onMounted, readonly, ref } from 'vue';
import { useRegisterSW } from 'virtual:pwa-register/vue';

// The Chromium-only event fired when the app meets installability criteria.
interface BeforeInstallPromptEvent extends Event {
  readonly platforms: string[];
  prompt(): Promise<void>;
  readonly userChoice: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>;
}

// Persisted so a user who dismissed the banner isn't nagged every load.
const DISMISS_KEY = 'filex.installPrompt.dismissed';

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
function detectDesktopPlatform(): 'windows' | 'linux' | 'mac' | null {
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

export function useInstallPrompt() {
  const deferredPrompt = ref<BeforeInstallPromptEvent | null>(null);
  const isStandalone = ref(detectStandalone());
  const isIOS = ref(detectIOS());
  const dismissed = ref(
    typeof localStorage !== 'undefined' && localStorage.getItem(DISMISS_KEY) === '1',
  );

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

  /** Hide the offer and remember it so we don't nag on every load. */
  function dismiss(): void {
    dismissed.value = true;
    if (typeof localStorage !== 'undefined') localStorage.setItem(DISMISS_KEY, '1');
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
