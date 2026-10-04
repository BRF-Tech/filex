/**
 * The user settings dialog's contract with whoever opens it.
 *
 * ⚠⚠ ONE dialog (components/UserSettingsDialog.vue) for every surface. It lived
 * in web/src until 2026-09-27, bound to the admin app's pinia stores, its axios
 * client and vue-i18n; the desktop app opened it in the browser because nothing
 * else could draw it (owner, the same day: *"Kullanıcı ayarları uygulamanın
 * İÇİNDE açılsın"*). It moved into this package, and what it needs from a host
 * is written down here instead of being imported from one.
 *
 * Two kinds of thing a host hands over:
 *
 *   1. What the dialog cannot know by itself — who is signed in, what the
 *      server can do, how to reach the account's endpoints, how to say "saved".
 *      Every host gives these.
 *   2. Rows that only make sense on SOME surfaces: the admin app's language
 *      switch and start page, the "open files with one click" preference, the
 *      desktop-app downloads, the browser-notification switch. A host that
 *      leaves one out gets no row for it — which is how the desktop app avoids
 *      drawing a second door to what its own Settings already carries
 *      (language, single/double click) or to what does not exist there (a
 *      start page, a download of itself, the browser's notification API).
 *
 * Pure types plus one transport; no Vue.
 */
import type { EventPossibility } from './webhookEvents';
import type { ExternalServiceStatus } from '../types/FileNode';
import type { PluginActionsResponse } from '../types/Plugins';

/** The signed-in person, as the dialog reads and writes it. */
export interface SettingsUser {
  id?: number;
  email?: string | null;
  username?: string | null;
  display_name?: string | null;
  avatar_url?: string | null;
  totp_enabled?: boolean;
  role?: string | null;
  locale?: string | null;
  timezone?: string | null;
}

/** `GET /api/files/quota/me`. */
export interface SettingsQuota {
  used_bytes: number;
  quota_bytes: number;
  percent_used: number;
  unlimited: boolean;
}

/** `GET/PATCH /api/notifications/settings` — the person's own switches. */
export interface SettingsNotificationPrefs {
  in_app_enabled: boolean;
  muted_events: string[];
}

/** What the server says it can do — the fields the dialog reads. ⚠ Not
 *  `account_admin`: that one is the account's role, which the host gives as
 *  `isAdmin`, and the server publishes nothing of the kind. */
export interface SettingsCapabilities extends Omit<EventPossibility, 'account_admin'> {
  version?: string;
  caller_admin?: boolean;
  demo_mode?: boolean;
  /** 0.51 - the document server (Default apps: whether a choice of ONLYOFFICE
   *  for a .csv is available, lib/serviceGate onlyOfficeUsable). */
  onlyoffice_url?: string | null;
  external?: { onlyoffice?: ExternalServiceStatus };
}

/** The account's own endpoints — every one of them open to every signed-in
 *  account (backend routes.go, "authenticated user routes"). */
export interface UserSettingsApi {
  updateProfile(patch: Partial<SettingsUser>): Promise<SettingsUser>;
  changePassword(current: string, next: string): Promise<void>;
  enrollTotp(): Promise<{ secret: string; qr_svg: string; recovery_codes?: string[] }>;
  verifyTotp(code: string): Promise<void>;
  disableTotp(code: string): Promise<void>;
  quota(): Promise<SettingsQuota>;
  notificationSettings(): Promise<SettingsNotificationPrefs | null>;
  updateNotificationSettings(prefs: SettingsNotificationPrefs): Promise<SettingsNotificationPrefs | null>;
  /**
   * 0.50 - `GET /api/files/plugins/actions`: the app interfaces that open
   * files and the administrator's open rules, so Default apps can say which
   * of the person's choices are still available. Absent: the pane lists the
   * choices without judging them.
   */
  pluginActions?(): Promise<PluginActionsResponse>;
}

export type SettingsThemeMode = 'light' | 'auto' | 'dark';
export type BrowserNotifyPermission = 'granted' | 'denied' | 'default' | 'unsupported';

