/**
 * Web Push on this device (task #191) - the switch "Push notifications on this
 * device" in the user settings dialog, and what keeps it true afterwards.
 *
 * ⚠⚠ ONE flow, here, for every surface that runs filex's own service worker:
 * asking the browser's permission (from the tap), subscribing with the
 * server's key, telling the server, finding this device in the person's list,
 * turning it off, re-subscribing when the server's key was rotated or the
 * browser dropped the subscription, and forgetting the device when the person
 * signs out. A host hands over the two things only it has - its service
 * worker and its transport (`WebPushEnv`, `WebPushApi`) - and nothing else.
 * Where there is no filex worker (an explorer embedded in another site, whose
 * workers are that site's) or the platform notifies on its own (the desktop
 * app raises native notifications from its tray even with the window closed),
 * the host gives no `webPush` row and the dialog draws none.
 *
 * What is pushed, and when, is the server's: a push is one more reader of the
 * person's bell (backend internal/notify push.go) - the same kinds, the same
 * mutes, the same digest. Nothing about it is decided here.
 *
 * ⚠ iPhone and iPad: only the web app added to the Home Screen (iOS / iPadOS
 * 16.4 and later) has `PushManager`; a Safari tab has none, and the dialog
 * says where to look instead. The permission is asked FIRST in `enable()`,
 * before anything is awaited, because Safari asks only from the tap itself.
 * Every push shows a notification (`userVisibleOnly`) - the worker always
 * raises one (web/public/notify-sw.js), which iOS requires.
 *
 * Pure TypeScript, no Vue: the dialog drives it, and the web app calls it on
 * start (reconcile) and on sign-out (forgetDevice).
 */

/** One of the person's devices, as GET /api/notifications/push lists it. */
export interface WebPushDevice {
  id: number;
  label: string;
  /** Hex SHA-256 of the device's endpoint - how this browser finds itself. */
  endpoint_hash: string;
  /** The push service's host (fcm.googleapis.com, web.push.apple.com …). */
  service?: string;
  created_at: string;
  last_ok_at?: string | null;
}

/** GET /api/notifications/push. */
export interface WebPushStatus {
  available: boolean;
  /** Why not: `disabled`, `no_secret_key`, `key_unreadable`, `error`. */
  reason?: string;
  /** The server's key (base64url): what a browser subscribes with. */
  public_key?: string;
  devices: WebPushDevice[];
}

/** What a browser hands the server: PushSubscription.toJSON() and a name. */
export interface WebPushSubscriptionBody {
  endpoint: string;
  keys: { p256dh: string; auth: string };
  label: string;
}

/** The account's push endpoints (backend handlers/notification_push.go). */
export interface WebPushApi {
  status(): Promise<WebPushStatus>;
  subscribe(body: WebPushSubscriptionBody): Promise<WebPushDevice>;
  /** This browser, by its endpoint: push turned off on it, or signing out. */
  forget(endpoint: string): Promise<void>;
  /** One of the person's devices, by id. */
  remove(id: number): Promise<void>;
  test(): Promise<{ sent: number; failed: number }>;
}

export type WebPushPermission = 'granted' | 'denied' | 'default' | 'unsupported';

/** What the browser's Notification API gives (`window.Notification`). */
export interface WebPushNotificationApi {
  readonly permission: NotificationPermission;
  requestPermission(): Promise<NotificationPermission> | void;
}

/** What only the host has. */
export interface WebPushEnv {
  /** The person signed in (the "on for this device" note is kept per person). */
  userId: number | null;
  /** The host's own service worker, the one the pushes arrive at. */
  registration(): Promise<ServiceWorkerRegistration | null>;
  /** The browser's Notification API. Default: `window.Notification`. */
  notification?: WebPushNotificationApi | null;
  /** Whether this browser has Web Push at all. Default: `PushManager` exists. */
  supported?: () => boolean;
  /** Where the note is kept. Default: localStorage. */
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | null;
  /** The browser, for the device's label. Default: navigator.userAgent. */
  userAgent?: string;
  /** Hex SHA-256 of a text. Default: crypto.subtle. */
  sha256?: (text: string) => Promise<string>;
}

/** What the dialog draws. */
export interface WebPushState {
  /** This browser can receive pushes at all. */
  supported: boolean;
  permission: WebPushPermission;
  /** The server's answer; null when it could not be asked. */
  status: WebPushStatus | null;
  /** This browser receives pushes for this person. */
  on: boolean;
  /** This browser in `status.devices`, by id. */
  thisDevice: number | null;
}

