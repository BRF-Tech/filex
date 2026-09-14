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
  onClick?: () => void;
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
  try {
    const n = new window.Notification(opts.title, {
      body: opts.body,
      tag: opts.tag,
      icon: '/admin/icons/icon.svg',
    });
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
    // notify there. Nothing to do and nothing worth telling the user.
    return null;
  }
}
