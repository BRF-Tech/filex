/**
 * explorerLocale — which language an explorer speaks, and why.
 *
 * Two kinds of host mount the explorer, and they answer the question
 * differently on purpose:
 *
 *   - the APP surfaces (the filex web app, the desktop app) bind the screen's
 *     language to the ACCOUNT (#191): the account's language, then the host's
 *     `config.locale` (which those hosts keep equal to the account's), then the
 *     browser's, then English. Unchanged here; the order is pinned by
 *     web/tests/lib/explorerLocale.test.ts.
 *   - an EMBED (`<filex-explorer>`, or core's FileExplorer mounted by another
 *     product) has no account settings screen of its own and often no person
 *     behind its credential. The owner's ruling (2026-10-19, task #275):
 *
 *       (a) what the person picked IN THIS BROWSER (the explorer's "⋯" →
 *           Language, `EMBED_LOCALE_LS_KEY`),
 *       (b) the account's language - only when a PERSON is behind the
 *           credential (a "personal" embed, `callerKind` `user`),
 *       (c) the browser's languages, matched against what this server offers
 *           (built-in English and Turkish plus its language packs),
 *       (d) the host's `config.locale`,
 *       (e) English.
 *
 *     The host's language is a default it wrote for every visitor before any
 *     of them arrived; the person's own pick, their account and their browser
 *     each say more about the person reading. The same judgement ranks the
 *     viewer's time zone above the host's (lib/timezone TIME_ZONE_TIERS).
 *
 * ⚠⚠ What makes an explorer an embed: no host told core where the person's
 * preferences live. The web app calls `configurePrefs({ surface: 'web' })` at
 * boot (web/src/main.ts) and the desktop app hands the explorer
 * `config.accountPrefs: { surface: 'desktop' }`; an embed does neither, which
 * is the same test lib/prefs already uses to keep an embed's preferences to
 * this page (`prefsConfigured`). ⚠ Not `accountPrefs` alone: the web app
 * never sets it on the explorer, so that test alone would have treated the
 * filex web app as an embed.
 *
 * ⚠ The pick is kept in THIS browser only (localStorage, per origin). It is
 * never sent to the account's preference document (`/api/me/prefs`); a
 * personal embed also writes it to the account's profile (`users.locale`),
 * and that write is the explorer's (FileExplorer `pickEmbedLocale`), not this
 * module's. ⚠ Not the web app's `filex.locale` key: that one is the web app's
 * copy of the ACCOUNT's language, and reading it here would rank another
 * product's cache above this person's browser.
 */

import { ref, type Ref } from 'vue';
import type { ExplorerConfig, LocaleCode } from '../types/ExplorerConfig';
import { browserLocale } from '../locales/resolve';
import { acceptableLocale, normalizeLocaleCode, plausibleLocale } from './uiLocales';
import { prefsConfigured } from './prefs';

/** localStorage key of the embed's own language pick. `''`/absent = none. */
export const EMBED_LOCALE_LS_KEY = 'filex.embed.locale';

/** Same-tab news for another bundle on this page (a window never receives its own `storage` event). */
const EMBED_LOCALE_EVENT = 'filex:embed-locale';

/* ── which kind of host ───────────────────────────────────────────────── */

/**
 * Is this explorer embedded in another product? See the module note: no host
 * wired the account's preference document - neither `configurePrefs` (the web
 * app, at boot) nor `config.accountPrefs` (the desktop app).
 */
export function explorerIsEmbedded(config: Pick<ExplorerConfig, 'accountPrefs'>): boolean {
  return !config.accountPrefs?.surface && !prefsConfigured();
}

/* ── the order ────────────────────────────────────────────────────────── */

/** Where the language on screen came from. */
export type ExplorerLocaleTier = 'chosen' | 'account' | 'browser' | 'host' | 'default';

