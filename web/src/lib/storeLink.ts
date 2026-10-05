/**
 * A store's install link (docs/APP-PLUGINS.md → Installing from a store):
 *
 *   <filex>/admin/store-install#store=<encodeURIComponent(store origin)>&intent=<token>
 *
 * The token is in the FRAGMENT so the browser never sends it to filex's
 * server or writes it into an access log. It is taken off the address bar
 * here, before the router's history is made (router/index.ts): an
 * administrator who is not signed in goes through the sign-in form and comes
 * back to /admin/store-install with the link waiting in this tab's
 * sessionStorage - never in a `?redirect=` query, never in the history.
 *
 * What the link carries is only an ADDRESS and a token: the store, its
 * signature, the trust an administrator gave it and the review decide
 * everything else (views/StoreInstall.vue).
 *
 * ⚠ A SECOND link in a tab that is already on /admin/store-install is a
 * same-document fragment navigation: no page load, so the capture below never
 * runs, and vue-router writes the address back with its fragment. The router
 * takes it there instead (router/index.ts, beforeEach → captureStoreFragment)
 * and the page reads it (storeLinkArrivals) - store fe review #2.
 */
import { ref } from 'vue';

const KEY = 'filex.storeLink';
/** A link waiting longer than this (a tab left open) is dropped. */
const MAX_AGE_MS = 60 * 60 * 1000;

export interface StoreLink {
  store: string;
  token: string;
}

/**
 * The path a store link opens, under the admin door. ⚠ In any letter case:
 * the router matches its paths so (vue-router's default), and a link to
 * `/admin/Store-Install#…` opened the page with the token left in the address
 * bar (store fe review #6).
 */
export function isStoreInstallPath(pathname: string): boolean {
  return /\/admin\/store-install\/?$/i.test(pathname);
}

/** The longest fragment read at all, and the longest store address. */
const MAX_FRAGMENT = 4096;
const MAX_STORE = 2048;
/** A token a store hands out (the server holds it to the same rule). */
const TOKEN_RE = /^[A-Za-z0-9._~-]{8,512}$/;
/** A store address: https://host[:port], or http on this machine (the
 *  server decides that half; here only the shape). ASCII only - a look-alike
 *  host in another script is refused, never shown to be compared. */
const STORE_RE = /^https?:\/\/[A-Za-z0-9.-]+(?::[0-9]{1,5})?\/?$/;

/**
 * Read `#store=…&intent=…`; null when the fragment is not a store link.
 *
 * ⚠ Strict, because the fragment is whatever the address bar was given: each
 * key once, nothing else beside them, a token of the store's own alphabet
 * (8-512 characters), a store that is an origin in ASCII (no path, no
 * credentials, no look-alike script), nothing over 4 KiB. Anything else is no
 * link at all - the page says so and asks the server nothing.
 */
export function parseStoreFragment(hash: string): StoreLink | null {
  const h = hash.startsWith('#') ? hash.slice(1) : hash;
  if (!h || h.length > MAX_FRAGMENT) return null;
  let q: URLSearchParams;
  try {
    q = new URLSearchParams(h);
  } catch {
    return null;
  }
  const keys = [...q.keys()];
  if (keys.length !== 2 || q.getAll('store').length !== 1 || q.getAll('intent').length !== 1) return null;
  const store = q.get('store') ?? '';
  const token = q.get('intent') ?? '';
  if (store.length > MAX_STORE || !STORE_RE.test(store) || !TOKEN_RE.test(token)) return null;
  return { store: store.replace(/\/$/, ''), token };
}

function storage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.sessionStorage : null;
  } catch {
    return null;
  }
}

/**
 * Take a store link off the address bar into this tab's sessionStorage.
 * Returns what it took. ⚠ Runs before createWebHistory (router/index.ts),
 * which reads the address once and writes it back at the first navigation:
 * `history.replaceState` keeps the path and the query, drops the fragment,
 * and adds no entry.
 */
export function captureStoreLink(win: Window = window): StoreLink | null {
  dropStaleStoreLink();
  if (!isStoreInstallPath(win.location.pathname)) return null;
  if (!win.location.hash) return null;
  return captureStoreFragment(win.location.hash, win);
}

/**
 * Take the store link `hash` carries (or the fragment that is none): the
 * address bar loses its fragment (replaced, no entry added), the tab keeps
 * the link, and the store page hears that one arrived (storeLinkArrivals).
 * The newest fragment decides: one that is no store link drops a link still
 * waiting from before. The page load's capture above and the router's guard
 * (a second link in a tab already on the page) both come through here.
 */
