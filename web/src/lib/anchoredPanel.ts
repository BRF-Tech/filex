// Where a header dropdown goes once it is teleported to <body>.
//
// Two controls in the explorer's header open a panel — the avatar's account
// menu and the notification bell — and both have to be TELEPORTED: `.fe`, the
// explorer's root, carries `overflow: hidden`, so a panel positioned inside the
// header is cut off at the header's bottom edge (measured when the account menu
// was built: every row below the first was simply not on screen). A teleported
// panel is `position: fixed`, so it needs coordinates, and the two controls were
// about to carry two copies of the arithmetic that produces them.
//
// The rule, once: hang the panel 6px under the button, flush with the button's
// END edge — its right edge in a left-to-right interface, its LEFT edge in a
// right-to-left one (both controls sit at the end of the row, where a
// start-edge anchor hangs the panel off the viewport) — and, when the caller
// gives a width, keep the whole panel on screen. That last part is not
// decoration: at 390px the bell sits a button's width left of the avatar, and a
// 360px panel flush with its right edge started 20px off the left of the phone.
//
// ⚠ RTL: the arithmetic is the LTR arithmetic measured from the other side of
// the viewport, so the answer carries `left` instead of `right`; bind both
// (`{ right: pos.right, left: pos.left }`) and the absent one is simply not set.

export interface AnchoredPanelPosition {
  top: string;
  /** Distance from the viewport's right edge — left-to-right interfaces. */
  right?: string;
  /** Distance from the viewport's left edge — right-to-left interfaces. */
  left?: string;
  /** Present only when a width was asked for. */
  width?: string;
  /** Room left under the anchor, for a scrolling list inside the panel. */
  maxHeight: string;
}

export interface AnchorOptions {
  /** The panel's preferred width in px; clamped to the viewport. */
  width?: number;
  /** Minimum distance from the viewport's edges. */
  gutter?: number;
  /** Gap between the button's bottom and the panel's top. */
  gap?: number;
  /** Ceiling for `maxHeight`. */
  maxHeight?: number;
  /** The direction the header is drawn in (core `dirOfElement(button)`). */
  dir?: 'ltr' | 'rtl';
}

/** The DOM element behind a template ref that may be a component (Headless UI's buttons are). */
export function refElement(r: unknown): HTMLElement | null {
  if (!r) return null;
  if (typeof HTMLElement !== 'undefined' && r instanceof HTMLElement) return r;
  const el = (r as { $el?: unknown }).$el;
  return typeof HTMLElement !== 'undefined' && el instanceof HTMLElement ? el : null;
}

export function anchorUnderEndEdge(
  rect: { bottom: number; right: number; left?: number },
  viewport: { width: number; height: number },
  opts: AnchorOptions = {},
): AnchoredPanelPosition {
  const rtl = opts.dir === 'rtl';
  const side = rtl ? 'left' : 'right';
  const gutter = opts.gutter ?? 8;
  const top = Math.round(rect.bottom + (opts.gap ?? 6));
  // The gap between the button's END edge and the viewport's end edge.
  const flush = Math.round(rtl ? (rect.left ?? 0) : viewport.width - rect.right);
  const room = Math.max(200, viewport.height - top - gutter);
  const maxHeight = `${opts.maxHeight ? Math.min(opts.maxHeight, room) : room}px`;
  if (!opts.width) return { top: `${top}px`, [side]: `${flush}px`, maxHeight };
  const width = Math.max(0, Math.min(opts.width, viewport.width - gutter * 2));
  const offset = Math.min(Math.max(flush, gutter), viewport.width - width - gutter);
  return { top: `${top}px`, [side]: `${Math.round(offset)}px`, width: `${Math.round(width)}px`, maxHeight };
}
