/**
 * useReorderDrag — drag a row of a vertical list to a new place (GitHub #57).
 *
 * ONE gesture for every list that can be put in order by hand: the navigation
 * panel's storages (SideNav, a person's own order) and the admin Storages table
 * (the administrator's order, dragged by its handle). Two lists, one grammar —
 * a second copy of this is how the two would start to feel different under the
 * hand.
 *
 * Pointer events, not HTML5 drag-and-drop: HTML5 DnD does not start from a
 * finger in most mobile browsers, and it would also look like a FILE drag to
 * the explorer's own drop targets.
 *
 *   · mouse / pen — the press becomes a drag once it has travelled DRAG_SLOP;
 *     below that it is an ordinary click and nothing here gets in its way;
 *   · a finger — after `touchDelayMs` of stillness (a long press on a whole
 *     row, so a finger that only wanted to SCROLL the list scrolls it; 0 on a
 *     dedicated handle, whose `touch-action: none` means it never scrolls);
 *     moving further than TOUCH_SLOP before that is a scroll and ends the
 *     gesture. Once pressed, the finger's moves are kept from scrolling.
 *   · a finger that long-pressed and lifted without moving is not a drop: it
 *     is `onHold` (the navigation panel opens the row's menu there).
 *
 * The caller owns the answer: `onDrop(key, gap)` is called only for a release
 * that changes the order — `gap` is the gap BEFORE row `gap` among the rows
 * drawn, the dragged one still counted, and its own two gaps are not a drop.
 * `dropGap` / `dragKey` are what the caller draws the marker and the lifted
 * row from.
 */
import { onBeforeUnmount, ref, type Ref } from 'vue';
import { LONG_PRESS_MS } from './useRowTouch';

/** How far a mouse/pen press travels before it is a drag, not a click. */
export const DRAG_SLOP = 5;
/** A finger that travels further than this before it is pressed is scrolling. */
const TOUCH_SLOP = 10;
/** A click this soon after a drag ended is the drag's own release. */
const CLICK_SWALLOW_MS = 300;
/** How close to the scroller's top/bottom edge a drag scrolls it. */
const EDGE_PX = 28;

export interface ReorderDragOptions {
  /** The rows' keys, in the order they are drawn. */
  keys: () => readonly string[];
  /** A row's element — the drop gap is measured against its box. */
  rowEl: (key: string) => Element | null | undefined;
  /** What scrolls the rows; nudged when a drag reaches its edge. */
  scroller?: () => Element | null | undefined;
  /** A finger's wait before the row can be dragged. Default: a long press. */
  touchDelayMs?: number;
  /** A release that changes the order. */
  onDrop: (key: string, gap: number) => void;
  /** A finger's long press lifted where it was. */
  onHold?: (key: string) => void;
}

export interface ReorderDrag {
  /** The row in hand (and the row a finger is holding). */
  dragKey: Ref<string | null>;
  /** Where it would land; null = nowhere new. */
  dropGap: Ref<number | null>;
  /** A drag is under way (the list wears the grabbing cursor). */
  reordering: Ref<boolean>;
  /** A finger's gesture is in progress — its long-press `contextmenu` is the gesture's, not a menu request. */
  touchActive: () => boolean;
  onPointerDown: (key: string, ev: PointerEvent) => void;
  /** Bind with `@click.capture` on the list: swallows the click a release produces. */
  onClickCapture: (ev: MouseEvent) => void;
}

interface Gesture {
  key: string;
  pointerId: number;
  touch: boolean;
  startX: number;
  startY: number;
  /** Mouse/pen: from the start. Finger: once `touchDelayMs` has passed. */
  pressed: boolean;
  /** Travelled far enough to be a drag. */
  armed: boolean;
  timer?: ReturnType<typeof setTimeout>;
}

