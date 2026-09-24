// pdfFieldsGeom — a field's place as fractions of the rendered page, and the
// three conversions around it: to screen pixels, and to and from PDF user
// space for every /Rotate the spec allows. Pure arithmetic, no DOM.
import { describe, expect, it } from 'vitest';

import {
  clampFrac,
  fitPageWidth,
  fracToPdf,
  fracToPixel,
  normRotation,
  pdfToFrac,
  pixelToFrac,
  pointToFrac,
  rectFromPoints,
  unrotatedSize,
  type FracRect,
  type PageBox,
} from '@brftech/filex-core/src/lib/pdfFieldsGeom';

const portrait: PageBox = { width: 612, height: 792, rotation: 0 };
/** The same page turned clockwise: the rendered viewport is landscape. */
const turned90: PageBox = { width: 792, height: 612, rotation: 90 };
const turned180: PageBox = { width: 612, height: 792, rotation: 180 };
const turned270: PageBox = { width: 792, height: 612, rotation: 270 };

function close(a: FracRect, b: FracRect) {
  expect(a.x).toBeCloseTo(b.x, 5);
  expect(a.y).toBeCloseTo(b.y, 5);
  expect(a.w).toBeCloseTo(b.w, 5);
  expect(a.h).toBeCloseTo(b.h, 5);
}

describe('pdfFieldsGeom', () => {
  it('normalises any degree count to 0/90/180/270', () => {
    expect(normRotation(0)).toBe(0);
    expect(normRotation(90)).toBe(90);
    expect(normRotation(-90)).toBe(270);
    expect(normRotation(450)).toBe(90);
    expect(normRotation(undefined)).toBe(0);
    expect(normRotation(NaN)).toBe(0);
  });

  it('fraction ↔ pixel is the page size, nothing else', () => {
    const f: FracRect = { x: 0.25, y: 0.5, w: 0.1, h: 0.05 };
    const px = fracToPixel(f, { width: 400, height: 800 });
    expect(px).toEqual({ left: 100, top: 400, width: 40, height: 40 });
    close(pixelToFrac(px, { width: 400, height: 800 }), f);
    // A zoomed page changes the pixels and not the fractions.
    close(pixelToFrac(fracToPixel(f, { width: 1000, height: 2000 }), { width: 1000, height: 2000 }), f);
  });

  it('clamps a box inside the page and never below the minimum size', () => {
    expect(clampFrac({ x: 0.95, y: 0.99, w: 0.2, h: 0.1 })).toEqual({ x: 0.8, y: 0.9, w: 0.2, h: 0.1 });
    expect(clampFrac({ x: -1, y: -1, w: 0, h: 0 })).toEqual({ x: 0, y: 0, w: 0.02, h: 0.01 });
    expect(clampFrac({ x: NaN, y: NaN, w: NaN, h: NaN })).toEqual({ x: 0, y: 0, w: 0.02, h: 0.01 });
  });

  it('a drag between two corners is the same box whichever corner came first', () => {
    const a = rectFromPoints({ x: 0.1, y: 0.1 }, { x: 0.4, y: 0.3 });
    const b = rectFromPoints({ x: 0.4, y: 0.3 }, { x: 0.1, y: 0.1 });
    expect(a).toEqual(b);
    close(a, { x: 0.1, y: 0.1, w: 0.3, h: 0.2 });
    expect(pointToFrac({ x: -10, y: 900 }, { width: 400, height: 800 })).toEqual({ x: 0, y: 1 });
  });

  it('rotation 0: PDF space is the same box with the y axis flipped', () => {
    const f: FracRect = { x: 0.1, y: 0.1, w: 0.5, h: 0.05 };
    const pdf = fracToPdf(f, portrait);
    expect(pdf.x).toBeCloseTo(61.2, 3);
    expect(pdf.w).toBeCloseTo(306, 3);
    expect(pdf.h).toBeCloseTo(39.6, 3);
    // Top of the box is 10% from the page top → its bottom is 792 - (79.2 + 39.6).
    expect(pdf.y).toBeCloseTo(792 - 79.2 - 39.6, 3);
    close(pdfToFrac(pdf, portrait), f);
  });

  it('rotation 90: the rendered top-right corner is the unrotated top-left', () => {
    // A small box in the rendered page's top-right corner.
    const f: FracRect = { x: 0.9, y: 0, w: 0.1, h: 0.1 };
    const pdf = fracToPdf(f, turned90);
    const { width: pw, height: ph } = unrotatedSize(turned90);
    expect(pw).toBe(612);
    expect(ph).toBe(792);
    expect(pdf.x).toBeCloseTo(0, 3);
    expect(pdf.y).toBeCloseTo(ph - 79.2, 3);
    expect(pdf.w).toBeCloseTo(61.2, 3);
    expect(pdf.h).toBeCloseTo(79.2, 3);
    close(pdfToFrac(pdf, turned90), f);
  });

  it('rotation 180: the rendered bottom-right corner is the unrotated top-left', () => {
    const f: FracRect = { x: 0.9, y: 0.9, w: 0.1, h: 0.1 };
    const pdf = fracToPdf(f, turned180);
    expect(pdf.x).toBeCloseTo(0, 3);
    expect(pdf.y).toBeCloseTo(792 - 79.2, 3);
    close(pdfToFrac(pdf, turned180), f);
  });

  it('rotation 270: the rendered bottom-left corner is the unrotated top-left', () => {
    const f: FracRect = { x: 0, y: 0.9, w: 0.1, h: 0.1 };
    const pdf = fracToPdf(f, turned270);
    expect(pdf.x).toBeCloseTo(0, 3);
    expect(pdf.y).toBeCloseTo(792 - 79.2, 3);
    expect(pdf.w).toBeCloseTo(61.2, 3);
    close(pdfToFrac(pdf, turned270), f);
  });

  it('round-trips an arbitrary box through every rotation', () => {
    const f: FracRect = { x: 0.123, y: 0.456, w: 0.2, h: 0.07 };
    for (const page of [portrait, turned90, turned180, turned270]) {
      const pdf = fracToPdf(f, page);
      // Always inside the unrotated page.
      const { width: pw, height: ph } = unrotatedSize(page);
      expect(pdf.x).toBeGreaterThanOrEqual(0);
      expect(pdf.y).toBeGreaterThanOrEqual(0);
      expect(pdf.x + pdf.w).toBeLessThanOrEqual(pw + 1e-6);
      expect(pdf.y + pdf.h).toBeLessThanOrEqual(ph + 1e-6);
      close(pdfToFrac(pdf, page), f);
    }
  });
});