export interface ExplorerLocaleSources {
  /** The explorer is embedded (`explorerIsEmbedded`). */
  embedded: boolean;
  /** Embeds only: the pick made in this browser. Ignored on an app surface. */
  chosen?: string | null;
  /** The account's language (`users.locale`). The caller passes it only when a person is behind the credential. */
  account?: string | null;
  /** The browser's languages, most wanted first (`navigatorLanguages()`). */
  browser?: readonly (string | null | undefined)[];
  /** The host's `config.locale`. */
  host?: string | null;
}

export interface ResolvedExplorerLocale {
  locale: LocaleCode;
  tier: ExplorerLocaleTier;
}

const clean = (v: string | null | undefined): string => String(v ?? '').trim();

/**
 * THE order (see the module note). Reads only what it is given and the
 * server's language list (lib/uiLocales, reactive), so a computed that calls
 * it re-decides when a pack's language arrives.
 *
 * A stored or host-given language that is still in flight (a pack's `es`
 * before the list arrived) is HELD, as `resolveLocale` holds it; the browser's
 * languages are matched only against what is offered, because a browser asks
 * for many languages and the first plausible one is not an answer.
 */
export function resolveExplorerLocale(s: ExplorerLocaleSources): ResolvedExplorerLocale {
  const browser = () => browserLocale(s.browser ?? []);
  if (!s.embedded) {
    // #191, as it was: the account's language, else the host's; whichever is
    // named and refused falls to the browser rather than to the other one.
    const named = clean(s.account) || clean(s.host);
    if (named && acceptableLocale(named)) {
      return { locale: normalizeLocaleCode(named), tier: clean(s.account) ? 'account' : 'host' };
    }
    const b = browser();
    return b ? { locale: b, tier: 'browser' } : { locale: 'en', tier: 'default' };
  }
  const chosen = clean(s.chosen);
  if (chosen && acceptableLocale(chosen)) return { locale: normalizeLocaleCode(chosen), tier: 'chosen' };
  const account = clean(s.account);
  if (account && acceptableLocale(account)) return { locale: normalizeLocaleCode(account), tier: 'account' };
  const b = browser();
  if (b) return { locale: b, tier: 'browser' };
  const host = clean(s.host);
  if (host && acceptableLocale(host)) return { locale: normalizeLocaleCode(host), tier: 'host' };
  return { locale: 'en', tier: 'default' };
}

/* ── the account's language ───────────────────────────────────────────── */

/** What `accountLanguage` / `pickEmbedLanguage` need from their explorer. */
export interface AccountLanguageDeps {
  /** A PERSON is behind the credential (`callerKind` / capabilities `caller_kind` is `user`). */
  person: boolean;
  /** The explorer is embedded (`explorerIsEmbedded`). */
  embedded: boolean;
  /** The host's `config.locale`. */
  host?: string | null;
  /** `GET /api/auth/me` with the explorer's credential. Rejects with nobody signed in. */
  me: () => Promise<{ user?: { locale?: unknown } | null } | undefined>;
  /** `PATCH /api/auth/profile` with the explorer's credential (lib/userSettingsHost `updateProfile`). */
  updateProfile: (patch: { locale: string }) => Promise<unknown>;
}

/**
 * The language of the account behind the credential (`users.locale`), or `''`.
 *
 *   - No person behind it (an app's token, shared by every visitor of the
 *     embed that holds it): `''`, and NOTHING is asked - its owner's language
 *     is nobody's.
 *   - Nobody signed in (a public link, a refused credential): `''`.
 *   - An account with a language of its own: that language.
 *   - An account with NONE, on the web and desktop apps: the host's language
 *     is written to it and becomes its own (#191 - the desktop's pinToAdopt
 *     does the same; the web panel's sign-in has the server do it,
 *     handlers/auth.go adoptSignInLanguage), so its notifications agree with
 *     the screen. ⚠ Not in an embed (#275): there the host's language is the
 *     LAST resort, a default written for every visitor, and the account is
 *     written only by the person's own pick (`pickEmbedLanguage`). Seeding it
 *     from the host made one product's default every device's language.
 */
