/**
 * prefs — the look-and-language preferences, kept on the ACCOUNT.
 *
 * ⚠⚠ Why this module exists: a palette picked in one browser was not there
 * in the next one. Theme, palette, density and language were each stored in
 * `localStorage` and nowhere else, so every device — and every private
 * window, and every cleared cache — started the product over with somebody
 * else's defaults. They are a PERSON's preferences, not a machine's.
 *
 * So the answer lives on the account: `GET /api/me/prefs?surface=web` on
 * boot, `PUT /api/me/prefs?surface=web` on every change, debounced.
 *
 * ⚠ `localStorage` stays, and stays for exactly ONE job: the first paint.
 * The fetch cannot beat the first frame, so the mirror is what stops the
 * window flashing the stock palette on the way to the chosen one. It is a
 * CACHE of the account's answer, never the answer — when the server's
 * values land they overwrite it.
 *
 * ⚠⚠ ONE module, several surfaces. The web app asks for `surface=web` and
 * the desktop app for `surface=desktop` — same code, same keys, same
 * debounce; only the query differs, because a person may legitimately want
 * a dense list on a laptop and a roomy one on a wall screen. There is no
 * per-surface BRANCH anywhere in here, and there must not be one: a
 * behaviour that exists on one surface only is the bug this product keeps
 * having to un-write.
 */

/** The preferences an account carries for one surface. */
export interface UiPrefs {
  /** `light` | `dark` | `auto`. */
  theme?: string;
  /** A palette id from `lib/themes` (`default`, `night-blue`, …). */
  palette?: string;
  /** `comfortable` | `compact`. */
  density?: string;
  /** A language tag the interface offers (`lib/uiLocales`). */
  locale?: string;
  /**
   * The order this person put their storages in, in the navigation panel
   * (GitHub #57) — a JSON list of storage keys (`lib/storageOrder`). Absent or
   * empty = the host's own order, which is what everybody who never reordered
   * keeps seeing.
   *
   * ⚠ A string, like every other value here: `sanitize` keeps strings only, so
   * a raw array would be dropped on the way in and erased on the next write.
   */
  storageOrder?: string;
  /**
   * Set (to `done`) once the first-use tour has been OFFERED to this person
   * (`lib/tour`). Not a look: it has no first-paint mirror of its own, and it
   * only ever goes one way — nothing in the product clears it.
   */
  tour?: string;
}

export type PrefKey = keyof UiPrefs;

/** The keys that paint the window — each has a first-paint mirror (below). */
export type LookKey = Exclude<PrefKey, 'tour'>;

/**
 * Every key the account document carries.
 *
 * ⚠⚠ A key the client does not list here is DROPPED on the next write:
 * `sanitize` keeps only these, and `PUT` replaces the whole document. So a
 * preference that is not a look (the tour) still has to be named here, or the
 * first palette change after it was stored would erase it.
 */
export const PREF_KEYS: readonly PrefKey[] = ['theme', 'palette', 'density', 'locale', 'storageOrder', 'tour'];

/**
 * The keys with a first-paint mirror in `localStorage` — what the window is
 * painted with, nothing else.
 *
 * ⚠ `storageOrder` is one: the navigation panel draws the storages on the very
 * first frame, and without a mirror a person who reordered them would watch
 * the list jump from the host's order to theirs once the account answered. It
 * is also the ONLY place the order lives for an embed that never wires the
 * account document (`prefsConfigured()` false). The tour is not: it is a
 * one-way flag with a browser key of its own (`lib/tour`).
 */
export const LOOK_KEYS: readonly LookKey[] = ['theme', 'palette', 'density', 'locale', 'storageOrder'];

/**
 * The localStorage mirror's key per preference.
 *
 * ⚠⚠ These are the names the surfaces ALREADY read at first paint
 * (`web/src/lib/theme`, `lib/themes.THEME_LS_KEY`, `web/src/lib/density`,
 * `web/src/i18n`). Inventing new ones here would have left four first-paint
 * readers looking at keys nobody writes — the preference would work, and
 * the window would flash on every load, which is the failure this module is
 * supposed to remove.
 */
export const PREF_LS_KEYS: Record<LookKey, string> = {
  theme: 'filex.theme',
  palette: 'filex.palette',
  density: 'filex.density',
  locale: 'filex.locale',
  // New in 0.45.2 (#57); no earlier reader to stay compatible with.
  storageOrder: 'filex.storageOrder',
};