export function captureStoreFragment(hash: string, win: Window = window): StoreLink | null {
  // Whatever it was, the fragment goes: a malformed one included.
  if (win.location.hash) win.history.replaceState(win.history.state, '', win.location.pathname + win.location.search);
  const link = parseStoreFragment(hash);
  storeLinkArrivals.value++;
  if (!link) {
    malformed = true;
    takeStoreLink();
    return null;
  }
  malformed = false;
  keepStoreLink(link);
  return link;
}

/**
 * Keep `link` waiting in this tab (again): a page that took it and could not
 * use it yet - the session had ended - puts it back for after the sign-in.
 * It keeps the time it first arrived, so it still goes after an hour.
 */
export function keepStoreLink(link: StoreLink): void {
  const at = arrived.get(link) ?? Date.now();
  try {
    storage()?.setItem(KEY, JSON.stringify({ store: link.store, token: link.token, at }));
  } catch {
    /* private mode: the view reads `pending` below */
  }
  pending = link;
  arrived.set(link, at);
}

/** Counts the store fragments taken off the address bar while this page lives. */
export const storeLinkArrivals = ref(0);

// The link of this page load, for a browser whose storage refused it.
let pending: StoreLink | null = null;
// This page load was given a fragment that is not a store link.
let malformed = false;
// When each link handed out first arrived (keepStoreLink keeps that time).
const arrived = new WeakMap<StoreLink, number>();

/** Whether this page load was given a fragment that is no store link (once). */
export function takeMalformedLink(): boolean {
  const m = malformed;
  malformed = false;
  return m;
}

/** The waiting link, once: read and removed. */
export function takeStoreLink(): StoreLink | null {
  let out: StoreLink | null = pending;
  pending = null;
  const s = storage();
  try {
    const raw = s?.getItem(KEY);
    s?.removeItem(KEY);
    if (raw) {
      const v = JSON.parse(raw) as StoreLink & { at?: number };
      if (v && typeof v.store === 'string' && typeof v.token === 'string' && Date.now() - (v.at ?? 0) < MAX_AGE_MS) {
        out = { store: v.store, token: v.token };
        arrived.set(out, v.at ?? Date.now());
      }
    }
  } catch {
    /* unreadable: nothing waiting */
  }
  return out;
}

/**
 * Drop a link that has waited over an hour (a tab left open, a sign-in never
 * finished): every page load does, on any page (store fe review #3).
 */
export function dropStaleStoreLink(): void {
  const s = storage();
  try {
    const raw = s?.getItem(KEY);
    if (!raw) return;
    const v = JSON.parse(raw) as { store?: unknown; token?: unknown; at?: number };
    if (v && typeof v.store === 'string' && typeof v.token === 'string' && Date.now() - (v.at ?? 0) < MAX_AGE_MS) return;
    s?.removeItem(KEY);
  } catch {
    try {
      s?.removeItem(KEY);
    } catch {
      /* storage refused: nothing kept there either */
    }
  }
}

/** Whether a link younger than an hour waits in this tab, without taking it. */
export function hasStoreLink(): boolean {
  if (pending) return Date.now() - (arrived.get(pending) ?? Date.now()) < MAX_AGE_MS;
  try {
    const raw = storage()?.getItem(KEY);
    if (!raw) return false;
    const v = JSON.parse(raw) as { store?: unknown; token?: unknown; at?: number };
    return !!v && typeof v.store === 'string' && typeof v.token === 'string' && Date.now() - (v.at ?? 0) < MAX_AGE_MS;
  } catch {
    return false;
  }
}

/**
 * `fullPath` without a store link's fragment - what a sign-in may carry back
 * (`?redirect=`, and from there an SSO's return address). ⚠ A store link's
 * token never goes into another address: the link waits in this tab and the
 * store page reads it after the sign-in (store fe review #2).
 */
export function dropStoreFragment(fullPath: string): string {
  const i = fullPath.indexOf('#');
  if (i < 0) return fullPath;
  const path = fullPath.slice(0, i).split('?')[0];
  const fragment = fullPath.slice(i + 1);
  if (/\/store-install\/?$/i.test(path) || /(^|&)(store|intent)=/i.test(fragment)) return fullPath.slice(0, i);
  return fullPath;
}

/** Whether this document is inside a frame (a frame we cannot look out of
 *  counts as one). */
export function isFramed(win: Window = window): boolean {
  try {
    return win.self !== win.top;
  } catch {
    return true;
  }
}