export async function accountLanguage(d: AccountLanguageDeps): Promise<string> {
  if (!d.person) return '';
  let user: { locale?: unknown } | null | undefined;
  try {
    user = (await d.me())?.user;
  } catch {
    return ''; /* nobody signed in: the host's language */
  }
  if (!user) return '';
  const own = typeof user.locale === 'string' ? user.locale.trim() : '';
  if (own) return own;
  const seed = clean(d.host);
  if (d.embedded || !seed) return '';
  d.updateProfile({ locale: seed }).catch(() => {
    /* an account that may not edit its profile keeps none; the host's
       language stays on screen */
  });
  return '';
}

/**
 * A language picked in an embed's own dialog (`''` = back to the default).
 * Kept in THIS browser (`setEmbedLocaleChoice`); in a personal embed also
 * written to the account (`users.locale`), so the person's other devices and
 * their notifications follow - the same `PATCH /api/auth/profile` the user
 * settings dialog sends. Clearing the pick leaves the account as it is.
 *
 * Returns the language now picked (`''` = none), which the explorer also takes
 * as the account's when it was written there.
 */
export function pickEmbedLanguage(code: string, d: Pick<AccountLanguageDeps, 'person' | 'updateProfile'>): string {
  setEmbedLocaleChoice(code);
  const picked = chosen.value;
  if (picked && d.person) {
    d.updateProfile({ locale: picked }).catch(() => {
      /* the pick already took effect in this browser; an account that may
         not edit its profile keeps the language it had */
    });
  }
  return picked;
}

/* ── the pick, kept in this browser ───────────────────────────────────── */

/** A stored value that could be a language tag, lower-cased; anything else is no pick. */
function tagOrNone(raw: unknown): string {
  const v = typeof raw === 'string' ? raw.trim().toLowerCase().replace(/_/g, '-') : '';
  return plausibleLocale(v) ? v : '';
}

function readStored(): string {
  try {
    return tagOrNone(localStorage.getItem(EMBED_LOCALE_LS_KEY));
  } catch {
    // Private window, blocked site data: no pick, and the account, the
    // browser and the host decide.
    return '';
  }
}

const hasWindow = typeof window !== 'undefined';

/** Read at module load, so the FIRST paint after a reload is already in the picked language. */
const chosen: Ref<string> = ref(hasWindow ? readStored() : '');

/** The pick made in this browser; `''` = none. Reactive. */
export function embedLocaleChoice(): string {
  return chosen.value;
}

/**
 * Set (or, with `''`, clear) the pick in this browser. Takes effect on every
 * embedded explorer on this page at once; nothing is sent anywhere.
 */
export function setEmbedLocaleChoice(code: string): void {
  const v = tagOrNone(code);
  chosen.value = v;
  try {
    if (v) localStorage.setItem(EMBED_LOCALE_LS_KEY, v);
    else localStorage.removeItem(EMBED_LOCALE_LS_KEY);
  } catch {
    /* refused storage: this page still moved, only the next reload forgets */
  }
  try {
    window.dispatchEvent(new CustomEvent(EMBED_LOCALE_EVENT, { detail: v }));
  } catch {
    /* non-DOM environment */
  }
}

if (hasWindow) {
  try {
    window.addEventListener('storage', (e) => {
      if (e.key === null || e.key === EMBED_LOCALE_LS_KEY) chosen.value = readStored();
    });
    // From the event's own detail, not re-read: in a private window storage
    // refuses the write, and re-reading would undo the pick just made.
    window.addEventListener(EMBED_LOCALE_EVENT, (e) => {
      chosen.value = tagOrNone((e as CustomEvent).detail);
    });
  } catch {
    /* non-DOM environment */
  }
}