/** How long a change waits for the next one before it is sent. */
export const PREFS_PUT_DEBOUNCE_MS = 400;

/* ── is anybody signed in? ────────────────────────────────────────────── */

/**
 * ⚠⚠ WITH NO SESSION, NOTHING PERSONAL PAINTS. The owner's rule, verbatim:
 * "logoutluyken zaten seçtiğim tema değil, şirketin default, yoksa filex'in
 * default teması gelecek; tarayıcı gece modundaysa gece modunda olacak ya da
 * light mode."
 *
 * So a signed-out window wears the INSTANCE's look (`ui.default_theme`, or
 * filex's own stock palette when the operator has not set one) and follows the
 * BROWSER's `prefers-color-scheme`. The palette and the light/dark mode became
 * per-PERSON settings in v3 (migration 00047, `GET/PUT /api/me/prefs`), and a
 * person is exactly what a signed-out window does not have.
 *
 * ⚠ Measured before it was changed (2026-09-21, vitest against the shipped
 * `packages/core/dist`): with `filex.palette` = `night` and no session at all,
 * `useThemeState().themeId` came back `"night"` and `filex.thememode` = `dark`
 * came back `"dark"` — both read at MODULE LOAD, long before anything could
 * know whether a session existed. Worse, `instanceThemes.hasOwnChoice()` saw
 * the same key and suppressed the operator's own default entirely, so an
 * instance with a brand theme showed the LAST PERSON WHO SIGNED IN HERE their
 * colours on its sign-in page — and showed the next person at that shared
 * machine the previous one's taste.
 *
 * ⚠⚠ WHY A STORED HINT AND NOT THE REAL ANSWER. The real answer is
 * `/api/auth/me`, and it cannot be had at the first paint: the session cookie
 * is HttpOnly (handlers/auth.go) so JavaScript cannot see it, and the fetch
 * resolves long after the window has painted. A wrong answer is a visible bug
 * in BOTH directions — read the personal palette with no session and the leak
 * above is back; ignore it WITH a session and every single load flashes the
 * instance default on the way to the person's own palette. So this is a guess
 * that is right in the overwhelming case (this browser's last load answered
 * the same question) and is corrected within one round trip by
 * `rememberSession`, which every entry path reaches through `/api/auth/me`.
 */
export const SESSION_LS_KEY = 'filex.session';

/**
 * Set for THIS DOCUMENT only, never persisted: a public link (`/s/`, `/d/`)
 * cannot carry a session whatever the browser remembers.
 *
 * ⚠ Persisting it would be the bug. The same browser's admin tab reads the
 * same localStorage, so writing "no session" from a share link would make that
 * tab flash the instance default on its next load — the very flash the mirror
 * exists to prevent.
 */
let sealed = false;

function readSessionHint(): boolean {
  try {
    return localStorage.getItem(SESSION_LS_KEY) === '1';
  } catch {
    // Private window / blocked site data. "No session" is the safe answer: it
    // shows the instance's look, never somebody else's.
    return false;
  }
}

/**
 * Is a person signed in here? The synchronous, first-paint answer.
 *
 * ⚠ `true` for a host that has not declared `sessionAware` — see that
 * option. "Nobody told us about sessions" means "this explorer is inside
 * something that already dealt with it", not "nobody is here".
 */
export function hasSession(): boolean {
  if (sealed) return false;
  if (!cfg.sessionAware) return true;
  return readSessionHint();
}

/**
 * `/api/auth/me` has answered. Remember it for this browser's next first paint.
 *
 * ⚠ A hint and nothing more — it is not a credential, it grants nothing, and
 * every authenticated request still carries the HttpOnly cookie. Forging it
 * buys an attacker the wrong palette.
 */
export function rememberSession(present: boolean): void {
  try {
    if (present) localStorage.setItem(SESSION_LS_KEY, '1');
    else localStorage.removeItem(SESSION_LS_KEY);
  } catch {
    /* quota / private mode — `hasSession()` then answers false, which is the
       safe direction */
  }
  if (present) sealed = false;
}

/** This document is a public link: the answer is no, and it is not remembered. */
export function sealSessionless(): void {
  sealed = true;
}

