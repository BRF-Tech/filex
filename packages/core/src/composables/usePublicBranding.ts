/**
 * usePublicBranding — whose product the visitor thinks they are looking at.
 *
 * ⚠⚠ v3 §1.2: whitelabel means whitelabel. A share, a file request and a
 * signature request are the surfaces an OUTSIDER sees, and until now they
 * were the only ones that ignored the instance's own name, logo and colour
 * — so an operator who had renamed filex everywhere still sent out links
 * that said filex. The branding record is the same one the admin
 * "Corporate identity" page writes; this composable is how the public shell
 * wears it.
 *
 * ⚠ It is a PUBLIC fetch, and the page must be usable before it lands: the
 * shell paints with the stock palette and re-paints when the answer arrives.
 * Nothing waits on it and a failure is not an error — an older server has no
 * such route, and the product's own name is a perfectly good answer.
 *
 * ⚠ The accent is written as `--fe-*` custom properties on the SHELL's root
 * element, never on `:root`. The same shell is mounted inside other pages
 * (an embed, a test harness), and a public link must not repaint the host
 * around it.
 */
import { computed, ref } from 'vue';
import type { PublicBranding } from '../types/Public';
import { setLocalesFromBranding } from '../lib/uiLocales';

/** The product's own name, used until (and unless) an operator renames it. */
export const DEFAULT_BRAND_NAME = 'filex';

export interface PublicBrandingOptions {
  /** API origin; empty = same origin. */
  base?: string;
  fetchImpl?: typeof fetch;
}

const HEX = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/;

/** `#abc` → `#aabbcc`; anything that is not a hex colour → ''. */
export function normalizeAccent(raw: string | undefined | null): string {
  const v = String(raw ?? '').trim();
  if (!HEX.test(v)) return '';
  if (v.length === 4) return `#${v[1]}${v[1]}${v[2]}${v[2]}${v[3]}${v[3]}`.toLowerCase();
  return v.toLowerCase();
}

/** The same colour, `k` of the way to black (0.85 = the hover state). */
export function shade(hex: string, k: number): string {
  const v = normalizeAccent(hex);
  if (!v) return '';
  const n = parseInt(v.slice(1), 16);
  const part = (shift: number) => Math.max(0, Math.min(255, Math.round(((n >> shift) & 0xff) * k)));
  const hx = (x: number) => x.toString(16).padStart(2, '0');
  return `#${hx(part(16))}${hx(part(8))}${hx(part(0))}`;
}

/**
 * Black or white on this colour, by luminance.
 *
 * ⚠ Not a constant. An operator who picks a pale accent gets white text on
 * pale yellow if the ink is hardcoded — which is what "unreadable button"
 * looks like to the person reporting it, and it is only ever seen on
 * somebody else's instance.
 */
export function inkOn(hex: string): string {
  const v = normalizeAccent(hex);
  if (!v) return '';
  const n = parseInt(v.slice(1), 16);
  const lin = (c: number) => {
    const x = c / 255;
    return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
  };
  const L = 0.2126 * lin((n >> 16) & 0xff) + 0.7152 * lin((n >> 8) & 0xff) + 0.0722 * lin(n & 0xff);
  return L > 0.42 ? '#0b0f14' : '#ffffff';
}

/**
 * The `--fe-*` properties an accent colour becomes on the public shell — the
 * ONE derivation, used by the real page (usePublicBranding) and by the admin
 * page's live preview (PublicLinkPreview), so the preview cannot paint a
 * button the page itself would not. `{}` for no (or no valid) accent.
 */
