import { createI18n } from 'vue-i18n';
import en from '../locales/en.json';
import tr from '../locales/tr.json';

export type Locale = 'en' | 'tr';
export const SUPPORTED_LOCALES: Locale[] = ['en', 'tr'];
const STORAGE_KEY = 'filex.locale';

// Set once the account's own preference has been applied, so a capabilities
// response arriving afterwards cannot overwrite it with the instance default.
let accountLocaleApplied = false;

export function getStoredLocale(): Locale {
  const v = localStorage.getItem(STORAGE_KEY);
  if (v && (SUPPORTED_LOCALES as string[]).includes(v)) return v as Locale;

  const browser = (navigator.language || 'en').slice(0, 2).toLowerCase();
  if (browser === 'tr') return 'tr';
  return 'en';
}

export function setStoredLocale(locale: Locale): void {
  localStorage.setItem(STORAGE_KEY, locale);
  document.documentElement.lang = locale;
  i18n.global.locale.value = locale;
}

export function applyStoredLocale(): void {
  const l = getStoredLocale();
  document.documentElement.lang = l;
}

// applyAccountLocale pins the UI language to the one saved on the ACCOUNT
// (`users.locale`, from /api/auth/me).
//
// ⚠⚠ Until v0.34.1 this preference was written and never read. Profile → Save
// stores it server-side *and* calls setStoredLocale, so it looked correct on
// the machine you saved it on; sign in from a second browser, a new device or
// a private window and the account's language was ignored entirely.
//
// It went unnoticed because the miss was invisible while `tr` was the
// fallback for everybody: a Turkish user got Turkish either way, from the
// default rather than from their choice. v0.33.0 made the fallback `en` — the
// right change — and the never-read preference surfaced as "I set Turkish and
// the panel is in English". `17-theme-locale.cy.ts` had been asserting this
// all along and had been passing for the wrong reason.
//
// Ranked below a device-local choice on purpose: the language switcher writes
// only to localStorage, so someone who flips the switcher on this machine has
// made a newer decision than the one stored on the account. Never persists.
export function applyAccountLocale(loc?: string | null): void {
  if (!loc) return;
  if (localStorage.getItem(STORAGE_KEY)) return; // this device already chose
  if (!(SUPPORTED_LOCALES as string[]).includes(loc)) return;
  accountLocaleApplied = true;
  document.documentElement.lang = loc;
  i18n.global.locale.value = loc as Locale;
}

// applyServerDefaultLocale pins the UI language to the operator's
// FILEX_DEFAULT_LOCALE (from /api/capabilities) for users who haven't picked
// one yet — overriding browser detection. A user's explicit switch (stored in
// localStorage) always wins, and this never persists, so it stays a *default*.
//
// ⚠ Ranked BELOW applyAccountLocale: this is the instance's default for people
// who have expressed no preference, so it must not overwrite one that a user
// actually saved. Capabilities and /me race, so the guard is explicit rather
// than positional.
export function applyServerDefaultLocale(def?: string | null): void {
  if (!def) return;
  if (localStorage.getItem(STORAGE_KEY)) return; // user already chose
  if (accountLocaleApplied) return; // the account's own choice outranks this
  if (!(SUPPORTED_LOCALES as string[]).includes(def)) return;
  document.documentElement.lang = def;
  i18n.global.locale.value = def as Locale;
}

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: getStoredLocale(),
  fallbackLocale: 'en',
  messages: { en, tr },
});

export function t(key: string, params?: Record<string, unknown>): string {
  // Tiny helper so non-component code can translate without injecting i18n.
  return i18n.global.t(key, params ?? {});
}
