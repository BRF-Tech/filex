/**
 * signFonts — the closed set of faces a signature may be written in.
 *
 * A typed signature and a `pdf-fields` text field both let the signer choose
 * how their words are drawn, and the choice TRAVELS: it rides back to the
 * plugin as `font` (see docs/APP-PLUGINS-API.md → `pdf-fields` props), which
 * then stamps the PDF in that face. So the browser and the stamping plugin
 * have to agree on a small, stable vocabulary — five keys, not a free-text
 * CSS family — and this file is that vocabulary.
 *
 * ⚠ ONE definition, three readers: the signature pad's "type" mode, the
 * pdf-fields overlay and anything that later re-renders a stored value. A
 * second copy is how a signature typed in Caveat comes back in Inter.
 *
 * ⚠ The faces themselves are self-hosted (`styles/sign-fonts.css` +
 * `assets/fonts/`), never a CDN: see that stylesheet's note for why a signing
 * screen in particular must not phone out for a font.
 */

/** The wire value of a `font` choice. */
export type SignFontKey = 'caveat' | 'dancing-script' | 'homemade-apple' | 'inter' | 'source-serif';

export interface SignFont {
  key: SignFontKey;
  /** What the person sees in the picker — a font's name is not translated. */
  name: string;
  /** Handwriting faces are offered first; the formal pair closes the list. */
  kind: 'hand' | 'formal';
  /** The class `styles/sign-fonts.css` declares for this key. */
  className: string;
  /**
   * The same stack as a string, for a canvas `ctx.font` (canvas takes no
   * class). ⚠ `inter` resolves through `var(--fe-font)` in CSS but a canvas
   * cannot read a custom property, so its literal stack is spelled out here
   * and kept in step with `styles/variables.css`.
   */
  stack: string;
}

/** Handwriting first, formal last — the order the picker draws. */
export const SIGN_FONTS: readonly SignFont[] = [
  { key: 'caveat', name: 'Caveat', kind: 'hand', className: 'fe-signfont--caveat', stack: "'Caveat', cursive" },
  {
    key: 'dancing-script',
    name: 'Dancing Script',
    kind: 'hand',
    className: 'fe-signfont--dancing-script',
    stack: "'Dancing Script', cursive",
  },
  {
    key: 'homemade-apple',
    name: 'Homemade Apple',
    kind: 'hand',
    className: 'fe-signfont--homemade-apple',
    stack: "'Homemade Apple', cursive",
  },
  {
    key: 'inter',
    name: 'Inter',
    kind: 'formal',
    className: 'fe-signfont--inter',
    stack: 'Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  },
  {
    key: 'source-serif',
    name: 'Source Serif 4',
    kind: 'formal',
    className: 'fe-signfont--source-serif',
    stack: "'Source Serif 4', Georgia, 'Times New Roman', serif",
  },
];

/** The face a screen starts on when the plugin named none. */
export const DEFAULT_SIGN_FONT: SignFontKey = 'caveat';

/** The named face, or the default — never `undefined`, so no caller branches. */
export function signFont(key: unknown): SignFont {
  const k = String(key ?? '');
  return SIGN_FONTS.find((f) => f.key === k) ?? SIGN_FONTS.find((f) => f.key === DEFAULT_SIGN_FONT)!;
}

/** Is this one of the five? (A value off the wire is not trusted.) */
export function isSignFontKey(v: unknown): v is SignFontKey {
  return typeof v === 'string' && SIGN_FONTS.some((f) => f.key === v);
}
