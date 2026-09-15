/**
 * accentButton — how a button filled with the operator's branding accent stays
 * readable and visible on the sign-in card, per theme (issue #29).
 *
 * "If I select OIDC login button black color almost disappearing in background
 * of login frame … Also applies to white color for button it completely
 * invisible". The accent was painted as the fill and nothing else adapted:
 *
 *   - the label used the theme's on-primary colour — dark in the dark theme,
 *     white in the light theme — so a black accent in dark mode and a white
 *     accent in light mode got a label the same colour as the fill;
 *   - the fill was never compared with the card behind it, so a near-black
 *     button sat invisible on the dark card and a near-white one on the light
 *     card.
 *
 * The reporter preferred "different design for dark theme and light theme":
 * the label is picked from the accent itself, and the button draws an edge in
 * the theme it is shown in whenever the fill does not stand out from THAT
 * theme's card.
 *
 * ⚠ The same rule, with the same thresholds, lives server-side for the public
 * share pages (backend/internal/api/handlers/branding.go, accentChrome). Change
 * both or neither.
 */

const HEX_RE = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/;

/** Card backgrounds the button sits on (`--fe-bg`, light and dark). */
export const CARD_LIGHT = '#ffffff';
export const CARD_DARK = '#15171c';
/** Label colours: the light and dark ends of the palette. */
export const ON_LIGHT_TEXT = '#15171c';
export const ON_DARK_TEXT = '#ffffff';
/** Below this contrast against the card, the fill needs an edge. */
export const MIN_EDGE_CONTRAST = 1.6;
const EDGE_ON_LIGHT = 'rgba(21, 23, 28, 0.35)';
const EDGE_ON_DARK = 'rgba(255, 255, 255, 0.45)';

function channels(hex: string): [number, number, number] {
  let h = hex.slice(1);
  if (h.length === 3) h = h.split('').map((c) => c + c).join('');
  return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)) as [number, number, number];
}

/** WCAG relative luminance of a #rgb/#rrggbb colour. */
export function luminance(hex: string): number {
  const [r, g, b] = channels(hex).map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

/** WCAG contrast ratio between two colours (1–21). */
export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

// A type alias, not an interface: Vue's `:style` binding accepts an object
// literal type but not an interface (no index signature) — vue-tsc refuses it.
export type AccentButtonStyle = {
  backgroundColor: string;
  borderColor: string;
  color: string;
};

/**
 * Inline style for an accent-filled button on the sign-in card, or `undefined`
 * when there is no valid accent (the product palette is already readable in
 * both themes).
 */
export function accentButtonStyle(accent: string | null | undefined, dark: boolean): AccentButtonStyle | undefined {
  const fill = (accent ?? '').trim();
  if (!HEX_RE.test(fill)) return undefined;
  const color = contrast(fill, ON_DARK_TEXT) >= contrast(fill, ON_LIGHT_TEXT) ? ON_DARK_TEXT : ON_LIGHT_TEXT;
  const card = dark ? CARD_DARK : CARD_LIGHT;
  const borderColor = contrast(fill, card) < MIN_EDGE_CONTRAST ? (dark ? EDGE_ON_DARK : EDGE_ON_LIGHT) : fill;
  return { backgroundColor: fill, borderColor, color };
}
