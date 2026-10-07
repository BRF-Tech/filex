/**
 * Where a dropdown list goes, relative to the field it drops from
 * (`components/ChoiceSelect.vue`).
 *
 * The list is teleported out of the field's tree (so no `overflow: hidden`
 * ancestor can clip it) and is `position: fixed`, so it needs viewport
 * coordinates. The rule, once:
 *
 *   - under the field, 4px below it; ABOVE it when the room below is less
 *     than the list wants (capped at 160px) and there is more room above;
 *   - at least as wide as the field and never narrower than 180px, never
 *     wider than the window less an 8px gutter on each side;
 *   - starting at the field's inline-START edge (its right edge in a
 *     right-to-left interface) and pushed back inside the window when it
 *     would cross the far edge (lib/direction `clampAlongInline`);
 *   - as tall as the room on its side allows, between 120px and 320px.
 *
 * ⚠ `start` is the distance from the viewport's inline-start edge, written as
 * `inset-inline-start` on a list that carries the field's `dir` — never a
 * physical left or right (web/tests/quality/rtlLogical.test.ts).
 */
import { clampAlongInline, inlineStartX, type TextDirection } from './direction';

export interface ListPlacement {
  /** The list's top edge, when it opens downwards. */
  top: number;
  /** The list's distance from the viewport's bottom edge, when it opens upwards. */
  bottom: number;
  /** The list's distance from the viewport's inline-start edge. */
  start: number;
  width: number;
  maxHeight: number;
  /** Opens upwards (there was no room below). */
  up: boolean;
}

/** A row of the list is at most this tall (a finger's target). */
const ROW = 44;

export function placeListUnder(
  field: { top: number; bottom: number; left: number; right: number; width: number },
  viewport: { width: number; height: number },
  dir: TextDirection,
  rows: number,
): ListPlacement {
  const below = viewport.height - field.bottom - 8;
  const above = field.top - 8;
  const want = Math.min(320, rows * ROW + 8);
  const up = below < Math.min(want, 160) && above > below;
  const width = Math.min(Math.max(field.width, 180), viewport.width - 16);
  // A physical x from the viewport's left edge, mirrored for right to left.
  const x = clampAlongInline(inlineStartX(field, dir), width, viewport.width, dir);
  return {
    top: Math.round(field.bottom + 4),
    bottom: Math.round(viewport.height - field.top + 4),
    start: Math.round(dir === 'rtl' ? viewport.width - x - width : x),
    width: Math.round(width),
    maxHeight: Math.round(Math.max(120, Math.min(320, up ? above : below))),
    up,
  };
}
