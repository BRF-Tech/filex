// Installing the web app itself on a phone (task #190).
//
// ⚠⚠ Owner, 2026-10-06: "application filex yani tarayıcı app'i olarak
// kurulsun bildirimi mobilde gelmiyor". On a PC the offer showed; on a phone
// nothing did, and three things in this composable's corner made sure of it:
//
//   1. `beforeinstallprompt` was caught by the banner component's own
//      `onMounted`. The event is a one-shot - Chromium fires it once per page
//      load and replays it to nobody - and the saved event was the banner's
//      private state, so the settings dialog could not offer it at all. It is
//      caught at boot now (`captureInstallPrompt`, main.ts) and shared.
//   2. ONE dismissal flag for two offers: closing the desktop-app chip on a PC
//      (a flag that follows the account to every device) also closed the
//      phone's offer before it was ever shown. The phone's is `app` now.
//   3. Chrome's own install bar is suppressed (`preventDefault`) in favour of
//      ours, and ours never reached a phone's screen - that half is the
//      banner's (installPromptPhone.test.ts) and the settings row's
//      (userSettingsInstallApp.test.ts).
//
// What is pinned here is the decision: which device is offered what, by
// whom, once, and what closing it remembers.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { __flushViewPrefs, __resetViewPrefs, attachViewPrefsStore } from '@brftech/filex-core';

vi.mock('@/lib/serviceWorker', async () => {
  const { ref } = await import('vue');
  return {
    registerAppServiceWorker: vi.fn(() => ({
      needRefresh: ref(false),
      updateServiceWorker: vi.fn(async () => {}),
    })),
    swLocation: () => ({ url: '/admin/sw.js', scope: '/admin/' }),
  };
});

import {
  __resetInstallPrompt,
  appInstallState,
  captureInstallPrompt,
  useAppInstall,
  useInstallPrompt,
} from '@/composables/useInstallPrompt';
import { registerAppServiceWorker } from '@/lib/serviceWorker';

const UA = {
  android:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36',
  iPhone:
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
  windows:
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36',
};

/** This test's device, as the browser describes it. */
function onDevice(userAgent: string, extra: Record<string, unknown> = {}): void {
  vi.stubGlobal('navigator', { userAgent, platform: '', ...extra });
}

/** The display mode the page reports: `browser` for a tab. */
function displayMode(mode: 'browser' | 'standalone'): void {
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: q === `(display-mode: ${mode})`,
    media: q,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }));
}

/** Chromium's install offer, the way it reaches the page. */
function offerInstall(outcome: 'accepted' | 'dismissed' = 'accepted') {
  const e = new Event('beforeinstallprompt', { cancelable: true });
  const prompt = vi.fn(async () => {});
  Object.assign(e, {
    platforms: ['web'],
    prompt,
    userChoice: Promise.resolve({ outcome, platform: 'web' }),
  });
  window.dispatchEvent(e);
  return { event: e, prompt };
}

/** The account's per-user document, held in a variable. */
function signedIn(doc: Record<string, unknown> = {}) {
  const saved: Array<Record<string, unknown>> = [];
  attachViewPrefsStore({
    load: async () => doc,
    save: (d) => saved.push(JSON.parse(JSON.stringify(d))),
  });
  return saved;
}

/** The document loads asynchronously; let it land. */
const settle = () => new Promise<void>((r) => setTimeout(r, 0));

beforeEach(() => {
  __resetInstallPrompt();
  __resetViewPrefs();
  displayMode('browser');
  vi.mocked(registerAppServiceWorker).mockClear();
});

afterEach(() => {
  __resetInstallPrompt();
  __resetViewPrefs();
  vi.unstubAllGlobals();
  delete (window as unknown as { filexDesktop?: unknown }).filexDesktop;
});

