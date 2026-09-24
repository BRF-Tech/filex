/**
 * signaturePad — the `signature-pad` node's rules without a canvas.
 *
 * Which modes a surface offers, how big an uploaded picture may be and how
 * it is fitted, what the value on the wire looks like. The component draws;
 * this decides, so the decisions can be tested on their own.
 */
import { DEFAULT_SIGN_FONT, isSignFontKey, type SignFontKey } from './signFonts';

export const SIGNATURE_MODES = ['draw', 'type', 'upload'] as const;
export type SignatureMode = (typeof SIGNATURE_MODES)[number];

/** `data.values[id]` of a signed pad. */
export interface SignatureValue {
  png_b64: string;
  mode: SignatureMode;
  /**
   * `type` mode only: which of the five faces the signer picked
   * (`lib/signFonts`).
   *
   * ⚠ It travels even though the PNG is already rendered in that face,
   * because the plugin may re-render the name at print resolution rather
   * than upscale a 600×200 bitmap — and it cannot ask the screen which
   * font was used after the fact.
   */
  font?: SignFontKey;
}

/** An uploaded picture may weigh this much. */
export const SIGNATURE_UPLOAD_MAX_BYTES = 200 * 1024;
/** The box every signature is fitted into (CSS px). */
export const SIGNATURE_MAX_WIDTH = 600;
export const SIGNATURE_MAX_HEIGHT = 200;
/** The pad's own size when the node names none. */
export const SIGNATURE_DEFAULT_WIDTH = 480;
export const SIGNATURE_DEFAULT_HEIGHT = 160;

/** Ink is black whatever the theme: it ends up on paper, not on a screen. */
export const SIGNATURE_INK = '#111111';
/**
 * The face a typed signature is drawn in when the signer has not chosen one.
 *
 * ⚠⚠ It is a KEY from `lib/signFonts`, not a CSS stack any more. The stack
 * that used to live here named fonts the machine might happen to have
 * ('Segoe Script', 'Apple Chancery', …), so the same name signed on a Mac,
 * on Windows and on a phone came out in three different hands — and on a
 * machine with none of them, in the browser's generic cursive. The five faces
 * are shipped with the package now, so every signer gets the one they picked.
 */
export const SIGNATURE_FONT_DEFAULT: SignFontKey = DEFAULT_SIGN_FONT;

export function isSignatureMode(v: unknown): v is SignatureMode {
  return typeof v === 'string' && (SIGNATURE_MODES as readonly string[]).includes(v);
}

/** The modes the node offers, in the catalogue's order; every mode when it names none. */
export function signatureModes(raw: unknown): SignatureMode[] {
  if (!Array.isArray(raw)) return [...SIGNATURE_MODES];
  const picked = raw.filter(isSignatureMode);
  return picked.length ? SIGNATURE_MODES.filter((m) => picked.includes(m)) : [...SIGNATURE_MODES];
}

export function isSignatureValue(v: unknown): v is SignatureValue {
  const o = v as SignatureValue | null;
  return !!o && typeof o === 'object' && typeof o.png_b64 === 'string' && o.png_b64 !== '' && isSignatureMode(o.mode);
}

/** The face a value was typed in, or the default. Never `undefined`. */
export function signatureFontOf(v: unknown): SignFontKey {
  const o = v as SignatureValue | null;
  return o && isSignFontKey(o.font) ? o.font : SIGNATURE_FONT_DEFAULT;
}

/** The pad's drawing size: the node's numbers, bounded, else the defaults. */
export function padSize(width: unknown, height: unknown): { width: number; height: number } {
  const w = Number(width);
  const h = Number(height);
  return {
    width: Number.isFinite(w) && w > 0 ? Math.min(SIGNATURE_MAX_WIDTH, Math.max(120, Math.round(w))) : SIGNATURE_DEFAULT_WIDTH,
    height: Number.isFinite(h) && h > 0 ? Math.min(SIGNATURE_MAX_HEIGHT, Math.max(60, Math.round(h))) : SIGNATURE_DEFAULT_HEIGHT,
  };
}

/** `w×h` scaled down (never up) to fit inside `maxW×maxH`, aspect kept. */
export function fitWithin(w: number, h: number, maxW: number, maxH: number): { width: number; height: number } {
  if (!(w > 0) || !(h > 0)) return { width: maxW, height: maxH };
  const k = Math.min(1, maxW / w, maxH / h);
  return { width: Math.max(1, Math.round(w * k)), height: Math.max(1, Math.round(h * k)) };
}

/** `data:image/png;base64,AAAA` → `AAAA`. Anything without the prefix is returned as is. */
export function stripDataUrl(s: string): string {
  const i = s.indexOf('base64,');
  return i >= 0 ? s.slice(i + 'base64,'.length) : s;
}

export type UploadVerdict = 'ok' | 'too_big' | 'bad_type';

/** PNG or JPEG, at most the ceiling. */
export function uploadVerdict(file: { type: string; size: number }): UploadVerdict {
  if (file.type !== 'image/png' && file.type !== 'image/jpeg') return 'bad_type';
  if (file.size > SIGNATURE_UPLOAD_MAX_BYTES) return 'too_big';
  return 'ok';
}

/** The largest font size (px) at which `text` fits `maxWidth`, measured by the caller. */
export function fitFontSize(measure: (px: number) => number, maxWidth: number, maxHeight: number): number {
  let px = Math.min(96, Math.floor(maxHeight * 0.6));
  while (px > 12 && measure(px) > maxWidth) px -= 2;
  return px;
}
