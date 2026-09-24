/**
 * pdfFieldsGeom — where a `pdf-fields` box is, as pure arithmetic.
 *
 * A field's place is stored as FRACTIONS of the page's rendered viewport
 * (0..1, origin top-left, CropBox and /Rotate already applied by pdf.js's
 * viewport), so it survives any zoom and any screen. Three conversions are
 * needed around that and they all live here, DOM-free:
 *
 *   fraction ↔ pixel   the drag on screen (a page drawn `size` CSS px wide)
 *   fraction ↔ PDF     the rectangle in PDF user space (points, origin
 *                      bottom-left of the UNROTATED page) that a stamping
 *                      plugin needs, for /Rotate 0, 90, 180 and 270
 *
 * The rotation maps are the ones pdf.js's `PageViewport` applies: 90 is the
 * page turned clockwise on screen, so the unrotated top-left corner lands at
 * the rendered top-right; 270 turns it counter-clockwise.
 */

/** A box as fractions of the rendered page (0..1, origin top-left). */
export interface FracRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** A box in CSS pixels on a drawn page. */
export interface PixelRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

/** A box in PDF user space: points, origin bottom-left of the unrotated page. */
export interface PdfRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** The rendered viewport at scale 1: its size after rotation, and that rotation. */
export interface PageBox {
  width: number;
  height: number;
  rotation: number;
}

export type Rotation = 0 | 90 | 180 | 270;

/** Any degree count as one of the four the PDF spec allows. */
export function normRotation(r: number | undefined | null): Rotation {
  const n = Number(r);
  if (!Number.isFinite(n)) return 0;
  const m = ((Math.round(n / 90) * 90) % 360 + 360) % 360;
  return m as Rotation;
}

/** The smallest box the editor lets a field shrink to (fractions). */
export const MIN_FIELD_FRAC = { w: 0.02, h: 0.01 };

const clamp01 = (v: number) => Math.min(1, Math.max(0, v));

/** Inside the page, at least the minimum size, no NaN. */
export function clampFrac(r: FracRect): FracRect {
  const w = Math.min(1, Math.max(MIN_FIELD_FRAC.w, Number(r.w) || 0));
  const h = Math.min(1, Math.max(MIN_FIELD_FRAC.h, Number(r.h) || 0));
  const x = Math.min(1 - w, clamp01(Number(r.x) || 0));
  const y = Math.min(1 - h, clamp01(Number(r.y) || 0));
  return { x, y, w, h };
}

/** A box drawn between two points (any corner order), as fractions. */
export function rectFromPoints(a: { x: number; y: number }, b: { x: number; y: number }): FracRect {
  return clampFrac({
    x: Math.min(a.x, b.x),
    y: Math.min(a.y, b.y),
    w: Math.abs(a.x - b.x),
    h: Math.abs(a.y - b.y),
  });
}

export function fracToPixel(r: FracRect, size: { width: number; height: number }): PixelRect {
  return {
    left: r.x * size.width,
    top: r.y * size.height,
    width: r.w * size.width,
    height: r.h * size.height,
  };
}

export function pixelToFrac(px: PixelRect, size: { width: number; height: number }): FracRect {
  const W = size.width || 1;
  const H = size.height || 1;
  return { x: px.left / W, y: px.top / H, w: px.width / W, h: px.height / H };
}

/** A pointer position on a drawn page (CSS px from its top-left) as fractions. */
export function pointToFrac(p: { x: number; y: number }, size: { width: number; height: number }): { x: number; y: number } {
  return { x: clamp01(p.x / (size.width || 1)), y: clamp01(p.y / (size.height || 1)) };
}

/** The unrotated page's size in points, from the rendered viewport. */
export function unrotatedSize(page: PageBox): { width: number; height: number } {
  const rot = normRotation(page.rotation);
  return rot === 90 || rot === 270 ? { width: page.height, height: page.width } : { width: page.width, height: page.height };
}

/** A rendered point (px, origin top-left) → the unrotated page's top-left system (points). */
function renderedToPage(X: number, Y: number, page: PageBox): { x: number; y: number } {
  const rot = normRotation(page.rotation);
  const W = page.width;
  const H = page.height;
  switch (rot) {
    case 90:
      return { x: Y, y: W - X };
    case 180:
      return { x: W - X, y: H - Y };
    case 270:
      return { x: H - Y, y: X };
    default:
      return { x: X, y: Y };
  }
}

/** Inverse of `renderedToPage`. */
function pageToRendered(x: number, y: number, page: PageBox): { X: number; Y: number } {
  const rot = normRotation(page.rotation);
  const W = page.width;
  const H = page.height;
  switch (rot) {
    case 90:
      return { X: W - y, Y: x };
    case 180:
      return { X: W - x, Y: H - y };
    case 270:
      return { X: y, Y: H - x };
    default:
      return { X: x, Y: y };
  }
}

/** Fractions of the rendered page → PDF user-space rectangle (points, origin bottom-left, unrotated). */
export function fracToPdf(r: FracRect, page: PageBox): PdfRect {
  const a = renderedToPage(r.x * page.width, r.y * page.height, page);
  const b = renderedToPage((r.x + r.w) * page.width, (r.y + r.h) * page.height, page);
  const { height: ph } = unrotatedSize(page);
  const left = Math.min(a.x, b.x);
  const right = Math.max(a.x, b.x);
  const top = Math.min(a.y, b.y);
  const bottom = Math.max(a.y, b.y);
  return round({ x: left, y: ph - bottom, w: right - left, h: bottom - top });
}

/** PDF user-space rectangle → fractions of the rendered page. */
export function pdfToFrac(r: PdfRect, page: PageBox): FracRect {
  const { height: ph } = unrotatedSize(page);
  const topLeft = { x: r.x, y: ph - (r.y + r.h) };
  const bottomRight = { x: r.x + r.w, y: ph - r.y };
  const a = pageToRendered(topLeft.x, topLeft.y, page);
  const b = pageToRendered(bottomRight.x, bottomRight.y, page);
  const W = page.width || 1;
  const H = page.height || 1;
  return round({
    x: Math.min(a.X, b.X) / W,
    y: Math.min(a.Y, b.Y) / H,
    w: Math.abs(a.X - b.X) / W,
    h: Math.abs(a.Y - b.Y) / H,
  });
}

function round<T extends Record<string, number>>(o: T): T {
  const out: Record<string, number> = {};
  for (const [k, v] of Object.entries(o)) out[k] = Math.round(v * 1e6) / 1e6;
  return out as T;
}

/**
 * v3 §3.2 — the width to draw a page at so the WHOLE page fits the box.
 *
 * ⚠⚠ The complaint that started the round: a signature page you had to
 * scroll in two directions. The document was fitted to the column's WIDTH, so
 * a portrait page in a landscape window ran off the bottom — the boxes were
 * then found by hunting rather than by reading. Fitting means whichever of
 * the two constraints binds.
 *
 * `gap` is the breathing room around the page (the scroll box's padding),
 * taken off the HEIGHT: a page that touches the toolbar reads as clipped.
 * A box with no height yet (before layout) falls back to the width, which is
 * the old behaviour and is corrected on the first resize observation.
 */
export function fitPageWidth(
  box: { width: number; height: number },
  page: { width: number; height: number },
  gap = 16,
): number {
  const w = Math.max(0, box.width);
  const h = Math.max(0, box.height) - gap;
  if (!page.width || !page.height || h <= 0) return w;
  const byHeight = (h * page.width) / page.height;
  return Math.max(120, Math.min(w, byHeight));
}
