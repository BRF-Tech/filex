/**
 * themes.ts — wiring:c1 theme registry + application engine.
 *
 * A "theme" is a palette-only preset: a map of `--fe-*` CSS custom
 * properties in TWO variants (light + dark). Picking a theme is fully
 * independent from the light/dark MODE — the mode (config.theme prop /
 * OS preference) keeps deciding which variant of the selected theme is
 * active, exactly like the stock palette in styles/variables.css.
 *
 * Application strategy (two cooperating layers):
 *
 *  1. Inline `--fe-*` variables on the explorer ROOT element
 *     (`applyThemeToEl`) — the documented "consumer overrides by setting
 *     --fe-* on a higher scope" philosophy, resolved to the currently
 *     active variant. Covers the whole `.fe` subtree, works in embeds
 *     without touching the host page's own theme.
 *
 *  2. A singleton injected `<style data-filex-theme>` (`syncThemeStyle`)
 *     that mirrors variables.css' EXACT selector cascade with the
 *     selected theme's values. This is required because some surfaces
 *     re-declare the variables on themselves and would otherwise shadow
 *     the inherited inline values back to the stock palette:
 *       - the Teleport-ed context menu backdrop (`.fe-ctx-backdrop…`
 *         lives under <body>, outside the root element entirely), and
 *       - modal backdrops, which carry their own `.fe` /
 *         `.fe--theme-dark` classes (dark-mode selectors like
 *         `.dark .fe` re-set the tokens on those descendants).
 *     Because the injected element is appended to <head> at runtime it
 *     always comes AFTER the bundled stylesheet, so equal-specificity
 *     rules win by source order — the same mechanism variables.css uses
 *     internally. No `!important` needed.
 *
 * The `default` theme applies NOTHING (inline vars removed, style
 * element emptied) so host-level `--fe-*` overrides keep working
 * untouched — its token maps below exist only to render the gallery
 * preview card.
 *
 * Persistence: localStorage `filex.palette` (absent/invalid → default),
 * shared reactively across every explorer instance on the page and
 * synced across tabs via the `storage` event.
 *
 * All palettes were contrast-checked (WCAG 2.1): text/bg ≥ 7:1,
 * text on elevated/hover/selected surfaces + muted text + text-on-primary
 * ≥ 4.5:1, primary & danger vs bg ≥ 3:1 — in BOTH variants.
 */

import { ref, type Ref } from 'vue';
import { hasSession, registerPersonalMirror, savePref, setLocalPref } from './prefs';

/** Map of `--fe-*` custom property → value. */
export type ThemeTokenMap = Record<string, string>;

export interface ThemeDef {
  /** Stable id — what a palette choice stores (on the account where the host
   *  keeps preferences there, `lib/prefs`; mirrored in localStorage for the
   *  first paint), never shown to users. */
  id: string;
  /**
   * i18n catalogue key for the display name (tr + en).
   *
   * ⚠ Built-in palettes only. An operator's own theme carries `name` instead:
   * "Acme Bulut" is not a string this product translates, and putting it
   * through `t()` would print the key straight back at them the first time
   * somebody named a theme something the catalogue did not contain.
   */
  nameKey?: string;
  /** Literal display name — operator-defined themes only (tema:v1). */
  name?: string;
  /** Palette applied when the resolved mode is light. */
  light: ThemeTokenMap;
  /** Palette applied when the resolved mode is dark. */
  dark: ThemeTokenMap;
}

/**
 * tema:v1 — the prefix that keeps the two namespaces apart.
 *
 * An operator-defined theme's id is always `custom:<slug>`, so no name an
 * operator types can ever shadow `night`, `forest` or `default`. The server
 * stores the slug bare and adds the prefix on the way out
 * (backend/internal/api/handlers/themes.go).
 */
export const CUSTOM_THEME_PREFIX = 'custom:';

/**
 * ⚠⚠ `filex.palette`, NOT `filex.theme` — and the rename is a BUG FIX, not
 * tidying.
 *
 * `web/src/lib/theme.ts` has always stored the admin app's light/dark/auto
 * MODE under `filex.theme`, and this module stored the PALETTE id under the
 * same name. Two writers, one key, neither aware of the other — and because
 * both readers fall back silently on a value they do not recognise, nothing
 * ever threw: picking "Night Blue" in the explorer reset the panel's
 * light/dark choice to auto, and choosing Dark in the panel reset the palette
 * to default. It went unseen for as long as the two controls lived on
 * opposite sides of the product; putting them both in the settings modal
 * makes it a one-click round trip.
 */
export const THEME_LS_KEY = 'filex.palette';

/** The key this preference used to share with the app's mode. Read once, to
 *  carry a palette across the rename; never written. */
const LEGACY_THEME_LS_KEY = 'filex.theme';

export const DEFAULT_THEME_ID = 'default';

/* ------------------------------------------------------------------ */
/* Registry                                                            */
/* ------------------------------------------------------------------ */