describe("the browser's install offer is caught once, for the whole page", () => {
  it('is caught before any component exists, and Chrome’s own bar is held back for ours', () => {
    onDevice(UA.android);
    captureInstallPrompt();
    const { event } = offerInstall();
    expect(event.defaultPrevented).toBe(true);
    expect(useAppInstall().state.value).toBe('prompt');
  });

  it('the reminder and the settings row read the ONE saved event, and it is used once', async () => {
    onDevice(UA.android);
    captureInstallPrompt();
    const { prompt } = offerInstall('accepted');
    const reminder = useInstallPrompt();
    const settings = useAppInstall();
    expect(reminder.canPromptInstall.value).toBe(true);
    expect(settings.state.value).toBe('prompt');

    await expect(settings.promptInstall()).resolves.toBe('accepted');
    expect(prompt).toHaveBeenCalledTimes(1);
    // Single-use: neither reader can call it a second time.
    expect(reminder.canPromptInstall.value).toBe(false);
    await expect(reminder.promptInstall()).resolves.toBe('unavailable');
    expect(prompt).toHaveBeenCalledTimes(1);
    // Accepted is installed: nothing is offered any more, before `appinstalled`.
    expect(settings.state.value).toBeNull();
    expect(reminder.shouldOfferInstall.value).toBe(false);
  });

  it('turned down in the browser’s dialog: the menu’s way is left, and the event is spent', async () => {
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall('dismissed');
    const settings = useAppInstall();
    await expect(settings.promptInstall()).resolves.toBe('dismissed');
    expect(settings.state.value).toBe('menu');
    expect(localStorage.getItem('filex.installPrompt.installed')).toBeNull();
  });

  it('once the browser has installed it, this page offers nothing', () => {
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    const reminder = useInstallPrompt();
    window.dispatchEvent(new Event('appinstalled'));
    expect(useAppInstall().state.value).toBeNull();
    expect(reminder.shouldOfferInstall.value).toBe(false);
  });

  it('the service worker is registered once, however many readers there are', () => {
    onDevice(UA.android);
    useInstallPrompt();
    useInstallPrompt();
    expect(registerAppServiceWorker).toHaveBeenCalledTimes(1);
  });
});

describe('which way a device installs the web app', () => {
  const base = { standalone: false, desktopShell: false, desktop: null, canPrompt: false, ios: false };

  it.each([
    ['a phone whose browser offered an install', { canPrompt: true }, 'prompt'],
    ['an iPhone or an iPad', { ios: true }, 'ios'],
    ['a phone whose browser has not offered one (yet)', {}, 'menu'],
    ['a later tab on a phone that has the app installed', { installed: true }, null],
    ['…until the browser offers it again: it was uninstalled', { installed: true, canPrompt: true }, 'prompt'],
    ['a PC - it is offered the desktop app instead', { desktop: 'windows', canPrompt: true }, null],
    ['the installed app itself', { standalone: true, canPrompt: true }, null],
    ['the desktop app’s own window', { desktopShell: true }, null],
  ] as const)('%s', (_name, over, want) => {
    expect(appInstallState({ ...base, ...over })).toBe(want);
  });

  it('an Android phone before the browser offers: the band says where its menu has it', () => {
    // ⚠ The owner's ruling (2026-10-06): a phone gets the band from the first
    // page, like a PC's desktop download. Chrome offers the install to a page
    // only after a tap and ~30 s on the site, so a first visit has no event.
    onDevice(UA.android);
    const reminder = useInstallPrompt();
    expect(useAppInstall().state.value).toBe('menu');
    expect(reminder.showMenuInstructions.value).toBe(true);
    expect(reminder.shouldOfferInstall.value).toBe(true);
    expect(reminder.canPromptInstall.value).toBe(false);
    // …and becomes the Install button the moment the browser offers.
    offerInstall();
    expect(reminder.canPromptInstall.value).toBe(true);
    expect(reminder.showMenuInstructions.value).toBe(false);
  });

  it('an iPhone: Add to Home Screen in the reminder and in the settings, never an install button', () => {
    onDevice(UA.iPhone);
    const reminder = useInstallPrompt();
    expect(useAppInstall().state.value).toBe('ios');
    expect(reminder.showIOSInstructions.value).toBe(true);
    expect(reminder.canPromptInstall.value).toBe(false);
  });

  it('a PC: the desktop app, even when the browser offers to install the page', () => {
    onDevice(UA.windows);
    captureInstallPrompt();
    offerInstall();
    const reminder = useInstallPrompt();
    expect(useAppInstall().state.value).toBeNull();
    expect(reminder.canPromptInstall.value).toBe(false);
    expect(reminder.showDesktopDownload.value).toBe(true);
  });

  it('inside the desktop app’s window: nothing', () => {
    onDevice(UA.android);
    (window as unknown as { filexDesktop?: unknown }).filexDesktop = true;
    expect(useAppInstall().state.value).toBeNull();
  });
});

