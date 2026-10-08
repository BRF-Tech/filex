/**
 * usePublicText — the words a public page says, in the SERVER's catalogue.
 *
 * ⚠⚠ The pages a stranger opens (`/s/<token>`, `/d/<token>`) are said by the
 * server: its no-JavaScript pages, its refusals (a file request's `message`)
 * and these JavaScript pages all read ONE table, `server.public.*`, from
 * `GET /api/public/strings?lang=` (handlers/public_api.go `Strings`). This
 * package used to carry a second copy of those words (`public.*` in
 * locales/en.ts), so the same limit was said two ways on the two pages - "Not
 * sent - larger than 5 MB." here, "a.pdf is too big (max 5 MB)." there - and
 * a language pack had to translate both. There is no client copy any more: a
 * key the table does not have reads as nothing, never as a client sentence.
 *
 * The table is fetched by the page that owns the visitor's language
 * (`PublicLinkPage`, and the admin's `PublicLinkPreview`) and handed down
 * with `providePublicText`; the shell and its bodies read it with
 * `usePublicText`. A counted sentence picks its form the way the server's
 * own page script does: `strings[key + "_" + category] || strings[key]`.
 */
import { computed, inject, provide, ref, watch, type InjectionKey, type Ref } from 'vue';
import { pluralCategory } from '../lib/plural';
import { apiRootOr } from '../lib/appBase';
import { foreignText } from '../lib/direction';

/** `GET /api/public/strings`. */
export interface PublicStringsAnswer {
  lang: string;
  dir?: string;
  strings: Record<string, string>;
}

/** What a page that owns the language hands down. */
export interface PublicTextSource {
  /** The table, keys without `server.public.`; empty until it arrives. */
  table: Readonly<Ref<Readonly<Record<string, string>>>>;
  /** The language the server answered the table in. */
  lang: Readonly<Ref<string>>;
  /** True once an answer (or a failure) has come back for the current language. */
  ready: Readonly<Ref<boolean>>;
}

export const PUBLIC_TEXT: InjectionKey<PublicTextSource> = Symbol('filex-public-text');

export interface PublicTextOptions {
  /** API origin; empty = same origin. */
  base?: string;
  /** The visitor's language, as the page resolved it. */
  locale: () => string;
  fetchImpl?: typeof fetch;
}

/**
 * Fetch the server's public sentences for the page's language, and again
 * whenever the language changes. A failed fetch leaves the table empty and
 * `ready` true: the page still opens (its controls carry no words of their
 * own), and nothing invents a sentence in the server's place.
 */
export function loadPublicText(opts: PublicTextOptions): PublicTextSource {
  const table = ref<Record<string, string>>({});
  const lang = ref('');
  const ready = ref(false);
  let asked = 0;

  async function load(code: string): Promise<void> {
    const mine = ++asked;
    ready.value = false;
    const doFetch = opts.fetchImpl ?? (typeof fetch === 'function' ? fetch : null);
    try {
      if (!doFetch) return;
      const res = await doFetch(`${apiRootOr(opts.base)}/api/public/strings?lang=${encodeURIComponent(code)}`, {
        method: 'GET',
        headers: { Accept: 'application/json' },
        credentials: 'same-origin',
      });
      if (!res.ok) return;
      const body = (await res.json()) as PublicStringsAnswer;
      if (mine !== asked) return; // a newer language was asked for meanwhile
      table.value = body?.strings && typeof body.strings === 'object' ? body.strings : {};
      lang.value = body?.lang || code;
    } catch {
      /* the page opens without the words rather than not at all */
    } finally {
      if (mine === asked) ready.value = true;
    }
  }

  watch(opts.locale, (code) => void load(code), { immediate: true });
  return { table, lang, ready };
}

/** Hand the table to the shell and its bodies. */
export function providePublicText(src: PublicTextSource): void {
  provide(PUBLIC_TEXT, src);
}

/** The variables a counted sentence picks its form on, in this order. */
const COUNTERS = ['count', 'n'] as const;

/**
 * One server sentence, filled: `{name}` placeholders replaced, the plural form
 * picked on the count. Exported for tests and for a caller outside a
 * component; components use `usePublicText().pt`.
 */
export function publicSentence(
  table: Readonly<Record<string, string>>,
  lang: string,
  key: string,
  vars: Record<string, string | number> = {},
): string {
  let raw: string | undefined;
  const counter = COUNTERS.find((c) => c in vars);
  if (counter !== undefined) {
    const cat = pluralCategory(lang || 'en', Number(vars[counter]));
    if (cat !== 'other') raw = table[`${key}_${cat}`];
  }
  raw = raw || table[key] || '';
  return Object.entries(vars).reduce((acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)), raw);
}

/**
 * The shell's and the bodies' reader. Outside a provider (a component mounted
 * on its own) the table is empty and every sentence reads as nothing.
 */
export function usePublicText(locale?: () => string) {
  const src = inject(PUBLIC_TEXT, null);
  const table = computed(() => src?.table.value ?? {});
  const lang = computed(() => src?.lang.value || (locale ? locale() : '') || 'en');
  const ready = computed(() => src?.ready.value ?? true);
  /**
   * The server's sentence for `key` (no `server.public.` prefix). ⚠ It is
   * not this package's text, so a right-to-left page isolates it the way it
   * isolates every other server sentence (lib/direction `foreignText`).
   */
  function pt(key: string, vars: Record<string, string | number> = {}): string {
    const said = publicSentence(table.value, lang.value, key, vars);
    return said ? foreignText(lang.value, said) : '';
  }
  return { pt, ready };
}
