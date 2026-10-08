// Push notifications on this device (#191) - the web app's binding of core's
// one flow (`@brftech/filex-core` lib/webPush).
//
// ⚠ Not a second flow. What this file gives the core is what only the web app
// has: its service worker (the one notify-sw.js runs in, found by its own
// scope - lib/browserNotify swRegistration) and its transport (the axios
// client, with its CSRF header and its 401 handling). Asking the permission,
// subscribing, finding this device, re-subscribing and forgetting are core's.
//
// ⚠ Never inside the desktop app: its main process raises native
// notifications even with the window closed, so a push there would be a
// second notification of the same row (and Electron has no push service).

import { createWebPush, type WebPushApi, type WebPushController } from '@brftech/filex-core';

import { NotificationsApi } from '@/api/notifications';
import { isDesktopShell, swRegistration } from '@/lib/browserNotify';

const transport: WebPushApi = {
  status: () => NotificationsApi.pushStatus(),
  subscribe: (body) => NotificationsApi.pushSubscribe(body),
  forget: (endpoint) => NotificationsApi.pushForget(endpoint),
  remove: (id) => NotificationsApi.pushRemove(id),
  test: () => NotificationsApi.pushTest(),
};

/** The flow for this person in this browser; null inside the desktop app. */
export function webPushFor(userId: number | null | undefined): WebPushController | null {
  if (isDesktopShell()) return null;
  return createWebPush(transport, { userId: userId ?? null, registration: swRegistration });
}

/**
 * On every start of a signed-in app: this device's subscription made true
 * again where the person turned push on here (a rotated key, a browser that
 * dropped it). Never throws, and asks nothing where they did not.
 */
export async function reconcileWebPush(userId: number | null | undefined): Promise<void> {
  try {
    await webPushFor(userId)?.reconcile();
  } catch {
    /* the next start tries again */
  }
}

/** How long a sign-out waits for this browser to be forgotten. */
const FORGET_WAIT_MS = 3000;

/**
 * Before signing out: this browser stops receiving the person's pushes (the
 * server forgets it, the browser unsubscribes). Bounded, so a slow push
 * service never holds a sign-out up.
 */
export async function forgetWebPush(userId: number | null | undefined): Promise<void> {
  const ctl = webPushFor(userId);
  if (!ctl) return;
  await Promise.race([
    ctl.forgetDevice().catch(() => undefined),
    new Promise<void>((resolve) => setTimeout(resolve, FORGET_WAIT_MS)),
  ]);
}