/**
 * Every localStorage key that mirrors a PERSON and must not outlive their
 * session.
 *
 * ⚠⚠ An allow-list, never `localStorage.clear()`. Most of what this product
 * keeps in a browser is a fact about the MACHINE or the INSTALL and survives a
 * sign-out on purpose:
 *   • `filex.instancetheme` — the operator's default, a PUBLIC fact identical
 *     for everybody. Clearing it would put back the very flash it was added to
 *     remove, on the sign-in page, for nothing.
 *   • `filex.installPrompt.dismissed`, `filex.notify.browserAsked`,
 *     `filex.tourDone` — "this browser has already been asked". Clearing them
 *     re-nags whoever sits down next, which is worse than useless. (The tour
 *     is ALSO recorded on the account now — `lib/tour` — and the two are read
 *     as one answer: seen here OR seen by this person anywhere.)
 *   • `filex.sidenav`, `filex.inspector`, `filex.tabs`, `filex.showHidden`,
 *     `filex.startpage`, `filex.shortcuts`, `filex.presence.expanded` — window
 *     furniture and this machine's ergonomics, not a look somebody wears.
 *   • `filex.openTrigger`, `filex.desktopHandoff` — in-flight machinery for a
 *     hand-off that may be mid-sign-in right now.
 * `filex.bearer` is already dropped by the auth store, and it lives in
 * sessionStorage besides.
 */
const personalMirrors = new Set<string>(Object.values(PREF_LS_KEYS));

/**
 * A module with a per-person key of its own adds it here.
 *
 * ⚠ `lib/themes` registers `filex.thememode` this way rather than having its
 * name repeated in the list above — a second copy of a storage key is exactly
 * the drift `lib/density`' header warns about, where a rename on one side
 * leaves the other writing to a key nobody reads.
 */
export function registerPersonalMirror(key: string): void {
  personalMirrors.add(key);
}

/**
 * The session ended: this browser forgets everything it cached ABOUT THE
 * PERSON.
 *
 * Owner, 2026-09-21: *"oturum kapanınca temizlersek localstorage'ı tamamız ya
 * o kısımda"* — once somebody has left, their preferences are not something
 * this machine should go on holding.
 *
 * ⚠⚠ It is an "and", not an "instead of". Clearing only covers the moments
 * the app KNOWS about; it cannot cover a cookie that quietly expired, a tab
 * closed without signing out, or a browser whose owner never signs out at all.
 * `hasSession()` is what covers those, by making the keys unreadable rather
 * than absent. Drop either one and the sign-in page goes back to wearing the
 * last person who used this browser.
 *
 * ⚠ The in-memory document goes too, and the queued PUT with it. Leaving
 * `remote` behind would keep `hasOwnChoice()` answering for somebody who has
 * gone; leaving `pending` behind would fire a write with their values a moment
 * after their cookie stopped existing.
 */
export function forgetPersonalPrefs(): void {
  rememberSession(false);
  for (const key of personalMirrors) {
    try {
      localStorage.removeItem(key);
    } catch {
      /* blocked site data — `hasSession()` already makes the key unreadable */
    }
  }
  remote = {};
  hydrated = false;
  pending = {};
  if (timer !== null) clearTimeout(timer);
  timer = null;
  // The answer to "whose preferences?" is "nobody's": nothing more is coming
  // for this document until somebody signs in (and `hydratePrefs` runs again).
  settle();
}

export interface PrefsConfig {
  /** `web`, `desktop`, or whatever a new surface calls itself. */
  surface: string;
  /** API origin; empty = same origin. */
  base?: string;
  /** Per-call auth, for a host that does not ride on cookies (the desktop app). */
  headers?: () => Promise<Record<string, string>> | Record<string, string>;
  fetchImpl?: typeof fetch;
  /**
   * ⚠⚠ Does this host have a SIGNED-OUT state at all?
   *
   * The filex SPA does: it draws a sign-in page, and the rule this flag turns
   * on is the owner's (`SESSION_LS_KEY` above) — with nobody signed in, the
   * palette and the light/dark mode in this browser belong to somebody else
   * and are not read.
   *
   * Most hosts do NOT. An explorer embedded in work.example.com or in the fishapp
   * is mounted inside a page the host already authenticated; the desktop app
   * is past its pairing screen. They never call `rememberSession`, they have
   * no `/api/auth/me` to call, and gating them would mean the palette a person
   * picked inside the embed silently stopped applying on the next load — a
   * regression, not a fix.
   *
   * ⚠ This is a fact about the HOST, declared once beside `surface` and
   * `base`, not a branch on which surface is running: there is exactly one
   * mechanism and every caller goes through it. Default OFF, so a host that
   * says nothing keeps the behaviour it has always had.
   */
  sessionAware?: boolean;
}

