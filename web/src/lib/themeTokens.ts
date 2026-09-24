/**
 * tema:v1 — what an operator composes, and what filex works out for them.
 *
 * A palette in this product is about twenty `--fe-*` values per variant
 * (packages/core/src/lib/themes.ts). Asking somebody to type forty hex codes
 * to brand their installation would be a form nobody finishes, and the tenth
 * field they gave up on is exactly the one that leaves stock blue sitting in
 * the corner of their brand. So the editor asks for TWELVE colours per
 * variant, a radius and a face, and derives the rest.
 *
 * ⚠ The derivations below are not taste: each fraction was fitted to the stock
 * palette's own relationship between the two values, measured off
 * packages/core/src/styles/variables.css. Fed the stock twelve they land
 * within 6/255 per channel of what that file declares — not bit-identical,
 * because the shipped palette was hand-tuned and a couple of its shades carry
 * a deliberate tint a single mix fraction cannot express. Six is far inside
 * "the same colour" and far outside what a drifting fraction would produce, so
 * `web/tests/api/customThemePalette.test.ts` pins that distance: a derivation
 * that drifts is caught by the palette it was derived from, rather than by
 * somebody noticing a wrong shade six months later.
 *
 * ⚠ The AUTHORED list must stay in step with `themeColorTokens` in
 * backend/internal/api/handlers/themes.go, which is the allowlist a save is
 * checked against. A token added here and not there is refused on save; added
 * there and not here it is simply never set. The same test asserts the two
 * lists match.
 */

/** A token an operator fills in by hand. */
export interface AuthoredToken {
  /** The `--fe-*` custom property. */
  key: string;
  /** i18n catalogue key for the field label. */
  labelKey: string;
  /** Which fieldset it belongs to. */
  group: 'surface' | 'text' | 'primary' | 'status';
}

/**
 * The twelve colours, in the order the editor lays them out.
 *
 * Grouped the way somebody actually thinks about a brand: the grounds, the
 * inks, the accent and its three companions, then the three signals.
 */
export const AUTHORED_TOKENS: AuthoredToken[] = [
  { key: '--fe-bg', labelKey: 'appearance.token.bg', group: 'surface' },
  { key: '--fe-bg-elev', labelKey: 'appearance.token.bgElev', group: 'surface' },
  { key: '--fe-border', labelKey: 'appearance.token.border', group: 'surface' },
  { key: '--fe-text', labelKey: 'appearance.token.text', group: 'text' },
  { key: '--fe-text-muted', labelKey: 'appearance.token.textMuted', group: 'text' },
  { key: '--fe-primary', labelKey: 'appearance.token.primary', group: 'primary' },
  { key: '--fe-primary-hover', labelKey: 'appearance.token.primaryHover', group: 'primary' },
  { key: '--fe-primary-soft', labelKey: 'appearance.token.primarySoft', group: 'primary' },
  { key: '--fe-primary-ink', labelKey: 'appearance.token.primaryInk', group: 'primary' },
  { key: '--fe-danger', labelKey: 'appearance.token.danger', group: 'status' },
  { key: '--fe-warning', labelKey: 'appearance.token.warning', group: 'status' },
  { key: '--fe-ok', labelKey: 'appearance.token.ok', group: 'status' },
];

/** The authored keys alone — the shape the backend allowlist mirrors. */
export const AUTHORED_KEYS: string[] = AUTHORED_TOKENS.map((t) => t.key);

/** A whole theme as the editor holds it while somebody is typing. */
export interface ThemeDraft {
  key: string;
  name: string;
  /** Authored colours, light variant. */
  light: Record<string, string>;
  /** Authored colours, dark variant. */
  dark: Record<string, string>;
  /** Shared metrics — set once, written into both variants. */
  radius: string;
  font: string;
}

/* ------------------------------------------------------------------ */
/* Colour maths                                                        */
/* ------------------------------------------------------------------ */

