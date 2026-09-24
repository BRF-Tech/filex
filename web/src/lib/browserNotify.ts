// Browser notifications for the bell.
//
// The bell is only seen by somebody who is looking at it. This raises the same
// event as an OS-level toast while the tab is open, so a file landing in a
// shared folder reaches a person who is in another tab.
//
// Three rules the implementation exists to keep:
//
//   • PERMISSION IS ASKED FROM A GESTURE, never on load. A `Notification`
//     permission prompt that appears because a page loaded is the single most
//     disliked pattern on the web, and Chrome/Firefox both penalise it — Chrome
//     shows a muted "blocked" chip instead of the prompt for origins that ask
//     without interaction, so asking on load can permanently cost you the
//     permission you were asking for. `requestPermission()` below is called
//     only from the button in notification settings.
//   • IT DEGRADES SILENTLY. No API (older Safari, an insecure origin, an
//     embedded webview), permission denied, the constructor throwing on
//     Android Chrome where only a service worker may notify — every one of
//     those is a no-op, never an error the user sees. A notification that
//     cannot be shown is not a failure of the thing that triggered it.
//   • IT NEVER FIRES INSIDE THE DESKTOP APP. The desktop shell raises a NATIVE
//     OS notification for the same row (desktop/src/main.ts); without this
//     guard the same event would appear twice on the same machine.
//
// ⚠ A notification body carries a name, a count and a target — never file
// CONTENT and never a credential. The strings here come from the server's
// `title`/`body` fields, which are the same strings the bell shows.

import { BRAND_BADGE_URL, BRAND_ICON_URL, brandName } from './brand';

const ENABLED_KEY = 'filex.notify.browser';
const ASKED_KEY = 'filex.notify.browserAsked';

export type BrowserNotifyPermission = 'granted' | 'denied' | 'default' | 'unsupported';

/** True inside the Electron shell, which does its own native notifications. */
export function isDesktopShell(): boolean {
  if (typeof window === 'undefined') return false;
  const w = window as unknown as { filexApp?: { isDesktop?: boolean } };
  if (w.filexApp?.isDesktop === true) return true;
  return typeof navigator !== 'undefined' && /Electron\//i.test(navigator.userAgent ?? '');
}

/** Whether this browser can raise notifications at all. */
export function browserNotifySupported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window && !isDesktopShell();
}

export function browserNotifyPermission(): BrowserNotifyPermission {
  if (!browserNotifySupported()) return 'unsupported';
  try {
    return (window.Notification.permission as BrowserNotifyPermission) ?? 'default';
  } catch {
    return 'unsupported';
  }
}

// ── the per-user switch ──────────────────────────────────────────────────
//
// ⚠ Stored per user in localStorage, NOT in the server-side notification
// settings row, and that is deliberate rather than a shortcut: the permission
// this switch turns into a toast is granted per browser profile, per device.
// A server-side flag would travel to a machine where the permission was never
// granted (the switch says "on", nothing ever appears) or where it was denied
// in the browser (the switch says "on", the browser says no) — a preference
// that cannot be honoured where it is read. The key carries the user id so two
// accounts sharing a machine keep separate answers.

function key(base: string, userId: number | null | undefined): string {
  return userId ? `${base}.${userId}` : base;
}

function read(k: string): string | null {
  try {
    return localStorage.getItem(k);
  } catch {
    return null; // private mode / blocked site data
  }
}

function write(k: string, v: string): void {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* the preference is a convenience; never let storage take the page down */
  }
}

/**
 * Is the user's switch on? Default ON — but nothing is ever shown without the
 * browser permission as well, so "on by default" cannot surprise anybody: it
 * only means the switch does not have to be found before the button that asks
 * for permission does anything.
 */
export function browserNotifyEnabled(userId?: number | null): boolean {
  return read(key(ENABLED_KEY, userId)) !== '0';
}

export function setBrowserNotifyEnabled(enabled: boolean, userId?: number | null): void {
  write(key(ENABLED_KEY, userId), enabled ? '1' : '0');
}

/** Have we already put the permission prompt in front of this user? */
export function browserNotifyAsked(userId?: number | null): boolean {
  return read(key(ASKED_KEY, userId)) === '1';
}

/**
 * Ask the browser for permission. MUST be called from a user gesture — see the
 * note at the top of the file. Returns the resulting permission.
 */
export async function requestBrowserNotifyPermission(
  userId?: number | null,
): Promise<BrowserNotifyPermission> {
  if (!browserNotifySupported()) return 'unsupported';
  write(key(ASKED_KEY, userId), '1');
  try {
    // Safari < 16 returns nothing and takes a callback; both shapes are
    // covered by awaiting whatever comes back.
    const res = await Promise.resolve(window.Notification.requestPermission());
    return (res as BrowserNotifyPermission) ?? browserNotifyPermission();
  } catch {
    return browserNotifyPermission();
  }
}

/** Everything that has to be true before a toast is raised. */
export function canShowBrowserNotification(userId?: number | null): boolean {
  return browserNotifySupported() && browserNotifyPermission() === 'granted' && browserNotifyEnabled(userId);
}

