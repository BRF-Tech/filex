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
 * Persistence: localStorage `filex.theme` (absent/invalid → default),
 * shared reactively across every explorer instance on the page and
 * synced across tabs via the `storage` event.
 *
 * All palettes were contrast-checked (WCAG 2.1): text/bg ≥ 7:1,
 * text on elevated/hover/selected surfaces + muted text + text-on-primary
 * ≥ 4.5:1, primary & danger vs bg ≥ 3:1 — in BOTH variants.
 */

import { ref, type Ref } from 'vue';

/** Map of `--fe-*` custom property → value. */
export type ThemeTokenMap = Record<string, string>;

export interface ThemeDef {
  /** Stable id — persisted in localStorage, never shown to users. */
  id: string;
  /** i18n catalogue key for the display name (tr + en). */
  nameKey: string;
  /** Palette applied when the resolved mode is light. */
  light: ThemeTokenMap;
  /** Palette applied when the resolved mode is dark. */
  dark: ThemeTokenMap;
}

export const THEME_LS_KEY = 'filex.theme';
export const DEFAULT_THEME_ID = 'default';

/* ------------------------------------------------------------------ */
/* Registry                                                            */
/* ------------------------------------------------------------------ */

export const THEMES: ThemeDef[] = [
  {
    // Stock palette — the maps duplicate variables.css ONLY for the
    // gallery preview; applying this theme clears all overrides.
    id: DEFAULT_THEME_ID,
    nameKey: 'theme.name.default',
    light: {
      '--fe-bg': '#ffffff',
      '--fe-bg-elev': '#f7f8fa',
      '--fe-bg-hover': '#edf0f5',
      '--fe-bg-selected': '#dfe8ff',
      '--fe-border': '#e2e6ed',
      '--fe-border-strong': '#c7ced9',
      '--fe-text': '#1a1e27',
      '--fe-text-muted': '#5a6475',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#3b82f6',
      '--fe-primary-hover': '#2563eb',
      '--fe-danger': '#dc2626',
      '--fe-danger-hover': '#b91c1c',
    },
    dark: {
      '--fe-bg': '#0f1419',
      '--fe-bg-elev': '#161c25',
      '--fe-bg-hover': '#1f2733',
      '--fe-bg-selected': '#23324a',
      '--fe-border': '#2a323e',
      '--fe-border-strong': '#3a4453',
      '--fe-text': '#e5e9f0',
      '--fe-text-muted': '#8b95a7',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#60a5fa',
      '--fe-primary-hover': '#3b82f6',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Gece Mavisi — deep indigo/navy.
    id: 'night',
    nameKey: 'theme.name.night',
    light: {
      '--fe-bg': '#f6f8fd',
      '--fe-bg-elev': '#edf1fa',
      '--fe-bg-hover': '#e2e9f7',
      '--fe-bg-selected': '#d2ddf4',
      '--fe-border': '#d6deee',
      '--fe-border-strong': '#aebfda',
      '--fe-text': '#151f38',
      '--fe-text-muted': '#49587a',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#2c4a9e',
      '--fe-primary-hover': '#213a80',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#0a1024',
      '--fe-bg-elev': '#111a35',
      '--fe-bg-hover': '#1a2547',
      '--fe-bg-selected': '#243665',
      '--fe-border': '#26335a',
      '--fe-border-strong': '#3c4e84',
      '--fe-text': '#dfe6f7',
      '--fe-text-muted': '#96a5cc',
      '--fe-text-on-primary': '#050b26',
      '--fe-primary': '#6d8dfc',
      '--fe-primary-hover': '#5273f2',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Orman — calm greens.
    id: 'forest',
    nameKey: 'theme.name.forest',
    light: {
      '--fe-bg': '#f5faf5',
      '--fe-bg-elev': '#eaf3eb',
      '--fe-bg-hover': '#dcecdf',
      '--fe-bg-selected': '#c8e2ce',
      '--fe-border': '#d2e2d4',
      '--fe-border-strong': '#a2c3a8',
      '--fe-text': '#182b1d',
      '--fe-text-muted': '#42604a',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#2b7a41',
      '--fe-primary-hover': '#226334',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#0d1611',
      '--fe-bg-elev': '#132019',
      '--fe-bg-hover': '#1b2d23',
      '--fe-bg-selected': '#264532',
      '--fe-border': '#28402f',
      '--fe-border-strong': '#3e5f49',
      '--fe-text': '#dcebe0',
      '--fe-text-muted': '#93b09b',
      '--fe-text-on-primary': '#04180b',
      '--fe-primary': '#54c17a',
      '--fe-primary-hover': '#3fae66',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Kehribar — warm amber/bronze.
    id: 'amber',
    nameKey: 'theme.name.amber',
    light: {
      '--fe-bg': '#fdf9f0',
      '--fe-bg-elev': '#f7efdd',
      '--fe-bg-hover': '#f0e3c8',
      '--fe-bg-selected': '#ead6ab',
      '--fe-border': '#e6dabf',
      '--fe-border-strong': '#c6b184',
      '--fe-text': '#33270f',
      '--fe-text-muted': '#655631',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#9a4b00',
      '--fe-primary-hover': '#7c3c00',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#191307',
      '--fe-bg-elev': '#231a0c',
      '--fe-bg-hover': '#2f2312',
      '--fe-bg-selected': '#44331b',
      '--fe-border': '#3b2e17',
      '--fe-border-strong': '#5b4a29',
      '--fe-text': '#f2e7d4',
      '--fe-text-muted': '#bda887',
      '--fe-text-on-primary': '#2a1c02',
      '--fe-primary': '#f5a524',
      '--fe-primary-hover': '#ffb84d',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Leylak — soft purple.
    id: 'lilac',
    nameKey: 'theme.name.lilac',
    light: {
      '--fe-bg': '#faf7fd',
      '--fe-bg-elev': '#f2ecfa',
      '--fe-bg-hover': '#e8ddf5',
      '--fe-bg-selected': '#dccaf0',
      '--fe-border': '#e1d6ee',
      '--fe-border-strong': '#bda6d9',
      '--fe-text': '#241a33',
      '--fe-text-muted': '#584871',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#7231e0',
      '--fe-primary-hover': '#5f21c4',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#130f1c',
      '--fe-bg-elev': '#1b1527',
      '--fe-bg-hover': '#261d37',
      '--fe-bg-selected': '#37294f',
      '--fe-border': '#302546',
      '--fe-border-strong': '#4b3a6d',
      '--fe-text': '#e9e2f5',
      '--fe-text-muted': '#a999c5',
      '--fe-text-on-primary': '#180d33',
      '--fe-primary': '#b197fa',
      '--fe-primary-hover': '#9c7cf4',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Yüksek Kontrast — black/white with strong borders, saturated accents.
    id: 'contrast',
    nameKey: 'theme.name.contrast',
    light: {
      '--fe-bg': '#ffffff',
      '--fe-bg-elev': '#f2f2f2',
      '--fe-bg-hover': '#e0e0e0',
      '--fe-bg-selected': '#c9dcff',
      '--fe-border': '#5c5c5c',
      '--fe-border-strong': '#000000',
      '--fe-text': '#000000',
      '--fe-text-muted': '#3d3d3d',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#003d99',
      '--fe-primary-hover': '#002a6b',
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
      '--fe-border-strong': '#ffffff',
      '--fe-text': '#ffffff',
      '--fe-text-muted': '#d6d6d6',
      '--fe-text-on-primary': '#001430',
      '--fe-primary': '#7ab8ff',
      '--fe-primary-hover': '#9ccaff',
      '--fe-danger': '#ff7575',
      '--fe-danger-hover': '#ff9999',
      '--fe-shadow': '0 0 0 1px #ffffff, 0 12px 32px rgba(0, 0, 0, 0.65)',
      '--fe-shadow-sm': '0 0 0 1px #ffffff',
    },
  },
  {
    // Yumuşak Gri — desaturated, quiet neutral.
    id: 'gray',
    nameKey: 'theme.name.gray',
    light: {
      '--fe-bg': '#f7f7f8',
      '--fe-bg-elev': '#efeff1',
      '--fe-bg-hover': '#e4e4e7',
      '--fe-bg-selected': '#d6d7db',
      '--fe-border': '#dfdfe2',
      '--fe-border-strong': '#b9b9c0',
      '--fe-text': '#26272b',
      '--fe-text-muted': '#585a63',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#4b4b54',
      '--fe-primary-hover': '#38383f',
      '--fe-danger': '#c62828',
      '--fe-danger-hover': '#a51f1f',
    },
    dark: {
      '--fe-bg': '#131316',
      '--fe-bg-elev': '#1b1b1f',
      '--fe-bg-hover': '#26262b',
      '--fe-bg-selected': '#35353c',
      '--fe-border': '#2d2d33',
      '--fe-border-strong': '#4a4a52',
      '--fe-text': '#e6e6e9',
      '--fe-text-muted': '#a4a4ad',
      '--fe-text-on-primary': '#17171b',
      '--fe-primary': '#b0b0ba',
      '--fe-primary-hover': '#c4c4cd',
      '--fe-danger': '#f87171',
      '--fe-danger-hover': '#ef4444',
    },
  },
  {
    // Terminal Yeşili — phosphor green, monospace face for the full CRT vibe.
    id: 'terminal',
    nameKey: 'theme.name.terminal',
    light: {
      '--fe-bg': '#f3f9f4',
      '--fe-bg-elev': '#e5f2e8',
      '--fe-bg-hover': '#d5ead9',
      '--fe-bg-selected': '#bce3c6',
      '--fe-border': '#cbe2d1',
      '--fe-border-strong': '#94c2a0',
      '--fe-text': '#0c2913',
      '--fe-text-muted': '#31573c',
      '--fe-text-on-primary': '#ffffff',
      '--fe-primary': '#116b33',
      '--fe-primary-hover': '#0c5427',
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
      '--fe-border-strong': '#316144',
      '--fe-text': '#4fdd8b',
      '--fe-text-muted': '#38a668',
      '--fe-text-on-primary': '#03180a',
      '--fe-primary': '#25c95e',
      '--fe-primary-hover': '#4fdd8b',
      '--fe-danger': '#ff6b62',
      '--fe-danger-hover': '#ff8d86',
      '--fe-font': 'ui-monospace, "SF Mono", Consolas, Menlo, monospace',
    },
  },
];

export function themeById(id: string | null | undefined): ThemeDef | undefined {
  return THEMES.find((t) => t.id === id);
}

/** Every token key ANY theme touches — used to fully clear inline overrides
 *  when switching themes (a theme that skips a token must not inherit the
 *  previous theme's value for it). */
const ALL_TOKEN_KEYS: string[] = Array.from(
  new Set(THEMES.flatMap((t) => [...Object.keys(t.light), ...Object.keys(t.dark)])),
);

/* ------------------------------------------------------------------ */
/* Shared reactive state + persistence                                 */
/* ------------------------------------------------------------------ */

function readStoredThemeId(): string {
  try {
    const v = localStorage.getItem(THEME_LS_KEY);
    return v && themeById(v) ? v : DEFAULT_THEME_ID;
  } catch {
    return DEFAULT_THEME_ID;
  }
}

// Module-level singleton so every explorer instance on the page follows the
// same selection instantly.
const themeId: Ref<string> = ref(
  typeof window === 'undefined' ? DEFAULT_THEME_ID : readStoredThemeId(),
);

export function setTheme(id: string): void {
  const valid = themeById(id) ? id : DEFAULT_THEME_ID;
  themeId.value = valid;
  try {
    if (valid === DEFAULT_THEME_ID) localStorage.removeItem(THEME_LS_KEY);
    else localStorage.setItem(THEME_LS_KEY, valid);
  } catch {
    /* quota / private mode */
  }
}

/** Reactive handle for components: `{ themeId, setTheme }`. */
export function useThemeState(): { themeId: Ref<string>; setTheme: (id: string) => void } {
  return { themeId, setTheme };
}

// Cross-tab sync — another tab changing the preference updates this one live.
if (typeof window !== 'undefined') {
  try {
    window.addEventListener('storage', (e) => {
      if (e.key !== THEME_LS_KEY) return;
      themeId.value = e.newValue && themeById(e.newValue) ? e.newValue : DEFAULT_THEME_ID;
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
  for (const key of ALL_TOKEN_KEYS) el.style.removeProperty(key);
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