describe('fitting a page to the box it is drawn in (v3 §3.2)', () => {
  it('takes whichever constraint binds, so the page never needs two scrollbars', () => {
    // ⚠⚠ The measured defect, reproduced as numbers: the request wizard's
    // "place the fields" step at 1440x900 drew an A4 page 916 wide and 1296
    // tall inside a 916x618 box — off the bottom AND sideways. Fitting to the
    // HEIGHT gives a page that fits: (618-16) * 595/842 ≈ 425.
    const a4 = { width: 595, height: 842 };
    expect(Math.round(fitPageWidth({ width: 916, height: 618 }, a4))).toBe(425);
    // A tall, narrow box is bound by its WIDTH instead — the old behaviour,
    // and the right one there.
    expect(fitPageWidth({ width: 400, height: 2000 }, a4)).toBe(400);
  });

  it('never returns something too small to work in, and survives a box with no height yet', () => {
    const a4 = { width: 595, height: 842 };
    // Before the first layout observation there is no height; the width is
    // the honest answer and the first resize corrects it.
    expect(fitPageWidth({ width: 700, height: 0 }, a4)).toBe(700);
    expect(fitPageWidth({ width: 700, height: 10 }, a4)).toBe(700);
    // A pathologically short box still yields a usable page rather than 0.
    expect(fitPageWidth({ width: 700, height: 40 }, a4)).toBe(120);
  });

  it('a landscape page is fitted by its own ratio, not by a portrait assumption', () => {
    const landscape = { width: 842, height: 595 };
    // (616 - 16) * 842/595 ≈ 849, which fits inside 1200 — so the HEIGHT
    // binds here too, and a portrait assumption would have drawn it 1200 wide
    // and scrolled it.
    expect(Math.round(fitPageWidth({ width: 1200, height: 616 }, landscape))).toBe(849);
  });
});