export interface WebPushController {
  state(): Promise<WebPushState>;
  /** ⚠ From the tap and from nowhere else: it may ask for the permission. */
  enable(): Promise<WebPushState>;
  disable(): Promise<WebPushState>;
  remove(id: number): Promise<WebPushState>;
  test(): Promise<{ sent: number; failed: number }>;
  /** On every start of the app: keep this device's subscription true. */
  reconcile(): Promise<void>;
  /** Before signing out: this browser stops receiving the person's pushes. */
  forgetDevice(): Promise<void>;
}

/** The note that the person turned push on for this browser, per person. */
export const WEB_PUSH_NOTE_KEY = 'filex.notify.push';

/** base64url (padded or not) to bytes - what `applicationServerKey` takes. */
export function base64UrlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/').replace(/=+$/, '');
  const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** The browsers and systems a label names, the more specific first. */
const BROWSERS: Array<[RegExp, string]> = [
  [/Edg(e|A|iOS)?\//, 'Edge'],
  [/OPR\/|Opera/, 'Opera'],
  [/SamsungBrowser\//, 'Samsung Internet'],
  [/Firefox\/|FxiOS\//, 'Firefox'],
  [/Chrome\/|CriOS\//, 'Chrome'],
  [/Safari\//, 'Safari'],
];
const SYSTEMS: Array<[RegExp, string]> = [
  [/iPhone/, 'iPhone'],
  [/iPad/, 'iPad'],
  [/Android/, 'Android'],
  [/CrOS/, 'ChromeOS'],
  [/Windows/, 'Windows'],
  [/Mac OS X|Macintosh/, 'macOS'],
  [/Linux/, 'Linux'],
];

/**
 * What this device is called in the person's list: "Chrome · Android",
 * "Safari · iPhone" - names, so the same in every language. The person sees it
 * beside a Remove button, so it only has to tell their own devices apart.
 */
export function deviceLabel(ua: string): string {
  const first = (list: Array<[RegExp, string]>) => list.find(([re]) => re.test(ua))?.[1] ?? '';
  return [first(BROWSERS), first(SYSTEMS)].filter(Boolean).join(' · ');
}

async function subtleSha256(text: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

function defaultStorage(): Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

/** Is the subscription made with this key? */
function sameKey(sub: PushSubscription, key: Uint8Array<ArrayBuffer>): boolean {
  const k = sub.options?.applicationServerKey;
  if (!k) return false;
  const have = new Uint8Array(k as ArrayBuffer);
  if (have.length !== key.length) return false;
  for (let i = 0; i < key.length; i++) if (have[i] !== key[i]) return false;
  return true;
}

function bodyOf(sub: PushSubscription, label: string): WebPushSubscriptionBody {
  const json = sub.toJSON();
  return {
    endpoint: json.endpoint ?? sub.endpoint,
    keys: { p256dh: json.keys?.p256dh ?? '', auth: json.keys?.auth ?? '' },
    label,
  };
}

/** The worker, once it is active: a subscription needs an active one. */
async function activeWorker(reg: ServiceWorkerRegistration | null): Promise<ServiceWorkerRegistration | null> {
  if (!reg || !reg.pushManager) return null;
  if (reg.active) return reg;
  const w = reg.installing ?? reg.waiting;
  if (!w) return null;
  await new Promise<void>((resolve) => {
    const done = setTimeout(resolve, 10_000);
    w.addEventListener('statechange', () => {
      if (w.state === 'activated') {
        clearTimeout(done);
        resolve();
      }
    });
  });
  return reg.active ? reg : null;
}

export function createWebPush(api: WebPushApi, env: WebPushEnv): WebPushController {
  const storage = env.storage === undefined ? defaultStorage() : env.storage;
  const noteKey = env.userId ? `${WEB_PUSH_NOTE_KEY}.${env.userId}` : '';
  const sha256 = env.sha256 ?? subtleSha256;
  const ua = env.userAgent ?? (typeof navigator === 'undefined' ? '' : navigator.userAgent ?? '');
  let lastStatus: WebPushStatus | null = null;

  const notificationApi = (): WebPushNotificationApi | null => {
    if (env.notification !== undefined) return env.notification;
    return typeof window !== 'undefined' && 'Notification' in window ? window.Notification : null;
  };
  const supported = (): boolean => {
    if (env.supported) return env.supported();
    return (
      typeof window !== 'undefined' &&
      'PushManager' in window &&
      typeof navigator !== 'undefined' &&
      'serviceWorker' in navigator &&
      notificationApi() !== null
    );
  };
  const permission = (): WebPushPermission => {
    const n = notificationApi();
    if (!n || !supported()) return 'unsupported';
    return (n.permission as WebPushPermission) ?? 'default';
  };
  const noted = (): boolean => {
    if (!noteKey || !storage) return false;
    try {
      return storage.getItem(noteKey) === '1';
    } catch {
      return false;
    }
  };
  const note = (on: boolean): void => {
    if (!noteKey || !storage) return;
    try {
      if (on) storage.setItem(noteKey, '1');
      else storage.removeItem(noteKey);
    } catch {
      /* a convenience: never let storage break the switch */
    }
  };
  const fetchStatus = async (): Promise<WebPushStatus | null> => {
    try {
      lastStatus = await api.status();
    } catch {
      lastStatus = null;
    }
    return lastStatus;
  };
  const currentSubscription = async (): Promise<PushSubscription | null> => {
    if (!supported()) return null;
    try {
      const reg = await env.registration();
      return reg?.pushManager ? await reg.pushManager.getSubscription() : null;
    } catch {
      return null;
    }
  };
  /** Subscribe (or keep the subscription that has the right key) and tell the server. */
  const subscribeWith = async (publicKey: string): Promise<void> => {
    const reg = await activeWorker(await env.registration());
    if (!reg) throw new Error('no service worker');
    const key = base64UrlToBytes(publicKey);
    let sub = await reg.pushManager.getSubscription();
    if (sub && !sameKey(sub, key)) {
      await sub.unsubscribe().catch(() => false);
      sub = null;
    }
    if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
    await api.subscribe(bodyOf(sub, deviceLabel(ua)));
  };

  async function state(): Promise<WebPushState> {
    const status = await fetchStatus();
    let thisDevice: number | null = null;
    const sub = status ? await currentSubscription() : null;
    if (sub && status) {
      try {
        const hash = await sha256(sub.endpoint);
        thisDevice = status.devices.find((d) => d.endpoint_hash === hash)?.id ?? null;
      } catch {
        thisDevice = null;
      }
    }
    const perm = permission();
    return { supported: supported(), permission: perm, status, on: thisDevice !== null && perm === 'granted', thisDevice };
  }

  async function enable(): Promise<WebPushState> {
    const n = notificationApi();
    if (!n || !supported()) return state();
    // ⚠ FIRST, before anything is awaited: Safari asks only from the tap.
    let perm = n.permission as WebPushPermission;
    if (perm === 'default') {
      // Safari before 16 answers through a callback and returns nothing;
      // the permission is read back then.
      const asked: unknown = await Promise.resolve(n.requestPermission());
      perm = (typeof asked === 'string' ? asked : n.permission) as WebPushPermission;
    }
    if (perm !== 'granted') return state();
    const status = lastStatus ?? (await fetchStatus());
    if (!status?.available || !status.public_key) return state();
    await subscribeWith(status.public_key);
    note(true);
    return state();
  }

  async function disable(): Promise<WebPushState> {
    note(false);
    const sub = await currentSubscription();
    if (sub) {
      await api.forget(sub.endpoint).catch(() => undefined);
      await sub.unsubscribe().catch(() => false);
    }
    return state();
  }

  async function remove(id: number): Promise<WebPushState> {
    const before = await state();
    await api.remove(id);
    if (before.thisDevice === id) {
      note(false);
      const sub = await currentSubscription();
      await sub?.unsubscribe().catch(() => false);
    }
    return state();
  }

  async function reconcile(): Promise<void> {
    if (!noted() || permission() !== 'granted') return;
    const status = await fetchStatus();
    if (!status?.available || !status.public_key) return;
    try {
      const sub = await currentSubscription();
      if (sub && sameKey(sub, base64UrlToBytes(status.public_key))) {
        const hash = await sha256(sub.endpoint);
        if (status.devices.some((d) => d.endpoint_hash === hash)) return;
      }
      // The browser dropped it, the server forgot it (a rotation, a push
      // service that said it is gone), or the key changed: subscribe again.
      await subscribeWith(status.public_key);
    } catch {
      /* the next start tries again */
    }
  }

  async function forgetDevice(): Promise<void> {
    const sub = await currentSubscription();
    if (!sub) return;
    await api.forget(sub.endpoint).catch(() => undefined);
    await sub.unsubscribe().catch(() => false);
  }

  return {
    state,
    enable,
    disable,
    remove,
    test: () => api.test(),
    reconcile,
    forgetDevice,
  };
}