/**
 * ⚠⚠ Every theme declares the SAME key set, and there is a test for it
 * (`web/tests/api/themeTokenParity.test.ts`). That test exists because of a
 * bug whose whole nature is that nothing notices:
 *
 *   `--fe-primary-soft` was declared by variables.css and used by nine rules
 *   in base.css — the active tab of a segmented control, a filter chip that is
 *   set, an active toolbar button, the advanced-search tabs — and by NO theme
 *   map at all. So somebody on Forest, Amber or Lilac got their palette
 *   everywhere except the one token that marks "this is the selected thing",
 *   which stayed stock blue and clashed with the palette they had just picked.
 *   `--fe-border-soft` (ten rules, the quieter separators inside panels) was
 *   missing the same way. Neither errored, neither looked broken in the
 *   gallery card, and the only way to see it was to pick a theme and look.
 *
 * How the two are derived, so a new theme fills them the same way:
 *
 *   --fe-primary-soft = the theme's OWN primary mixed into its OWN background,
 *     at the fraction that reproduces the STOCK tint's separation from its
 *     background (1.184:1 light, 1.207:1 dark). Separation is the invariant,
 *     not the mix fraction: the palettes' primaries run from a near-black
 *     green to a pale grey, so a fixed 13% lands anywhere between invisible
 *     and a slab. Held that way, every palette's tint reads with the same
 *     weight as the stock one. Measured: text on it is 9.44:1 (Terminal dark)
 *     to 17.85:1 (High Contrast light), so the 4.5 bar is never in question.
 *   --fe-border-soft = the theme's own border mixed toward its own background
 *     at the stock fraction (0.60 light / 0.68 dark), which reproduces the
 *     stock value exactly. ⚠ High Contrast is the deliberate exception: it
 *     takes its own `--fe-border` unchanged, because a *soft* separator is the
 *     one thing that theme exists to not have.
 *
 * ⚠ What is deliberately NOT themed, and why:
 *   `--fe-ok` / `--fe-warning` / `--fe-keep-ok` — status signals, not taste.
 *     They are identical in light and dark on purpose; "ok" that is green in
 *     one palette and amber in another stops being a signal.
 *   `--fe-icon-*` — file-type accents. A PDF is red and a folder is yellow in
 *     every palette, and they are never the only cue (the row carries its name
 *     in `--fe-text` too).
 *   radii, control heights, the type scale, gaps, `--fe-sidenav-w`,
 *     `--fe-font-mono` — metrics, not a palette.
 *   `--fe-shadow` / `--fe-shadow-sm` / `--fe-font` — depth and face rather
 *     than colour, and the two themes that DO override them (High Contrast's
 *     hard 1px outlines, Terminal's monospace face) are extras on top of the
 *     shared set, not holes in it. The parity test allows extras and requires
 *     the shared set.
 */