export interface UserSettingsHost {
  /** The language on screen — any offered code, a language pack's included. */
  readonly locale: string;
  readonly user: SettingsUser | null;
  /** The account's role is administrator — of its tenant, of the supertenant or
   *  of a single-tenant install. ⚠ Not `capabilities.caller_admin`, which is
   *  the narrower "may set the INSTANCE up" (the supertenant's alone on a
   *  multi-tenant install). It draws the account's badge, and decides who is
   *  offered the switch for a new encryption request (lib/webhookEvents). */
  readonly isAdmin: boolean;
  /** The demo account: nothing it changes may be saved. */
  readonly demoReadOnly: boolean;
  readonly capabilities: SettingsCapabilities | null;
  readonly api: UserSettingsApi;
  /** The server's answer after a profile write replaces the person. */
  setUser(user: SettingsUser): void;
  toast(kind: 'success' | 'error' | 'warn', message: string): void;
  /** A failed request, said for a person. */
  errorText(err: unknown, fallback: string): string;
  /** This person's time zone on this surface (lib/timezone accountZoneControl). */
  readonly zone: { get(): string; set(tz: string): void };
  /** Light / automatic / dark — whichever store this surface paints from. */
  readonly mode: { get(): SettingsThemeMode; set(mode: SettingsThemeMode): void };

  /* ── rows only some surfaces have (absent → not drawn) ─────────────────── */

  /** The interface language (the admin app). The dialog also writes it to the
   *  account, so the next device opens in it. */
  readonly language?: { pick(code: string): void };
  /** Where the app lands after sign-in (the admin app's router). */
  readonly startPage?: { readonly options: string[]; get(): string | null; set(page: string): void };
  /** Whether one click or two opens a file. */
  readonly openTrigger?: { get(): 'single' | 'double'; set(v: 'single' | 'double'): void };
  /** Where to download the desktop app for this machine. */
  readonly desktopApp?: {
    readonly platform: string | null;
    readonly platformLabel: string;
    /** `other`: the same file for the other processor (x64 / arm64), drawn
     *  beside the row. The host writes every word, this dialog none. */
    readonly downloads: Array<{
      href: string;
      label: string;
      hint: string;
      arch?: string;
      other?: { arch: string; label: string; href: string };
    }>;
    readonly releasesUrl: string;
  };
  /** The browser's own notifications (the admin app in a browser tab). */
  readonly browserNotifications?: {
    readonly desktopShell: boolean;
    permission(): BrowserNotifyPermission;
    enabled(): boolean;
    setEnabled(on: boolean): void;
    ask(): Promise<BrowserNotifyPermission>;
  };
}

/**
 * The account endpoints over the explorer's own client (`useFileApi(config)
 * .jsonFetch`): its credential, its language, its credentials mode — for a
 * host that hands the explorer a token rather than a session (the desktop app).
 * `base` is the server root (`connectionsBase(config)`).
 *
 * ⚠ The admin app does NOT use this: it passes its own axios client (the one
 * with its CSRF header and its 401 redirect), so a settings save there fails and
 * recovers exactly like every other request it makes.
 */
export function userSettingsApi(
  jsonFetch: <R>(url: string, init?: RequestInit) => Promise<R>,
  base: string,
): UserSettingsApi {
  const root = base.replace(/\/+$/, '');
  const json = (method: string, body?: unknown): RequestInit => ({
    method,
    headers: { 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  return {
    updateProfile: (patch) => jsonFetch<SettingsUser>(`${root}/api/auth/profile`, json('PATCH', patch)),
    changePassword: async (current, next) => {
      await jsonFetch(`${root}/api/auth/password`, json('POST', { current_password: current, new_password: next }));
    },
    enrollTotp: () => jsonFetch(`${root}/api/auth/totp/enroll`, json('POST')),
    verifyTotp: async (code) => {
      await jsonFetch(`${root}/api/auth/totp/verify`, json('POST', { code }));
    },
    disableTotp: async (code) => {
      await jsonFetch(`${root}/api/auth/totp/disable`, json('POST', { code }));
    },
    quota: () => jsonFetch<SettingsQuota>(`${root}/api/files/quota/me`),
    notificationSettings: () => jsonFetch<SettingsNotificationPrefs>(`${root}/api/notifications/settings`),
    updateNotificationSettings: (prefs) =>
      jsonFetch<SettingsNotificationPrefs>(`${root}/api/notifications/settings`, json('PATCH', prefs)),
    pluginActions: () => jsonFetch<PluginActionsResponse>(`${root}/api/files/plugins/actions`),
  };
}