/** Parse `#rgb` / `#rrggbb` into 0-255 components. Returns null if unparseable. */
export function parseHex(hex: string): [number, number, number] | null {
  const h = (hex ?? '').trim().replace(/^#/, '');
  if (/^[0-9a-fA-F]{3}$/.test(h)) {
    return [
      parseInt(h[0] + h[0], 16),
      parseInt(h[1] + h[1], 16),
      parseInt(h[2] + h[2], 16),
    ];
  }
  if (/^[0-9a-fA-F]{6}$/.test(h)) {
    return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
  }
  return null;
}

function toHex(rgb: [number, number, number]): string {
  return (
    '#' +
    rgb
      .map((v) => Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0'))
      .join('')
  );
}

/** `amount` of `b` mixed into `a`. mix(x, y, 0) === x. */
export function mix(a: string, b: string, amount: number): string {
  const ca = parseHex(a);
  const cb = parseHex(b);
  if (!ca || !cb) return a;
  const f = Math.max(0, Math.min(1, amount));
  return toHex([
    ca[0] + (cb[0] - ca[0]) * f,
    ca[1] + (cb[1] - ca[1]) * f,
    ca[2] + (cb[2] - ca[2]) * f,
  ]);
}

/** Scale a colour toward black. */
export function darken(hex: string, factor: number): string {
  const c = parseHex(hex);
  if (!c) return hex;
  return toHex([c[0] * factor, c[1] * factor, c[2] * factor]);
}

/** WCAG relative luminance. */
export function luminance(hex: string): number {
  const c = parseHex(hex);
  if (!c) return 0;
  const [r, g, b] = c.map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG contrast ratio between two colours, 1..21. */
export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/* ------------------------------------------------------------------ */
/* Derivation                                                          */
/* ------------------------------------------------------------------ */

/** The fractions, kept together so the comment above can point at one place. */
const DERIVE = {
  /** Row hover: the ground nudged toward the ink. Reproduces #f3f4f6 / #1f232a. */
  hover: 0.05,
  /** Selected row: the ground tinted with the accent. */
  selectedLight: 0.1,
  selectedDark: 0.18,
  /** The quieter separator: the border pulled back toward the ground. */
  borderSoftLight: 0.4,
  borderSoftDark: 0.32,
  /** The louder separator: the border pushed toward the ink. */
  borderStrongLight: 0.12,
  borderStrongDark: 0.08,
  /** The pressed state of a destructive button. */
  dangerHover: 0.85,
} as const;

/**
 * Turn the authored twelve into the full token map for one variant.
 *
 * ⚠ `--fe-text-on-primary` is chosen by CONTRAST, not by a fixed white. The
 * stock dark palette carries `#15171c` there with a comment explaining why:
 * white on its light-blue button measures 3.16:1, under the 4.5 a label on a
 * filled control needs. An operator picking a pale accent would hit exactly
 * that, and would have no way to know — so the editor measures both
 * candidates (white, and the theme's own page ground) and keeps the better
 * one. Applied to the stock twelve this returns `#ffffff` in light and
 * `#15171c` in dark, which is what variables.css already says.
 */
export function deriveTokens(
  authored: Record<string, string>,
  opts: { dark: boolean; radius: string; font: string },
): Record<string, string> {
  const bg = authored['--fe-bg'] ?? '#ffffff';
  const text = authored['--fe-text'] ?? '#000000';
  const border = authored['--fe-border'] ?? '#e5e7eb';
  const primary = authored['--fe-primary'] ?? '#2f6ceb';
  const danger = authored['--fe-danger'] ?? '#dc2626';
  const ok = authored['--fe-ok'] ?? '#16a34a';

  const onPrimary = contrast(primary, '#ffffff') >= contrast(primary, bg) ? '#ffffff' : bg;

  const r = radiusPx(opts.radius);

  return {
    ...authored,
    '--fe-bg-hover': mix(bg, text, DERIVE.hover),
    '--fe-bg-selected': mix(bg, primary, opts.dark ? DERIVE.selectedDark : DERIVE.selectedLight),
    '--fe-border-soft': mix(border, bg, opts.dark ? DERIVE.borderSoftDark : DERIVE.borderSoftLight),
    '--fe-border-strong': mix(
      border,
      text,
      opts.dark ? DERIVE.borderStrongDark : DERIVE.borderStrongLight,
    ),
    '--fe-text-on-primary': onPrimary,
    '--fe-danger-hover': darken(danger, DERIVE.dangerHover),
    // ⚠ The "kept on this computer" check follows the success colour rather
    // than being a thirteenth field. It is the same signal said twice, and an
    // operator who set them to different greens would have produced a bug,
    // not a palette.
    '--fe-keep-ok': ok,
    // Metrics. The three companions are offsets from the one radius the
    // operator sets, which is exactly the stock relationship (8 / 6 / 10 / 12).
    '--fe-radius': `${r}px`,
    '--fe-radius-sm': `${Math.max(0, r - 2)}px`,
    '--fe-radius-md': `${r + 2}px`,
    '--fe-radius-lg': `${r + 4}px`,
    '--fe-font': opts.font,
  };
}

/** Clamp the typed radius to something the backend's length pattern accepts. */
export function radiusPx(value: string): number {
  const n = Number.parseFloat(String(value).replace('px', ''));
  if (!Number.isFinite(n)) return 8;
  return Math.max(0, Math.min(24, Math.round(n)));
}

/** Both variants of a draft, ready to store or preview. */
export function draftToTokens(draft: ThemeDraft): {
  light: Record<string, string>;
  dark: Record<string, string>;
} {
  return {
    light: deriveTokens(draft.light, { dark: false, radius: draft.radius, font: draft.font }),
    dark: deriveTokens(draft.dark, { dark: true, radius: draft.radius, font: draft.font }),
  };
}

/* ------------------------------------------------------------------ */
/* Starting point                                                      */
/* ------------------------------------------------------------------ */

/**
 * A new theme starts as the PRODUCT's own palette rather than as twelve empty
 * fields.
 *
 * ⚠ Deliberate: an operator branding an installation changes three or four
 * colours — the accent, maybe the grounds — and wants the rest to keep
 * working. Starting empty would make the first save fail validation (every
 * authored colour is required, in both variants) and would teach them that
 * this screen is a chore.
 */
export const STOCK_LIGHT: Record<string, string> = {
  '--fe-bg': '#ffffff',
  '--fe-bg-elev': '#f7f8fb',
  '--fe-border': '#e5e7eb',
  '--fe-text': '#1f2937',
  '--fe-text-muted': '#6b7280',
  '--fe-primary': '#2f6ceb',
  '--fe-primary-hover': '#2559c9',
  '--fe-primary-soft': '#e6ecfa',
  '--fe-primary-ink': '#2559c9',
  '--fe-danger': '#dc2626',
  '--fe-warning': '#f59e0b',
  '--fe-ok': '#16a34a',
};

export const STOCK_DARK: Record<string, string> = {
  '--fe-bg': '#15171c',
  '--fe-bg-elev': '#1a1d23',
  '--fe-border': '#2e333c',
  '--fe-text': '#e6e8ec',
  '--fe-text-muted': '#888f9b',
  '--fe-primary': '#5b8cff',
  '--fe-primary-hover': '#7ba3ff',
  '--fe-primary-soft': '#1c2740',
  '--fe-primary-ink': '#7ba3ff',
  '--fe-danger': '#f87171',
  '--fe-warning': '#f59e0b',
  '--fe-ok': '#16a34a',
};

export const DEFAULT_FONT = 'Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';

/** A blank draft — the stock palette under a new name. */
export function newDraft(): ThemeDraft {
  return {
    key: '',
    name: '',
    light: { ...STOCK_LIGHT },
    dark: { ...STOCK_DARK },
    radius: '8',
    font: DEFAULT_FONT,
  };
}

/** Turn a stored theme back into an editable draft. */
export function draftFromStored(t: {
  key: string;
  name: string;
  light: Record<string, string>;
  dark: Record<string, string>;
}): ThemeDraft {
  const pick = (m: Record<string, string>, fallback: Record<string, string>) => {
    const out: Record<string, string> = {};
    for (const k of AUTHORED_KEYS) out[k] = m?.[k] ?? fallback[k];
    return out;
  };
  return {
    key: t.key,
    name: t.name,
    light: pick(t.light, STOCK_LIGHT),
    dark: pick(t.dark, STOCK_DARK),
    radius: String(radiusPx(t.light?.['--fe-radius'] ?? '8px')),
    font: t.light?.['--fe-font'] ?? DEFAULT_FONT,
  };
}

/* ------------------------------------------------------------------ */
/* Validation, client-side                                             */
/* ------------------------------------------------------------------ */

/** The slug rule, mirroring `themeKeyRe` in handlers/themes.go. */
export const THEME_KEY_RE = /^[a-z0-9][a-z0-9-]{0,39}$/;

/** Suggest a slug from a display name, so nobody has to invent one. */
export function slugify(name: string): string {
  const map: Record<string, string> = {
    ı: 'i', İ: 'i', ş: 's', Ş: 's', ğ: 'g', Ğ: 'g',
    ü: 'u', Ü: 'u', ö: 'o', Ö: 'o', ç: 'c', Ç: 'c',
  };
  return (name ?? '')
    .split('')
    .map((ch) => map[ch] ?? ch)
    .join('')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40);
}

/** The colours whose contrast the editor warns about, with the bar each must
 *  clear. Warnings, never refusals: an operator's brand is theirs, and a
 *  screen that refused to save an off-spec pairing would just be worked
 *  around with the raw-CSS hatch — which has no checks at all. */
export const CONTRAST_CHECKS: { fg: string; bg: string; min: number; labelKey: string }[] = [
  { fg: '--fe-text', bg: '--fe-bg', min: 7, labelKey: 'appearance.contrast.text' },
  { fg: '--fe-text-muted', bg: '--fe-bg', min: 4.5, labelKey: 'appearance.contrast.muted' },
  { fg: '--fe-primary-ink', bg: '--fe-primary-soft', min: 4.5, labelKey: 'appearance.contrast.ink' },
  { fg: '--fe-primary', bg: '--fe-bg', min: 3, labelKey: 'appearance.contrast.primary' },
  { fg: '--fe-danger', bg: '--fe-bg', min: 3, labelKey: 'appearance.contrast.danger' },
];

export interface ContrastWarning {
  labelKey: string;
  ratio: number;
  min: number;
}

/** Which pairings in one variant fall short. */
export function contrastWarnings(tokens: Record<string, string>): ContrastWarning[] {
  const out: ContrastWarning[] = [];
  for (const c of CONTRAST_CHECKS) {
    const fg = tokens[c.fg];
    const bg = tokens[c.bg];
    if (!parseHex(fg) || !parseHex(bg)) continue;
    const ratio = contrast(fg, bg);
    if (ratio < c.min) out.push({ labelKey: c.labelKey, ratio, min: c.min });
  }
  return out;
}