describe('the installed app offers nothing', () => {
  it('a later tab on the phone it was installed on offers nothing - until the browser offers again', () => {
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    window.dispatchEvent(new Event('appinstalled'));
    expect(localStorage.getItem('filex.installPrompt.installed')).toBe('1');

    // The next page in a browser tab: no event (Chromium stops offering an
    // installed app), and no band.
    __resetInstallPrompt();
    const reminder = useInstallPrompt();
    expect(useAppInstall().state.value).toBeNull();
    expect(reminder.shouldOfferInstall.value).toBe(false);

    // The app was uninstalled: the browser offers again, and so does filex.
    offerInstall();
    expect(useAppInstall().state.value).toBe('prompt');
    expect(reminder.canPromptInstall.value).toBe(true);
    expect(localStorage.getItem('filex.installPrompt.installed')).toBeNull();
  });

  it('the installed app tells the browser tabs of its device that it is there', () => {
    displayMode('standalone');
    onDevice(UA.android);
    captureInstallPrompt();
    expect(localStorage.getItem('filex.installPrompt.installed')).toBe('1');
  });

  it('display-mode: standalone (Android, desktop Chrome)', () => {
    displayMode('standalone');
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    const reminder = useInstallPrompt();
    expect(useAppInstall().state.value).toBeNull();
    expect(reminder.shouldOfferInstall.value).toBe(false);
  });

  it('navigator.standalone (an iPhone’s Home Screen app)', () => {
    onDevice(UA.iPhone, { standalone: true });
    const reminder = useInstallPrompt();
    expect(useAppInstall().state.value).toBeNull();
    expect(reminder.showIOSInstructions.value).toBe(false);
  });
});

describe('closing the reminder: two offers, two flags', () => {
  it('closing the desktop app’s offer on a PC does not close the phone’s', async () => {
    signedIn({ install: { dismissed: true } });
    await settle();
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    expect(useInstallPrompt().canPromptInstall.value).toBe(true);
  });

  it('…nor the iPhone’s Add to Home Screen help', async () => {
    signedIn({ install: { dismissed: true } });
    await settle();
    onDevice(UA.iPhone);
    expect(useInstallPrompt().showIOSInstructions.value).toBe(true);
  });

  it('closing it on the phone writes `app` to the account, and it stays closed', async () => {
    const saved = signedIn({});
    await settle();
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    const reminder = useInstallPrompt();
    reminder.dismiss();
    __flushViewPrefs();
    expect(saved.at(-1)?.install).toEqual({ app: true });
    expect(reminder.canPromptInstall.value).toBe(false);
    expect(useInstallPrompt().canPromptInstall.value).toBe(false);
    // The browser flag is for nobody-signed-in; an account keeps its own.
    expect(localStorage.getItem('filex.installPrompt.dismissed')).toBeNull();
  });

  it('closing the desktop app’s offer still writes `dismissed`, as it always has', async () => {
    const saved = signedIn({});
    await settle();
    onDevice(UA.windows);
    const reminder = useInstallPrompt();
    expect(reminder.showDesktopDownload.value).toBe(true);
    reminder.dismiss();
    __flushViewPrefs();
    expect(saved.at(-1)?.install).toEqual({ dismissed: true });
  });

  it('a dismissal kept in this browser closes the offer this browser shows', () => {
    localStorage.setItem('filex.installPrompt.dismissed', '1');
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    expect(useInstallPrompt().canPromptInstall.value).toBe(false);
    // The same flag in an iPhone's browser.
    __resetInstallPrompt();
    onDevice(UA.iPhone);
    expect(useAppInstall().state.value).toBe('ios');
    expect(useInstallPrompt().showIOSInstructions.value).toBe(false);
  });

  it('the settings row does not read the dismissal: it is the home that stays', async () => {
    signedIn({ install: { app: true } });
    await settle();
    onDevice(UA.android);
    captureInstallPrompt();
    offerInstall();
    expect(useInstallPrompt().canPromptInstall.value).toBe(false);
    expect(useAppInstall().state.value).toBe('prompt');
  });
});
