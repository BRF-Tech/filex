/**
 * useRowTouch — the one touch grammar every file view speaks (list, grid,
 * gallery).
 *
 * A mouse selects with a click and opens with a double-click. A finger has no
 * double-click: the browser swallows the second tap into a zoom or never sends
 * `dblclick` at all, so on a phone the desktop grammar left nothing openable
 * (issue #26). What a finger does instead is what every mobile file manager
 * does:
 *
 *   - a long press opens the item's menu (and, through it, selects the item);
 *   - a tap is reported AS a tap, so the host can open instead of select.
 *
 * The views only report. What a tap means is decided once, in FilePane.
 *
 * Issue #26, second round (the reporter's rule, on EVERY device):
 *   - a press on the item's NAME opens it — mouse click or finger tap, with or
 *     without a selection;
 *   - a long press (finger) or a right click (mouse) opens the menu;
 *   - the checkbox selects, exactly as before.
 * So the views also report whether the click landed on the name (`name`).
 *
 * Third round: on a phone the name still needed two taps. A tap is delivered
 * as a click only at the end of the browser's own compatibility sequence
 * (touch → mouse move → hover → mouse down/up → click), and iOS WebKit stops
 * that sequence when the hover step reveals content — the row "highlighted"
 * and the click never came. So a finger lifted on the NAME opens the item
 * right there, at `touchend`, and cancels the emulated mouse events that
 * would otherwise follow (`onNameTap`). The hover reveals are also kept off
 * screens that cannot hover (styles/base.css, `@media (hover: hover)`), for
 * taps anywhere else on the item.
 *
 * ⚠ A tap is judged from the gesture that produced the click, never from the
 * screen: `pointerType` where the browser sets it on click (Chromium, Firefox),
 * and the touchend that just preceded the click where it does not (older
 * WebKit). A touch laptop's trackpad therefore keeps the desktop grammar while
 * its screen gets the phone one. A `(pointer: coarse)` media query cannot tell
 * those two apart.
 *
 * ⚠ It used to live as three identical copies of the long-press timer, one per
 * view; the tap rule is exactly the kind of addition a copy misses.
 */
import { onBeforeUnmount } from 'vue';

/** How long a finger rests before the press becomes a long press. */
export const LONG_PRESS_MS = 500;
/** A click this soon after a touchend is the tap that touchend ended. */
const TAP_WINDOW_MS = 800;
/** A finger that travels further than this is scrolling, not pressing. */
const MOVE_TOLERANCE_PX = 10;

export interface TouchPoint {
  clientX: number;
  clientY: number;
}

/** What a view reports about a click on one of its items. */
export interface ClickMod {
  ctrl: boolean;
  shift: boolean;
  /** The click was a finger's tap. */
  touch?: boolean;
  /** The click landed on the item's name. */
  name?: boolean;
}

/**
 * The one place a view turns a click into a `ClickMod`. `nameSelector` is the
 * view's own name element (`.fe-list__name`, `.fe-grid__label`, …).
 */
export function clickMod(ev: MouseEvent, touch: boolean, nameSelector: string): ClickMod {
  const target = ev.target as Element | null;
  return {
    ctrl: ev.ctrlKey || ev.metaKey,
    shift: ev.shiftKey,
    touch,
    name: typeof target?.closest === 'function' && target.closest(nameSelector) !== null,
  };
}

export interface RowTouchOptions<T> {
  /** The view's own name element (`.fe-list__name`, `.fe-grid__label`, …). */
  nameSelector?: string;
  /** A tap that started on the name: open the item, before any click. */
  onNameTap?: (item: T) => void;
}

export function useRowTouch<T>(onLongPress: (item: T, at: TouchPoint) => void, options: RowTouchOptions<T> = {}) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let target: T | null = null;
  let origin: TouchPoint = { clientX: 0, clientY: 0 };
  let longPressed = false;
  let tapEndedAt = 0;
  let startedOnName = false;

  function stopTimer() {
    if (timer) clearTimeout(timer);
    timer = undefined;
  }

  function onTouchStart(item: T, ev: TouchEvent) {
    const t0 = ev.touches[0];
    if (!t0) return;
    stopTimer();
    target = item;
    longPressed = false;
    origin = { clientX: t0.clientX, clientY: t0.clientY };
    const el = ev.target as Element | null;
    startedOnName =
      !!options.nameSelector && typeof el?.closest === 'function' && el.closest(options.nameSelector) !== null;
    timer = setTimeout(() => {
      timer = undefined;
      if (target === null) return;
      longPressed = true;
      onLongPress(target, origin);
    }, LONG_PRESS_MS);
  }

  function onTouchMove(ev: TouchEvent) {
    const t0 = ev.touches[0];
    if (
      !t0 ||
      Math.abs(t0.clientX - origin.clientX) > MOVE_TOLERANCE_PX ||
      Math.abs(t0.clientY - origin.clientY) > MOVE_TOLERANCE_PX
    ) {
      stopTimer();
      target = null;
    }
  }

  /**
   * ⚠ Bind it WITHOUT `.passive`: a passive listener cannot cancel the
   * emulated mouse events, and the click they end in would then reach the new
   * listing under the finger (FilePane's name-open guard catches that too).
   */
  function onTouchEnd(ev?: TouchEvent) {
    stopTimer();
    const item = target;
    const tapped = item !== null && !longPressed;
    if (tapped) tapEndedAt = Date.now();
    target = null;
    if (tapped && startedOnName && options.onNameTap) {
      if (ev?.cancelable) ev.preventDefault();
      options.onNameTap(item as T);
    }
    startedOnName = false;
  }

  /** Whether this click is a finger's tap. */
  function isTap(ev: MouseEvent): boolean {
    const kind = (ev as PointerEvent).pointerType;
    if (kind) return kind === 'touch';
    return Date.now() - tapEndedAt < TAP_WINDOW_MS;
  }

  onBeforeUnmount(stopTimer);

  return { onTouchStart, onTouchMove, onTouchEnd, isTap };
}