export interface BrowserNotifyOptions {
  title: string;
  body?: string;
  /**
   * Collapse key. Two notifications sharing a tag replace each other rather
   * than stacking — one row per notification id, so a re-render or a second
   * poll cannot produce two toasts for one event.
   */
  tag?: string;
  /**
   * Where a click goes, as an ADDRESS. Needed only by the service-worker
   * path, which has no page to call back into; the in-page path uses
   * `onClick` and this is ignored.
   */
  url?: string;
  onClick?: () => void;
}

/**
 * The toast's own fields, brand included — one definition for both paths.
 *
 * ⚠⚠ Three of them were wrong or missing, and each cost something the
 * owner could see:
 *
 *   • TITLE carried the event's sentence and nothing else, so a toast said
 *     "New file: report.pdf" with the bare ORIGIN printed under it. The second
 *     line is the app's identity and the browser only fills it in for an
 *     INSTALLED app — everywhere else the name has to be in the text. So the
 *     instance's name (the operator's own, when they set one on the Branding
 *     page) is the title and the event becomes the body.
 *   • ICON pointed at an SVG. Chromium decodes a notification's icon through
 *     its image decoders and SVG is not among them, so that was not a small
 *     logo or a blurry one — it was NO logo, and the toast fell back to a
 *     generic bell. Firefox draws SVG fine, which is exactly why it lasted.
 *   • BADGE did not exist. On Android the status bar shows the badge and
 *     nothing else, so every filex notification was a grey dot there.
 */
export function brandedNotification(opts: BrowserNotifyOptions): {
  title: string;
  options: NotificationOptions;
} {
  // ⚠ The name is READ here, not fetched: `lib/documentTitle` asks for it on
  // every route change, so by the time a notification arrives the answer is
  // already in. A fetch from here would put a network call behind a toast —
  // and behind every test that raises one.
  // ⚠ The event sentence is never dropped to make room for the name: the two
  // are joined, because "New file: report.pdf" and "2.4 MB, in Documents" are
  // different facts and the toast has room for both.
  const body = opts.body ? `${opts.title} — ${opts.body}` : opts.title;
  const options: NotificationOptions = {
    body,
    icon: BRAND_ICON_URL,
    badge: BRAND_BADGE_URL,
    tag: opts.tag,
    data: { url: opts.url ?? '' },
  };
  // Android replaces a same-tag notification SILENTLY unless this is set, so
  // a second event carrying the same id would arrive with no alert at all.
  if (opts.tag) (options as NotificationOptions & { renotify?: boolean }).renotify = true;
  return { title: brandName(), options };
}

/**
 * The service worker that owns this page, when there is one.
 *
 * ⚠ It is scoped to `/admin/` (vite.config.ts), so a page served from
 * `/drive/` legitimately has none — that is a narrower frame, never a missing
 * notification, because the constructor path still works there.
 */
async function swRegistration(): Promise<ServiceWorkerRegistration | null> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return null;
  try {
    const reg = await navigator.serviceWorker.getRegistration();
    return reg && typeof reg.showNotification === 'function' ? reg : null;
  } catch {
    return null;
  }
}

/**
 * Raise one browser notification. Returns the `Notification` when one was
 * actually constructed, `null` in every degraded case — which is what the
 * tests assert on, because a real OS toast cannot be read back from a page.
 */
export function showBrowserNotification(
  opts: BrowserNotifyOptions,
  userId?: number | null,
): Notification | null {
  if (!canShowBrowserNotification(userId)) return null;
  const { title, options } = brandedNotification(opts);
  try {
    const n = new window.Notification(title, options);
    if (opts.onClick) {
      n.onclick = () => {
        try {
          window.focus();
        } catch {
          /* a tab that cannot focus itself still navigates below */
        }
        opts.onClick?.();
        n.close();
      };
    }
    return n;
  } catch {
    // Android Chrome throws "Illegal constructor" — only a service worker may
    // notify there. `raiseBrowserNotification` is the path that covers those
    // devices; this one answers null so the tests can read the degraded case,
    // which a real OS toast never lets them read.
    return null;
  }
}

/**
 * Raise a notification by whichever route this browser actually has.
 *
 * ⚠⚠ The window path is tried FIRST on purpose, even though the worker's
 * looks better when the app is installed: a toast constructed here keeps its
 * `onClick`, and that callback is what marks the row read and navigates
 * INSIDE the running SPA. The worker's click can only open an address, which
 * means a full page load and a row that stays unread. So the worker is the
 * fallback, and the case it covers is real rather than theoretical: on
 * Android Chrome the constructor throws and without this every filex
 * notification on every phone was silently dropped.
 *
 * Returns which route ran, for the tests and for nobody else.
 */
export async function raiseBrowserNotification(
  opts: BrowserNotifyOptions,
  userId?: number | null,
): Promise<'window' | 'sw' | 'none'> {
  if (!canShowBrowserNotification(userId)) return 'none';
  if (showBrowserNotification(opts, userId)) return 'window';
  const reg = await swRegistration();
  if (!reg) return 'none';
  const { title, options } = brandedNotification(opts);
  try {
    await reg.showNotification(title, options);
    return 'sw';
  } catch {
    return 'none';
  }
}
