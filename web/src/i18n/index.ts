import { createI18n } from 'vue-i18n';
import en from '../locales/en.json';
import tr from '../locales/tr.json';

export type Locale = 'en' | 'tr';
export const SUPPORTED_LOCALES: Locale[] = ['en', 'tr'];
const STORAGE_KEY = 'filex.locale';

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

// applyServerDefaultLocale pins the UI language to the operator's
// FILEX_DEFAULT_LOCALE (from /api/capabilities) for users who haven't picked
// one yet — overriding browser detection. A user's explicit switch (stored in
// localStorage) always wins, and this never persists, so it stays a *default*.
export function applyServerDefaultLocale(def?: string | null): void {
  if (!def) return;
  if (localStorage.getItem(STORAGE_KEY)) return; // user already chose
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