export function accentStyleOf(raw: string | undefined | null): Record<string, string> {
  const accent = normalizeAccent(raw);
  if (!accent) return {} as Record<string, string>;
  const n = parseInt(accent.slice(1), 16);
  const rgb = `${(n >> 16) & 0xff}, ${(n >> 8) & 0xff}, ${n & 0xff}`;
  return {
    '--fe-primary': accent,
    '--fe-primary-hover': shade(accent, 0.85),
    '--fe-text-on-primary': inkOn(accent),
    // ⚠ The TINT and the ink on it, or the accent only half arrives: the
    // button turned orange while the round badge behind the padlock, the
    // focus ring on the PIN box and the row hover stayed the stock blue
    // (measured on the first after-shot of the restored gate). The old
    // Go-rendered page derived exactly these from the same colour
    // (`--px-accent-soft: rgba(r,g,b,0.14)`, `color: var(--px-accent)`), and
    // a translucent tint is right rather than a solid: it composites onto
    // the card in light and in dark without a second value.
    '--fe-primary-soft': `rgba(${rgb}, 0.14)`,
    '--fe-primary-ink': accent,
  };
}

export function usePublicBranding(opts: PublicBrandingOptions = {}) {
  const branding = ref<PublicBranding | null>(null);
  const loaded = ref(false);

  const name = computed(() => branding.value?.name?.trim() || DEFAULT_BRAND_NAME);
  /** True when the operator has named the instance something of their own. */
  const whitelabel = computed(() => !!branding.value?.name?.trim() && name.value !== DEFAULT_BRAND_NAME);
  const logo = computed(() => branding.value?.logo_url?.trim() || '');
  const footerText = computed(() => branding.value?.footer_text?.trim() || '');
  const hidePoweredBy = computed(() => branding.value?.hide_powered_by === true);

  /**
   * The instance's light/dark default for a visitor who has chosen nothing.
   *
   * ⚠ The server's word is `system`; this package's is `auto`. They mean
   * the same thing and the translation happens HERE, once, rather than in
   * every component that compares the string.
   */
  const themeDefault = computed<'light' | 'dark' | 'auto'>(() => {
    const t = branding.value?.theme;
    return t === 'light' || t === 'dark' ? t : 'auto';
  });

  /** The languages this instance offers, as the branding answer lists them. */
  const locales = computed<string[]>(() => branding.value?.locales ?? []);

  /** The accent, as the `--fe-*` properties the shell's root carries. */
  const accentStyle = computed<Record<string, string>>(() => accentStyleOf(branding.value?.accent));

  /**
   * Ask the server.
   *
   * ⚠ `/api/public/branding` first, `/api/branding` second. The second is
   * the route this product has served since wiring:e1 and answers a
   * compatible record; asking only for the new address and giving up on a
   * 404 would mean a whitelabelled instance goes back to saying "filex" the
   * moment it is updated in the wrong order.
   *
   * ⚠ The languages ride along: `locales` / `ui_locales` are on this payload,
   * so the picker is filled by the fetch the shell already makes
   * (`lib/uiLocales.setLocalesFromBranding`). Only the language somebody
   * actually reads has its strings fetched, once.
   */
  async function load(): Promise<PublicBranding | null> {
    const doFetch = opts.fetchImpl ?? (typeof fetch === 'function' ? fetch : null);
    if (!doFetch) return null;
    const origin = (opts.base ?? '').replace(/\/+$/, '');
    for (const path of ['/api/public/branding', '/api/branding']) {
      try {
        const res = await doFetch(`${origin}${path}`, {
          method: 'GET',
          headers: { Accept: 'application/json' },
          credentials: 'same-origin',
        });
        if (!res.ok) continue;
        branding.value = (await res.json()) as PublicBranding;
        loaded.value = true;
        // ⚠ With the same origin and fetch: a language's STRINGS are fetched
        // later from `/api/public/ui-locales/{code}` on this same server.
        setLocalesFromBranding(branding.value, { base: opts.base, fetchImpl: opts.fetchImpl });
        return branding.value;
      } catch {
        /* try the next address; the default name is already on screen */
      }
    }
    return null;
  }

  return { branding, loaded, name, whitelabel, logo, footerText, hidePoweredBy, themeDefault, locales, accentStyle, load };
}