export function useReorderDrag(opts: ReorderDragOptions): ReorderDrag {
  const dragKey = ref<string | null>(null);
  const dropGap = ref<number | null>(null);
  const reordering = ref(false);
  let g: Gesture | null = null;
  let endedAt = 0;

  /** The gap under the pointer: before the first row whose middle is below it. */
  function gapAt(y: number): number {
    const keys = opts.keys();
    for (let i = 0; i < keys.length; i++) {
      const el = opts.rowEl(keys[i]);
      if (!el) continue;
      const r = el.getBoundingClientRect();
      if (y < r.top + r.height / 2) return i;
    }
    return keys.length;
  }

  function onPointerDown(key: string, ev: PointerEvent) {
    const touch = ev.pointerType === 'touch';
    if (!touch && ev.button !== 0) return;
    end();
    const delay = opts.touchDelayMs ?? LONG_PRESS_MS;
    const gesture: Gesture = {
      key,
      pointerId: ev.pointerId,
      touch,
      startX: ev.clientX,
      startY: ev.clientY,
      pressed: !touch || delay <= 0,
      armed: false,
    };
    if (touch && delay > 0) {
      gesture.timer = setTimeout(() => {
        if (g !== gesture) return;
        gesture.pressed = true;
        // The row lifts: the press registered — drag it now, or lift the finger.
        dragKey.value = key;
      }, delay);
    }
    g = gesture;
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    // ⚠ Every way a gesture can END, not just the happy one — a pointer the
    // browser takes away fires neither pointerup nor a key.
    window.addEventListener('pointercancel', abort);
    window.addEventListener('blur', abort);
    window.addEventListener('keydown', onKey, true);
    // ⚠ Not passive: a pressed finger's moves must not scroll the list away
    // from under the drag.
    if (touch) window.addEventListener('touchmove', onTouchMove, { passive: false });
  }

  function onTouchMove(ev: TouchEvent) {
    if (g?.pressed && ev.cancelable) ev.preventDefault();
  }

  function onMove(ev: PointerEvent) {
    const d = g;
    if (!d || ev.pointerId !== d.pointerId) return;
    const dx = ev.clientX - d.startX;
    const dy = ev.clientY - d.startY;
    if (!d.armed) {
      if (!d.pressed) {
        // A finger before the long press: moving means scrolling. Let it.
        if (Math.abs(dx) > TOUCH_SLOP || Math.abs(dy) > TOUCH_SLOP) end();
        return;
      }
      if (Math.abs(dx) < DRAG_SLOP && Math.abs(dy) < DRAG_SLOP) return;
      d.armed = true;
      dragKey.value = d.key;
      reordering.value = true;
    }
    const gap = gapAt(ev.clientY);
    const from = opts.keys().indexOf(d.key);
    // Its own two gaps are not a drop: no marker, and the release writes nothing.
    dropGap.value = gap === from || gap === from + 1 ? null : gap;
    const scroller = opts.scroller?.();
    if (scroller) {
      const r = scroller.getBoundingClientRect();
      if (ev.clientY < r.top + EDGE_PX) scroller.scrollTop -= 12;
      else if (ev.clientY > r.bottom - EDGE_PX) scroller.scrollTop += 12;
    }
  }

  /**
   * ⚠ A finger that held (or dragged) is not a tap, but the browser may still
   * emulate one when it lifts: mousedown / mouseup / CLICK at the point under
   * it. Measured (e2e 158, Chromium touch emulation): that click landed on the
   * menu sheet the hold had just opened — on its backdrop, which closes on a
   * click — and the sheet was gone 50 ms later. Cancelling the touchend is
   * what stops the emulation (useRowTouch follows the same rule). Just the one
   * touchend that ends THIS gesture: pointerup comes first, so it is caught on
   * its way in.
   */
  function cancelEmulatedClick() {
    const stop = (e: Event) => {
      if (e.cancelable) e.preventDefault();
      off();
    };
    const timer = setTimeout(() => off(), 600);
    function off() {
      clearTimeout(timer);
      window.removeEventListener('touchend', stop, true);
    }
    window.addEventListener('touchend', stop, { capture: true, passive: false });
  }

  function onUp(ev: PointerEvent) {
    const d = g;
    if (!d || ev.pointerId !== d.pointerId) return;
    const gap = dropGap.value;
    end();
    if (d.touch && d.pressed) cancelEmulatedClick();
    if (d.armed) {
      endedAt = Date.now();
      if (gap !== null) opts.onDrop(d.key, gap);
      return;
    }
    if (d.touch && d.pressed && (opts.touchDelayMs ?? LONG_PRESS_MS) > 0) {
      endedAt = Date.now();
      opts.onHold?.(d.key);
    }
  }

  function onKey(ev: KeyboardEvent) {
    if (ev.key !== 'Escape' || !g) return;
    ev.preventDefault();
    ev.stopPropagation();
    abort();
  }

  function abort() {
    const armed = g?.armed === true;
    end();
    if (armed) endedAt = Date.now();
  }

  function end() {
    if (g?.timer) clearTimeout(g.timer);
    g = null;
    dragKey.value = null;
    dropGap.value = null;
    reordering.value = false;
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointercancel', abort);
    window.removeEventListener('blur', abort);
    window.removeEventListener('keydown', onKey, true);
    window.removeEventListener('touchmove', onTouchMove);
  }

  function onClickCapture(ev: MouseEvent) {
    if (Date.now() - endedAt < CLICK_SWALLOW_MS) {
      ev.preventDefault();
      ev.stopPropagation();
    }
  }

  onBeforeUnmount(end);

  return {
    dragKey,
    dropGap,
    reordering,
    touchActive: () => g?.touch === true,
    onPointerDown,
    onClickCapture,
  };
}
