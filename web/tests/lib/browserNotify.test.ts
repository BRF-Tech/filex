// Browser notifications: the guards, not the toast.
//
// A real OS toast cannot be read back from a page, so what is tested is the
// decision in front of it — which is where every actual bug lives: asking
// without a gesture, notifying when the user said no, and notifying twice on a
// machine that is also showing a native one.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  browserNotifyEnabled,
  browserNotifyPermission,
  canShowBrowserNotification,
  isDesktopShell,
  requestBrowserNotifyPermission,
  setBrowserNotifyEnabled,
  showBrowserNotification,
} from '@/lib/browserNotify';

type Ctor = ReturnType<typeof vi.fn>;

function stubNotification(permission: NotificationPermission, opts: { throws?: boolean } = {}) {
  const calls: Array<{ title: string; options?: NotificationOptions; instance: Record<string, unknown> }> = [];
  const ctor = vi.fn(function (this: Record<string, unknown>, title: string, options?: NotificationOptions) {
    if (opts.throws) throw new TypeError('Illegal constructor');
    this.close = vi.fn();
    calls.push({ title, options, instance: this });
  }) as unknown as Ctor;
  (ctor as unknown as { permission: string }).permission = permission;
  (ctor as unknown as { requestPermission: () => Promise<string> }).requestPermission = vi.fn(async () => permission);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).Notification = ctor;
  return { ctor, calls };
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).Notification;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).filexApp;
  vi.unstubAllGlobals();
});

describe('the guards in front of a toast', () => {
  it('shows nothing when the API is missing', () => {
    expect(browserNotifyPermission()).toBe('unsupported');
    expect(showBrowserNotification({ title: 'x' })).toBeNull();
  });

  it('shows nothing while permission is still "default" — asking is a separate act', () => {
    stubNotification('default');
    expect(canShowBrowserNotification(1)).toBe(false);
    expect(showBrowserNotification({ title: 'x' }, 1)).toBeNull();
  });

  it('shows nothing when the browser said no', () => {
    const { calls } = stubNotification('denied');
    expect(showBrowserNotification({ title: 'x' }, 1)).toBeNull();
    expect(calls).toHaveLength(0);
  });

  it('constructs one when permission is granted and the switch is on', () => {
    const { calls } = stubNotification('granted');
    const n = showBrowserNotification({ title: 'New upload', body: 'report.pdf', tag: 'filex-notification-9' }, 1);
    expect(n).not.toBeNull();
    expect(calls).toHaveLength(1);
    expect(calls[0].title).toBe('New upload');
    expect(calls[0].options?.body).toBe('report.pdf');
    // The tag is what stops one event becoming two toasts.
    expect(calls[0].options?.tag).toBe('filex-notification-9');
  });

  it('the per-user switch turns it off, and is per user', () => {
    stubNotification('granted');
    setBrowserNotifyEnabled(false, 1);
    expect(browserNotifyEnabled(1)).toBe(false);
    expect(showBrowserNotification({ title: 'x' }, 1)).toBeNull();
    // A second account on the same machine kept its own answer.
    expect(browserNotifyEnabled(2)).toBe(true);
    expect(showBrowserNotification({ title: 'x' }, 2)).not.toBeNull();
  });

  it('never fires inside the desktop shell — that one shows a native toast', () => {
    stubNotification('granted');
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (window as any).filexApp = { isDesktop: true };
    expect(isDesktopShell()).toBe(true);
    expect(browserNotifyPermission()).toBe('unsupported');
    expect(showBrowserNotification({ title: 'x' }, 1)).toBeNull();
  });

  it('degrades silently when the constructor throws (Android Chrome)', () => {
    stubNotification('granted', { throws: true });
    expect(() => showBrowserNotification({ title: 'x' }, 1)).not.toThrow();
    expect(showBrowserNotification({ title: 'x' }, 1)).toBeNull();
  });

  it('a click runs the handler and closes the toast', () => {
    const { calls } = stubNotification('granted');
    const onClick = vi.fn();
    showBrowserNotification({ title: 'x', onClick }, 1);
    const inst = calls[0].instance as { onclick: () => void; close: () => void };
    inst.onclick();
    expect(onClick).toHaveBeenCalledOnce();
    expect(inst.close).toHaveBeenCalledOnce();
  });
});

describe('asking for permission', () => {
  it('goes through Notification.requestPermission and remembers that we asked', async () => {
    const { ctor } = stubNotification('granted');
    const res = await requestBrowserNotifyPermission(1);
    expect(res).toBe('granted');
    expect((ctor as unknown as { requestPermission: () => void }).requestPermission).toHaveBeenCalledOnce();
    expect(localStorage.getItem('filex.notify.browserAsked.1')).toBe('1');
  });

  it('answers "unsupported" instead of throwing where there is no API', async () => {
    await expect(requestBrowserNotifyPermission(1)).resolves.toBe('unsupported');
  });
});