let cfg: PrefsConfig = { surface: 'web' };
let remote: UiPrefs = {};
/** The server has answered at least once. */
let hydrated = false;
/** A host said where the account's document lives (`configurePrefs`). */
let configured = false;
/**
 * The question "whose preferences are these?" has been answered for this
 * document: the server's copy arrived, or the fetch gave up, or the session
 * turned out to be nobody's (`forgetPersonalPrefs`). Something that must not
 * decide before the account has spoken (the first-use tour) waits for this,
 * not for `hydrated` — a fetch that failed never hydrates.
 */
let settled = false;

const listeners = new Set<(p: UiPrefs) => void>();
const settleWaiters = new Set<() => void>();

export function configurePrefs(next: PrefsConfig): void {
  cfg = { ...next };
  configured = true;
}

/**
 * Did the host wire the account's document at all? An explorer embedded in
 * another product never calls `configurePrefs`, and for it there is no account
 * copy to wait for — only this browser's.
 */
export function prefsConfigured(): boolean {
  return configured;
}

/** See `settled`. */
export function prefsSettled(): boolean {
  return settled;
}

/** Be told once the account's answer is in (or known not to be coming). Returns the unsubscribe. */
export function onPrefsSettled(fn: () => void): () => void {
  if (settled) {
    fn();
    return () => {};
  }
  settleWaiters.add(fn);
  return () => settleWaiters.delete(fn);
}

function settle(): void {
  settled = true;
  const waiting = [...settleWaiters];
  settleWaiters.clear();
  for (const fn of waiting) {
    try {
      fn();
    } catch {
      /* one waiter's mistake is not another's */
    }
  }
}

/** The preferences as they stand — the server's answer once it has landed. */
export function currentPrefs(): UiPrefs {
  return { ...remote };
}

export function prefsHydrated(): boolean {
  return hydrated;
}