export const THEMES: ThemeDef[] = [
  {
    // Stock palette — the maps duplicate variables.css ONLY for the
    // gallery preview; applying this theme clears all overrides.
    id: DEFAULT_THEME_ID,
    nameKey: 'theme.name.default',
    light: {
      /* ⚠ Mirrors styles/variables.css exactly — web/tests/api/themeContrast.test.ts
       * fails on any drift. It has drifted before: the stock primary was
       * darkened for contrast and this preview map kept the old blue, so the
       * gallery card advertised a colour the product no longer used. */
      '--fe-bg': '#ffffff',
      '--fe-bg-elev': '#f7f8fb',
      '--fe-bg-hover': '#f3f4f6',
      '--fe-bg-selected': '#eef3ff',
      '--fe-border': '#e5e7eb',
      '--fe-border-soft': '#eef0f4',
      '--fe-border-strong': '#d1d5db',
      '--fe-text': '#1f2937',
      '--fe-text-muted': '#6b7280',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#2f6ceb',
      '--fe-primary-hover': '#2559c9',
      '--fe-primary-soft': '#e6ecfa',
      '--fe-primary-ink': '#2559c9',
      '--fe-danger': '#dc2626',
      '--fe-danger-hover': '#b91c1c',
    },
    dark: {
      '--fe-bg': '#15171c',
      '--fe-bg-elev': '#1a1d23',
      '--fe-bg-hover': '#1f232a',
      '--fe-bg-selected': '#1c2740',
      '--fe-border': '#2e333c',
      '--fe-border-soft': '#262a32',
      '--fe-border-strong': '#3d444f',
      '--fe-text': '#e6e8ec',
      '--fe-text-muted': '#888f9b',
      /* Dark ink, not white: white on the button measures 3.16 against the 4.5
       * this file claims. Mirrors styles/variables.css. */
      '--fe-text-on-primary': '#15171c',
      '--fe-primary': '#5b8cff',
      '--fe-primary-hover': '#7ba3ff',
      '--fe-primary-soft': '#1c2740',
      '--fe-primary-ink': '#7ba3ff',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Night Blue — deep indigo/navy.
    id: 'night',
    nameKey: 'theme.name.night',
    light: {
      '--fe-bg': '#f6f8fd',
      '--fe-bg-elev': '#edf1fa',
      '--fe-bg-hover': '#e2e9f7',
      '--fe-bg-selected': '#d2ddf4',
      '--fe-border': '#d6deee',
      '--fe-border-soft': '#e3e8f4',
      '--fe-border-strong': '#aebfda',
      '--fe-text': '#151f38',
      '--fe-text-muted': '#49587a',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#2c4a9e',
      '--fe-primary-hover': '#213a80',
      '--fe-primary-soft': '#e0e6f4',
      '--fe-primary-ink': '#213a80',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#0a1024',
      '--fe-bg-elev': '#111a35',
      '--fe-bg-hover': '#1a2547',
      '--fe-bg-selected': '#243665',
      '--fe-border': '#26335a',
      '--fe-border-soft': '#1d2748',
      '--fe-border-strong': '#3c4e84',
      '--fe-text': '#dfe6f7',
      '--fe-text-muted': '#96a5cc',
      '--fe-text-on-primary': '#050b26',
      '--fe-primary': '#6d8dfc',
      '--fe-primary-hover': '#5273f2',
      '--fe-primary-soft': '#182243',
      '--fe-primary-ink': '#6d8dfc',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Forest — calm greens.
    id: 'forest',
    nameKey: 'theme.name.forest',
    light: {
      '--fe-bg': '#f5faf5',
      '--fe-bg-elev': '#eaf3eb',
      '--fe-bg-hover': '#dcecdf',
      '--fe-bg-selected': '#c8e2ce',
      '--fe-border': '#d2e2d4',
      '--fe-border-soft': '#e0ece1',
      '--fe-border-strong': '#a2c3a8',
      '--fe-text': '#182b1d',
      '--fe-text-muted': '#42604a',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#2b7a41',
      '--fe-primary-hover': '#226334',
      '--fe-primary-soft': '#dbe9dc',
      '--fe-primary-ink': '#226334',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#0d1611',
      '--fe-bg-elev': '#132019',
      '--fe-bg-hover': '#1b2d23',
      '--fe-bg-selected': '#264532',
      '--fe-border': '#28402f',
      '--fe-border-soft': '#1f3225',
      '--fe-border-strong': '#3e5f49',
      '--fe-text': '#dcebe0',
      '--fe-text-muted': '#93b09b',
      '--fe-text-on-primary': '#04180b',
      '--fe-primary': '#54c17a',
      '--fe-primary-hover': '#3fae66',
      '--fe-primary-soft': '#16291d',
      '--fe-primary-ink': '#54c17a',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Amber — warm amber/bronze.
    id: 'amber',
    nameKey: 'theme.name.amber',
    light: {
      '--fe-bg': '#fdf9f0',
      '--fe-bg-elev': '#f7efdd',
      '--fe-bg-hover': '#f0e3c8',
      '--fe-bg-selected': '#ead6ab',
      '--fe-border': '#e6dabf',
      '--fe-border-soft': '#efe6d3',
      '--fe-border-strong': '#c6b184',
      '--fe-text': '#33270f',
      '--fe-text-muted': '#655631',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#9a4b00',
      '--fe-primary-hover': '#7c3c00',
      '--fe-primary-soft': '#f3e5d7',
      '--fe-primary-ink': '#7c3c00',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#191307',
      '--fe-bg-elev': '#231a0c',
      '--fe-bg-hover': '#2f2312',
      '--fe-bg-selected': '#44331b',
      '--fe-border': '#3b2e17',
      '--fe-border-soft': '#302512',
      '--fe-border-strong': '#5b4a29',
      '--fe-text': '#f2e7d4',
      '--fe-text-muted': '#bda887',
      '--fe-text-on-primary': '#2a1c02',
      '--fe-primary': '#f5a524',
      '--fe-primary-hover': '#ffb84d',
      '--fe-primary-soft': '#31230c',
      '--fe-primary-ink': '#ffb84d',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Lilac — soft purple.
    id: 'lilac',
    nameKey: 'theme.name.lilac',
    light: {
      '--fe-bg': '#faf7fd',
      '--fe-bg-elev': '#f2ecfa',
      '--fe-bg-hover': '#e8ddf5',
      '--fe-bg-selected': '#dccaf0',
      '--fe-border': '#e1d6ee',
      '--fe-border-soft': '#ebe3f4',
      '--fe-border-strong': '#bda6d9',
      '--fe-text': '#241a33',
      '--fe-text-muted': '#584871',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#7231e0',
      '--fe-primary-hover': '#5f21c4',
      '--fe-primary-soft': '#e9e3fc',
      '--fe-primary-ink': '#5f21c4',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#130f1c',
      '--fe-bg-elev': '#1b1527',
      '--fe-bg-hover': '#261d37',
      '--fe-bg-selected': '#37294f',
      '--fe-border': '#302546',
      '--fe-border-soft': '#261e38',
      '--fe-border-strong': '#4b3a6d',
      '--fe-text': '#e9e2f5',
      '--fe-text-muted': '#a999c5',
      '--fe-text-on-primary': '#180d33',
      '--fe-primary': '#b197fa',
      '--fe-primary-hover': '#9c7cf4',
      '--fe-primary-soft': '#272038',
      '--fe-primary-ink': '#b197fa',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // High Contrast — black/white with strong borders, saturated accents.
    id: 'contrast',
    nameKey: 'theme.name.contrast',
    light: {
      '--fe-bg': '#ffffff',
      '--fe-bg-elev': '#f2f2f2',
      '--fe-bg-hover': '#e0e0e0',
      '--fe-bg-selected': '#c9dcff',
      '--fe-border': '#5c5c5c',
      '--fe-border-soft': '#5c5c5c',
      '--fe-border-strong': '#000000',
      '--fe-text': '#000000',
      '--fe-text-muted': '#3d3d3d',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#003d99',
      '--fe-primary-hover': '#002a6b',
      '--fe-primary-soft': '#e7edf7',
      '--fe-primary-ink': '#002a6b',
      '--fe-danger': '#a80000',
      '--fe-danger-hover': '#7d0000',
      '--fe-shadow': '0 0 0 1px #000000, 0 10px 32px rgba(0, 0, 0, 0.25)',
      '--fe-shadow-sm': '0 0 0 1px #000000',
    },
    dark: {
      '--fe-bg': '#000000',
      '--fe-bg-elev': '#0d0d0d',
      '--fe-bg-hover': '#212121',
      '--fe-bg-selected': '#003d80',
      '--fe-border': '#8f8f8f',
      '--fe-border-soft': '#8f8f8f',
      '--fe-border-strong': '#ffffff',
      '--fe-text': '#ffffff',
      '--fe-text-muted': '#d6d6d6',
      '--fe-text-on-primary': '#001430',
      '--fe-primary': '#7ab8ff',
      '--fe-primary-hover': '#9ccaff',
      '--fe-primary-soft': '#0e1a29',
      '--fe-primary-ink': '#9ccaff',
      '--fe-danger': '#ff7575',
      '--fe-danger-hover': '#ff9999',
      '--fe-shadow': '0 0 0 1px #ffffff, 0 12px 32px rgba(0, 0, 0, 0.65)',
      '--fe-shadow-sm': '0 0 0 1px #ffffff',
    },
  },
  {
    // Soft Gray — desaturated, quiet neutral.
    id: 'gray',
    nameKey: 'theme.name.gray',
    light: {
      '--fe-bg': '#f7f7f8',
      '--fe-bg-elev': '#efeff1',
      '--fe-bg-hover': '#e4e4e7',
      '--fe-bg-selected': '#d6d7db',
      '--fe-border': '#dfdfe2',
      '--fe-border-soft': '#e9e9eb',
      '--fe-border-strong': '#b9b9c0',
      '--fe-text': '#26272b',
      '--fe-text-muted': '#585a63',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#4b4b54',
      '--fe-primary-hover': '#38383f',
      '--fe-primary-soft': '#e4e4e6',
      '--fe-primary-ink': '#38383f',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#131316',
      '--fe-bg-elev': '#1b1b1f',
      '--fe-bg-hover': '#26262b',
      '--fe-bg-selected': '#35353c',
      '--fe-border': '#2d2d33',
      '--fe-border-soft': '#242429',
      '--fe-border-strong': '#4a4a52',
      '--fe-text': '#e6e6e9',
      '--fe-text-muted': '#a4a4ad',
      '--fe-text-on-primary': '#17171b',
      '--fe-primary': '#b0b0ba',
      '--fe-primary-hover': '#c4c4cd',
      '--fe-primary-soft': '#242428',
      '--fe-primary-ink': '#c4c4cd',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Terminal Green — phosphor green, monospace face for the full CRT vibe.
    id: 'terminal',
    nameKey: 'theme.name.terminal',
    light: {
      '--fe-bg': '#f3f9f4',
      '--fe-bg-elev': '#e5f2e8',
      '--fe-bg-hover': '#d5ead9',
      '--fe-bg-selected': '#bce3c6',
      '--fe-border': '#cbe2d1',
      '--fe-border-soft': '#dbebdf',
      '--fe-border-strong': '#94c2a0',
      '--fe-text': '#0c2913',
      '--fe-text-muted': '#31573c',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#116b33',
      '--fe-primary-hover': '#0c5427',
      '--fe-primary-soft': '#dbe9dd',
      '--fe-primary-ink': '#0c5427',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
      '--fe-font': 'ui-monospace, "SF Mono", Consolas, Menlo, monospace',
    },
    dark: {
      '--fe-bg': '#050d07',
      '--fe-bg-elev': '#0a1710',
      '--fe-bg-hover': '#112418',
      '--fe-bg-selected': '#1a3a26',
      '--fe-border': '#1c3a2a',
      '--fe-border-soft': '#142b1e',
      '--fe-border-strong': '#316144',
      '--fe-text': '#4fdd8b',
      '--fe-text-muted': '#38a668',
      '--fe-text-on-primary': '#03180a',
      '--fe-primary': '#25c95e',
      '--fe-primary-hover': '#4fdd8b',
      '--fe-primary-soft': '#0c2412',
      '--fe-primary-ink': '#4fdd8b',
      '--fe-danger': '#ff6b62',
      '--fe-danger-hover': '#ff8d86',
      '--fe-font': 'ui-monospace, "SF Mono", Consolas, Menlo, monospace',
    },
  },
];

/* ------------------------------------------------------------------ */
/* tema:v1 — operator-defined themes                                   */
/* ------------------------------------------------------------------ */

/**
 * The instance's own themes, fetched once per page load from the public
 * `GET /api/appearance` and published here.
 *
 * ⚠⚠ THE SERVER'S LIST IS THE ONLY DEFINITION OF "EXISTS", and that is what
 * makes deleting a theme safe. Nothing goes and rewrites the stored choice of
 * everybody who was using a deleted theme — there is more than one place such
 * a choice can live (this browser's localStorage, a per-account document on
 * the server) and a cleanup pass that missed one would leave somebody pinned
 * to a palette that no longer paints anything. Instead every lookup RESOLVES:
 * `themeById` returns undefined for an id the server did not send, and every
 * caller already treats undefined as "the stock palette".
 */
const customThemes: Ref<ThemeDef[]> = ref([]);

/**
 * Replace the operator-defined themes. Called once at boot with the payload
 * from `/api/appearance`.
 *
 * ⚠ It also RE-RESOLVES the active selection, which is the client half of
 * "deleting a theme puts anybody using it back on the default": a browser
 * holding `custom:acme` in localStorage keeps it across the fetch, and the
 * moment a list arrives that does not contain it, the selection drops to
 * `default`.
 *
 * ⚠⚠ The correction is LOCAL — `applyStoredPalette`, never `setTheme`. Third
 * merge repair, and the most destructive of the three had it been left.
 * `setTheme` now writes the account, so correcting through it would send a
 * fact the SERVER just told us straight back to the server, as though the
 * person had chosen it. That is harmless only when the correction is right.
 * It is not always right: this browser's mirror can hold a stale id from
 * before the account was read, the two fetches have no ordering, and a
 * correction fired from that stale value would overwrite the account's real
 * choice — on every device — for a theme that was never deleted.
 *
 * Leaving the account's dead id untouched costs nothing: every read resolves
 * it to the stock palette anyway, which is the whole point of building
 * deletion as resolution rather than as a cleanup pass. The first deliberate
 * pick the person makes writes a live id over it.
 */
export function setCustomThemes(defs: ThemeDef[]): void {
  customThemes.value = Array.isArray(defs) ? defs : [];
  if (themeId.value !== DEFAULT_THEME_ID && !themeById(themeId.value)) {
    applyStoredPalette(DEFAULT_THEME_ID);
  }
}

/** Reactive handle for components that render the palette list. */
export function useCustomThemes(): { customThemes: Ref<ThemeDef[]> } {
  return { customThemes };
}

/**
 * Every palette a person may pick: the built-ins, then the instance's own.
 *
 * ⚠ Operator themes come LAST and are never interleaved. The built-in order
 * is the order of the gallery cards people have learned, and a theme named
 * "Amber Kurumsal" sorting itself between Amber and Lilac would move the card
 * under somebody's cursor the first time a theme was added.
 */
export function allThemes(): ThemeDef[] {
  return [...THEMES, ...customThemes.value];
}

export function themeById(id: string | null | undefined): ThemeDef | undefined {
  if (!id) return undefined;
  return THEMES.find((t) => t.id === id) ?? customThemes.value.find((t) => t.id === id);
}

/** The display name of a theme, translated for built-ins and literal for an
 *  operator's own. */
export function themeName(theme: ThemeDef, t: (key: string) => string): string {
  return theme.name ?? (theme.nameKey ? t(theme.nameKey) : theme.id);
}

/**
 * Every token key ANY theme touches — used to fully clear inline overrides
 * when switching themes (a theme that skips a token must not inherit the
 * previous theme's value for it).
 *
 * ⚠⚠ A FUNCTION, not the constant it used to be. Computed once at module load
 * it covered only the built-in palettes, so switching FROM an operator theme
 * that set a token no built-in declares would leave that token's inline value
 * on the root element — the new palette applied, and one stray colour from the
 * old one survived it. It has to be recomputed against the list that is
 * actually installed.
 */
function allTokenKeys(): string[] {
  return Array.from(
    new Set(allThemes().flatMap((t) => [...Object.keys(t.light), ...Object.keys(t.dark)])),
  );
}

/* ------------------------------------------------------------------ */
/* Shared reactive state + persistence                                 */
/* ------------------------------------------------------------------ */

/**
 * Whether an id is worth holding on to before the server's theme list has
 * arrived.
 *
 * ⚠ `themeById` cannot answer this at module load: the operator themes are
 * fetched over the network and the first paint happens long before they land.
 * A boot check that rejected every `custom:` id would silently reset the
 * palette of anybody whose choice was an operator theme, on every page load —
 * the preference would look like it never saved. So a prefixed id is kept on
 * trust here and re-resolved for real by `setCustomThemes` once the list is
 * in. Until then nothing paints it, which is the stock palette: correct, and
 * the same thing the person would see anyway while the fetch is in flight.
 */
function plausibleThemeId(v: string | null): boolean {
  return !!v && (!!themeById(v) || v.startsWith(CUSTOM_THEME_PREFIX));
}

function readStoredThemeId(): string {
  // ⚠⚠ THE MIRROR BELONGS TO A PERSON, so with nobody signed in it is not read
  // at all (`lib/prefs` → `hasSession`). It is not a machine's palette: it is a
  // cache of ONE ACCOUNT's answer, and handing it to a signed-out window paints
  // the last person who used this browser onto the sign-in page — which is what
  // this line was measured doing on 2026-09-21, and is also how the operator's
  // own default came to be suppressed (`instanceThemes.hasOwnChoice`).
  if (!hasSession()) return DEFAULT_THEME_ID;
  try {
    const v = localStorage.getItem(THEME_LS_KEY);
    if (plausibleThemeId(v)) return v as string;
    // One-time carry-over from the shared key. `themeById` is what makes this
    // safe: 'light' / 'dark' / 'auto' are not palette ids, so a value the app's
    // mode wrote can never be mistaken for a palette here.
    const legacy = localStorage.getItem(LEGACY_THEME_LS_KEY);
    if (legacy && themeById(legacy)) {
      localStorage.setItem(THEME_LS_KEY, legacy);
      localStorage.removeItem(LEGACY_THEME_LS_KEY);
      return legacy;
    }
    return DEFAULT_THEME_ID;
  } catch {
    return DEFAULT_THEME_ID;
  }
}

// Module-level singleton so every explorer instance on the page follows the
// same selection instantly.
const themeId: Ref<string> = ref(
  typeof window === 'undefined' ? DEFAULT_THEME_ID : readStoredThemeId(),
);

/**
 * Pick a palette.
 *
 * ⚠⚠ It is saved on the ACCOUNT, not only in this browser (`lib/prefs`).
 * A palette lived in `localStorage` alone until v3, which meant a person who
 * chose one on their laptop opened the product on their phone in the stock
 * colours and had to choose again — reported by Burak as exactly that. The
 * localStorage write stays as the FIRST-PAINT cache: it is what this browser
 * reads before the account's answer can arrive, so the window does not flash
 * the default on the way to the chosen one.
 */
export function setTheme(id: string): void {
  const valid = plausibleThemeId(id) ? id : DEFAULT_THEME_ID;
  themeId.value = valid;
  try {
    // ⚠ tema:v1 — the stock palette is WRITTEN, not cleared. It used to be
    // removed, on the reasonable-sounding grounds that "no key" and "the
    // default" mean the same thing. They stopped meaning the same thing the
    // moment an instance could have a default of its own: an operator whose
    // house style is a brand theme needs to tell "this person has not chosen"
    // from "this person deliberately chose the product's own colours", and
    // with the key cleared those are the same state. The second one would
    // have had the brand theme re-applied over it on every page load, which
    // reads as a preference that will not save.
    localStorage.setItem(THEME_LS_KEY, valid);
  } catch {
    /* quota / private mode */
  }
  // ⚠⚠ `valid`, NEVER ''. This line is a merge repair and it must not go back.
  // `THEME_LS_KEY` and `PREF_LS_KEYS.palette` are the SAME string,
  // 'filex.palette'; `savePref` mirrors into local storage and `setLocalPref`
  // DELETES the key when handed an empty value. So a `'' for stock` ternary
  // here wrote the key on the line above and erased it on this one:
  // `setTheme('default')` left no trace of itself, the account learned
  // nothing, and "deliberately chose the product's own colours" collapsed back
  // into "has never chosen" — the very state an instance default paints over,
  // on every page load. "Never chosen" is the key being ABSENT, and nothing
  // else.
  savePref('palette', valid);
}

/**
 * The account's palette has arrived: paint it, without sending it back.
 *
 * ⚠ Separate from `setTheme` on purpose. Echoing a value that came FROM the
 * server back TO it is how a hydration turns into a write, and a write into
 * another device's surprise.
 *
 * ⚠⚠ `plausibleThemeId`, not `themeById` — the second merge repair, and the
 * race it closes is worth spelling out. The account's answer and the operator
 * themes are two independent fetches with no ordering between them. When the
 * account arrives first and says `custom:acme`, `themeById` cannot know that
 * id yet (the list is still in flight), would call it invalid, and would drop
 * the person onto the stock palette — writing that demotion into this
 * browser's mirror. `setCustomThemes` then finds the selection already at the
 * default and sees nothing to correct, so the choice is gone rather than
 * restored. Holding a prefixed id on trust and letting the list resolve it for
 * real is the same contract `readStoredThemeId` already follows at boot.
 */
export function applyStoredPalette(id: string | undefined | null): void {
  // ⚠⚠ NOTHING is not an ANSWER. An account that carries no `palette` key has
  // never been asked the question on this server; it has not said "the stock
  // palette". Treating the two the same is how e2e 98-custom-theme.spec.ts:97
  // went red: the browser painted its own `custom:e2e-acme` at boot, then
  // `App.vue` hydrated an account document with no palette in it and called
  // this function with `''`, which resolved to the stock id and overwrote both
  // the selection AND this browser's mirror. `expect.poll(--fe-primary)` won
  // the race against the fetch and the unpolled `--fe-bg` read on the very
  // next line lost it, which is exactly what a flake caused by a real bug
  // looks like.
  //
  // ⚠ The other three preferences already got this right and only the palette
  // did not — `applyAccountTheme` (web/src/lib/theme.ts), `applyAccountDensity`
  // (web/src/lib/density.ts) and `applyPrefLocale` (web/src/i18n) each return
  // early on a value the account does not carry. This was the odd one out.
  //
  // ⚠⚠ Silence is not the same as an invalid id, either: `setCustomThemes`
  // deliberately calls this with `DEFAULT_THEME_ID` to demote somebody off a
  // theme the operator deleted, and that IS an answer and must still paint.
  if (!id) return;
  const valid = plausibleThemeId(id) ? id : DEFAULT_THEME_ID;
  themeId.value = valid;
  setLocalPref('palette', valid);
}

/**
 * Paint a palette this person did not choose — the instance's own default,
 * applied to somebody who has never expressed a preference.
 *
 * ⚠⚠ It records NOTHING: not the account, not this browser's mirror. An
 * instance default is the operator's standing answer, not a personal choice,
 * and the difference is load-bearing in both directions. Written to the
 * account, it would overwrite on one device a choice the person made on
 * another. Written to the mirror, it would make `hasOwnChoice()` true, so the
 * day the operator picks a different house theme every existing person would
 * stay pinned to the old one, permanently, with no way to tell why.
 */
export function applyInstanceDefault(id: string | undefined | null): void {
  if (id && plausibleThemeId(id)) themeId.value = id;
}

/** Reactive handle for components: `{ themeId, setTheme }`. */
export function useThemeState(): { themeId: Ref<string>; setTheme: (id: string) => void } {
  return { themeId, setTheme };
}

/* ------------------------------------------------------------------ */
/* Light / dark MODE preference                                        */
/* ------------------------------------------------------------------ */

/**
 * The user's own light/dark choice — a different question from WHICH theme
 * paints (that is `themeId` above).
 *
 * `'host'` is the default and means "whatever the embedder passed in
 * `config.theme`". That is what keeps every existing embed unchanged: work and
 * the fishapp hand the explorer the mode their own page is in, and an explorer
 * that silently overrode it would sit as a dark rectangle inside a light page.
 * The other three are an explicit choice by the person looking at it and
 * outrank the host: `'auto'` follows the operating system, `'light'`/`'dark'`
 * are fixed.
 */
export type ThemeModePref = 'host' | 'auto' | 'light' | 'dark';

export const THEME_MODE_LS_KEY = 'filex.thememode';

/**
 * ⚠ It is a PERSON's key, so a sign-out takes it with the rest
 * (`lib/prefs` → `forgetPersonalPrefs`). Registered from here rather than
 * listed over there because a storage key written down in two modules is the
 * drift `lib/density`' header was written about.
 */
registerPersonalMirror(THEME_MODE_LS_KEY);

const MODE_VALUES: ThemeModePref[] = ['host', 'auto', 'light', 'dark'];

function readStoredThemeMode(): ThemeModePref {
  // ⚠⚠ Same rule as the palette, same reason: light or dark is a PERSON's
  // answer. `'host'` is the neutral one — the explorer then falls back to
  // `config.theme || 'auto'` (FileExplorer.vue) and `'auto'` is
  // `prefers-color-scheme`, which is exactly what the owner asked a signed-out
  // window to follow.
  if (!hasSession()) return 'host';
  try {
    const v = localStorage.getItem(THEME_MODE_LS_KEY) as ThemeModePref | null;
    return v && MODE_VALUES.includes(v) ? v : 'host';
  } catch {
    return 'host';
  }
}

const themeMode: Ref<ThemeModePref> = ref(
  typeof window === 'undefined' ? 'host' : readStoredThemeMode(),
);

export function setThemeMode(mode: ThemeModePref): void {
  const valid = MODE_VALUES.includes(mode) ? mode : 'host';
  themeMode.value = valid;
  try {
    if (valid === 'host') localStorage.removeItem(THEME_MODE_LS_KEY);
    else localStorage.setItem(THEME_MODE_LS_KEY, valid);
  } catch {
    /* quota / private mode */
  }
}

/**
 * The session answer changed — re-decide whose look this window wears.
 *
 * ⚠⚠ Needed because the two reads above happen at MODULE LOAD, and the only
 * synchronous evidence available then is `hasSession()`'s stored hint. Two
 * things can contradict it later and both must be obeyed:
 *   • the document turns out to be a public link (`sealSessionless` in the
 *     host's boot), and
 *   • `/api/auth/me` answers "nobody" on a browser whose hint said otherwise —
 *     a session that expired, or somebody who closed the tab instead of
 *     signing out. That is the shared-machine case, and leaving it unhandled
 *     would keep the leak alive for exactly the people most likely to hit it.
 *
 * ⚠ It re-READS rather than assuming: called after a sign-in it restores the
 * person's own palette from the mirror, so the function is one answer to the
 * question, not a one-way demotion.
 *
 * ⚠ It touches no storage. Nothing here is a choice anybody made.
 */
export function resolveSessionLook(): void {
  themeId.value = readStoredThemeId();
  themeMode.value = readStoredThemeMode();
}

/** Reactive handle for components: `{ themeMode, setThemeMode }`. */
export function useThemeModeState(): {
  themeMode: Ref<ThemeModePref>;
  setThemeMode: (mode: ThemeModePref) => void;
} {
  return { themeMode, setThemeMode };
}

// Cross-tab sync, same contract as the theme id above.
//
// ⚠⚠ Gated on the session for the same reason the boot read is. A `storage`
// event is broadcast to EVERY tab on the origin, so a signed-in tab changing
// the mode would otherwise push that person's answer into a tab sitting on the
// sign-in page or on somebody's share link — the leak coming back through a
// side door after the front one was shut.
if (typeof window !== 'undefined') {
  try {
    window.addEventListener('storage', (e) => {
      if (e.key !== THEME_MODE_LS_KEY || !hasSession()) return;
      const v = e.newValue as ThemeModePref | null;
      themeMode.value = v && MODE_VALUES.includes(v) ? v : 'host';
    });
  } catch {
    /* non-browser env */
  }
}

// Cross-tab sync — another tab changing the preference updates this one live.
if (typeof window !== 'undefined') {
  try {
    window.addEventListener('storage', (e) => {
      // ⚠ `hasSession()` — see the mode listener above. Same event, same
      // origin-wide broadcast, same leak.
      if (e.key !== THEME_LS_KEY || !hasSession()) return;
      themeId.value = plausibleThemeId(e.newValue) ? (e.newValue as string) : DEFAULT_THEME_ID;
    });
  } catch {
    /* non-browser env */
  }
}

/* ------------------------------------------------------------------ */
/* Application — layer 1: inline vars on the explorer root             */
/* ------------------------------------------------------------------ */

/**
 * Set (or clear, for the default theme) the selected theme's tokens as
 * inline CSS variables on the explorer root element, resolved to the
 * active variant. Inline style wins over every stylesheet rule, so the
 * root subtree is always correct regardless of host CSS.
 */
export function applyThemeToEl(el: HTMLElement, id: string, dark: boolean): void {
  for (const key of allTokenKeys()) el.style.removeProperty(key);
  if (id === DEFAULT_THEME_ID) return;
  const theme = themeById(id);
  if (!theme) return;
  const map = dark ? theme.dark : theme.light;
  for (const [key, value] of Object.entries(map)) el.style.setProperty(key, value);
}

/* ------------------------------------------------------------------ */
/* Application — layer 2: injected stylesheet for shadowed surfaces    */
/* ------------------------------------------------------------------ */

const STYLE_ATTR = 'data-filex-theme';

function cssDecls(map: ThemeTokenMap): string {
  return Object.entries(map)
    .map(([k, v]) => `${k}:${v};`)
    .join('');
}

/**
 * Generate a stylesheet that mirrors styles/variables.css' selector
 * cascade 1:1 (light base → explicit-dark selectors → prefers-dark media
 * block) with the theme's palette. Appended after the bundled CSS it
 * overrides every surface — teleported context menus, modal backdrops —
 * in whichever mode they resolve to, without JS having to track them.
 */
export function generateThemeCss(theme: ThemeDef): string {
  const light = cssDecls(theme.light);
  const dark = cssDecls(theme.dark);
  const darkSelectors = [
    '.fe--theme-dark',
    ":root[data-theme='dark'] .fe",
    '.fe.fe--theme-dark',
    ':root.dark',
    '.dark',
    ':root.dark .fe',
    '.dark .fe',
    '.fe-ctx-backdrop--theme-dark',
    ".fe-ctx-backdrop--theme-auto[data-prefers-dark='1']",
  ].join(',');
  const autoDarkSelectors = [
    '.fe:not(.fe--theme-light)',
    '.fe-ctx-backdrop--theme-auto:not(.fe-ctx-backdrop--theme-light)',
  ].join(',');
  return (
    `/* filex theme: ${theme.id} */` +
    `:root,.fe{${light}}` +
    `${darkSelectors}{${dark}}` +
    `@media (prefers-color-scheme: dark){${autoDarkSelectors}{${dark}}}`
  );
}

/**
 * Create/update/empty the singleton `<style data-filex-theme>` element in
 * <head>. Idempotent — safe to call from every explorer instance.
 */
export function syncThemeStyle(id: string): void {
  if (typeof document === 'undefined') return;
  let el = document.head.querySelector<HTMLStyleElement>(`style[${STYLE_ATTR}]`);
  const theme = id === DEFAULT_THEME_ID ? undefined : themeById(id);
  if (!theme) {
    if (el) el.textContent = '';
    return;
  }
  if (!el) {
    el = document.createElement('style');
    el.setAttribute(STYLE_ATTR, '');
    document.head.appendChild(el);
  } else if (el !== document.head.lastElementChild) {
    // Keep it AFTER any stylesheet injected later (e.g. the webcomponent's
    // own core-CSS injection) so equal-specificity rules keep losing to us.
    document.head.appendChild(el);
  }
  const css = generateThemeCss(theme);
  if (el.textContent !== css) el.textContent = css;
}