/** Be told when the account's answer arrives or changes. Returns the unsubscribe. */
export function onPrefs(fn: (p: UiPrefs) => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function announce(): void {
  const snapshot = currentPrefs();
  for (const fn of listeners) {
    try {
      fn(snapshot);
    } catch {
      /* one listener's mistake is not another's */
    }
  }
}

/* ── the first-paint mirror ───────────────────────────────────────────── */

/** What this DEVICE last saw — read before the network, never trusted after it. */
export function localPref(key: LookKey): string {
  try {
    return localStorage.getItem(PREF_LS_KEYS[key]) ?? '';
  } catch {
    // Private window, blocked site data: the caller's own default is the
    // answer, and a preference is never worth an exception.
    return '';
  }
}

export function setLocalPref(key: LookKey, value: string): void {
  try {
    if (value) localStorage.setItem(PREF_LS_KEYS[key], value);
    else localStorage.removeItem(PREF_LS_KEYS[key]);
  } catch {
    /* see above */
  }
}

/** Everything the mirror holds, for the boot that has no server answer yet. */
export function localPrefs(): UiPrefs {
  const out: UiPrefs = {};
  for (const k of LOOK_KEYS) {
    const v = localPref(k);
    if (v) out[k] = v;
  }
  return out;
}

/* ── the account's copy ───────────────────────────────────────────────── */

function url(): string {
  const base = (cfg.base ?? '').replace(/\/+$/, '');
  return `${base}/api/me/prefs?surface=${encodeURIComponent(cfg.surface)}`;
}

async function headers(withBody: boolean): Promise<Record<string, string>> {
  const h: Record<string, string> = { Accept: 'application/json' };
  if (withBody) h['Content-Type'] = 'application/json';
  // ⚠ awaited — the desktop app's token is a FUNCTION that resolves
  // asynchronously, and spreading the promise sends no Authorization at all.
  const extra = cfg.headers ? await cfg.headers() : {};
  return { ...h, ...extra };
}

/** Only the keys this client knows, only as strings — a server that grows another is ignored, not obeyed. */
function sanitize(raw: unknown): UiPrefs {
  const src = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  const body = (src.prefs && typeof src.prefs === 'object' ? src.prefs : src) as Record<string, unknown>;
  const out: UiPrefs = {};
  for (const k of PREF_KEYS) {
    const v = body[k];
    if (typeof v === 'string' && v) out[k] = v;
  }
  return out;
}

/**
 * Read the account's preferences.
 *
 * Answers `null` when there is nothing to apply — no session, an older
 * server with no such route, a network that is not there. ⚠ `null` is not an
 * error: the mirror has already painted the window, and a preference is the
 * last thing that should put an error in front of a person.
 */
export async function hydratePrefs(): Promise<UiPrefs | null> {
  const doFetch = cfg.fetchImpl ?? (typeof fetch === 'function' ? fetch : null);
  if (!doFetch) {
    settle();
    return null;
  }
  // ⚠ A new question is being asked. A sign-out settled the last one with
  // "nobody's" (`forgetPersonalPrefs`), and a person who then signs in on the
  // sign-in page — no reload — must not be decided for on that stale answer
  // while their own document is still on its way.
  settled = false;
  try {
    const res = await doFetch(url(), {
      method: 'GET',
      headers: await headers(false),
      credentials: 'same-origin',
      cache: 'no-store',
    });
    if (!res.ok) return null;
    remote = sanitize(await res.json());
    hydrated = true;
    // ⚠⚠ An account that has NEVER stored anything adopts what this browser
    // already had, instead of wiping it. Every one of these preferences lived
    // in localStorage alone until v3, so the first load after the upgrade
    // would otherwise meet an empty document, clear the mirror and hand the
    // person back the stock palette they had chosen their way out of — a
    // silent reset that looks exactly like the bug this module was written to
    // fix. One key already stored is enough to make the ACCOUNT the answer:
    // somebody who cleared a palette on another device has a document, and
    // nothing here resurrects what they cleared.
    if (!Object.keys(remote).length) {
      const mine = localPrefs();
      if (Object.keys(mine).length) {
        remote = mine;
        for (const k of LOOK_KEYS) if (mine[k]) pending[k] = mine[k];
        if (timer !== null) clearTimeout(timer);
        timer = setTimeout(() => {
          inFlight = flush();
        }, PREFS_PUT_DEBOUNCE_MS);
      }
    }
    // The mirror is a cache of THIS answer from here on.
    for (const k of LOOK_KEYS) setLocalPref(k, remote[k] ?? '');
    announce();
    return currentPrefs();
  } catch {
    return null;
  } finally {
    settle();
  }
}

let pending: UiPrefs = {};
let timer: ReturnType<typeof setTimeout> | null = null;
let inFlight: Promise<void> | null = null;

async function flush(): Promise<void> {
  timer = null;
  const changed = pending;
  pending = {};
  if (!Object.keys(changed).length) return;
  // ⚠⚠ The WHOLE document, wrapped in `{prefs: …}`. `PUT /api/me/prefs`
  // REPLACES the stored document (handlers/userprefs.go: "Put replaces the
  // caller's document for one surface") — sending only what changed would
  // drop every other preference the account holds, which is a data loss that
  // looks like "my palette reset itself".
  const body = { prefs: currentPrefs() };
  const doFetch = cfg.fetchImpl ?? (typeof fetch === 'function' ? fetch : null);
  if (!doFetch) return;
  try {
    await doFetch(url(), {
      method: 'PUT',
      headers: await headers(true),
      credentials: 'same-origin',
      body: JSON.stringify(body),
    });
  } catch {
    /* the change is already on screen and in the mirror; a failed write
       means the next device does not see it, not that this one breaks */
  }
}

/**
 * A preference changed: mirror it now, tell the account shortly.
 *
 * ⚠ The mirror is written FIRST and unconditionally. If the PUT fails, this
 * device still opens the way the person left it — which is exactly the
 * behaviour the product had before the account copy existed.
 */
export function savePref(key: PrefKey, value: string): void {
  remote = { ...remote, [key]: value };
  if (key !== 'tour') setLocalPref(key, value);
  pending = { ...pending, [key]: value };
  if (timer !== null) clearTimeout(timer);
  timer = setTimeout(() => {
    inFlight = flush();
  }, PREFS_PUT_DEBOUNCE_MS);
}

/** Send anything still waiting, now (a window closing, a test). */
export async function flushPrefs(): Promise<void> {
  if (timer !== null) {
    clearTimeout(timer);
    inFlight = flush();
  }
  await inFlight;
}

/** Testing seam: forget the session's state. */
export function resetPrefs(): void {
  remote = {};
  hydrated = false;
  // ⚠ The document seal too. It is per-DOCUMENT state and a test file is one
  // document: a spec that opened a public link would otherwise leave every
  // later spec in this file believing it was on one.
  sealed = false;
  pending = {};
  if (timer !== null) clearTimeout(timer);
  timer = null;
  inFlight = null;
  listeners.clear();
  settled = false;
  settleWaiters.clear();
  configured = false;
}
