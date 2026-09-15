/**
 * useRowTouch — the one click and touch grammar every file view speaks (list,
 * grid, gallery).
 *
 * The rule (issue #26, fourth round — the reporter's words: "only clicking on
 * checkbox selects it, any other click will open"), the same on every device:
 *
 *   - the item's CHECKBOX selects: a tick adds or removes, shift+tick extends
 *     the range from the anchor;
 *   - a click or tap ANYWHERE ELSE on the item opens it — the name, the icon,
 *     the size column, the empty space beside the name, with or without a
 *     selection, with or without Ctrl/Shift held;
 *   - a right click (mouse) or a long press (finger) opens the item's menu;
 *   - the item's own controls — the star, the ⋮ button — do their own job.
 *
 * The views only report. What a click means is decided once, in FilePane.
 *
 * How the rule got here:
 *   - First round: a finger has no double-click, so on a phone nothing could
 *     be opened at all. A tap was made to open.
 *   - Second round: a press on the NAME opened on every device, a press
 *     beside it still selected. The reporter: "need to click precisely on
 *     name, if a lil bit on the right then it selects file".
 *   - Third round: on a phone the name still needed two taps. A tap reaches a
 *     page as a click only at the end of the browser's compatibility sequence
 *     (touch → mouse move → hover → mouse down/up → click), and iOS WebKit
 *     stops that sequence when the hover step reveals content. So a finger
 *     lifted on the item opens it right there, at `touchend`, and cancels the
 *     emulated mouse events that would follow (`onTap`); the hover reveals are
 *     kept off screens that cannot hover (styles/base.css,
 *     `@media (hover: hover)`).
 *   - Fourth round: the checkbox became the only way a click selects, and the
 *     grid and gallery cards got one too (they had none).
 *
 * ⚠ A tap is judged from the gesture that produced the click, never from the
 * screen: `pointerType` where the browser sets it on click (Chromium, Firefox),
 * and the touchend that just preceded the click where it does not (older
 * WebKit). A touch laptop's trackpad and its screen are two different gestures
 * a `(pointer: coarse)` media query cannot tell apart.
 *
 * ⚠ It used to live as three identical copies of the long-press timer, one per
 * view; a rule like this one is exactly the kind of addition a copy misses.
 */
import { onBeforeUnmount } from 'vue';

/** How long a finger rests before the press becomes a long press. */
export const LONG_PRESS_MS = 500;
/** A click this soon after a touchend is the tap that touchend ended. */
const TAP_WINDOW_MS = 800;
/** A finger that travels further than this is scrolling, not pressing. */
const MOVE_TOLERANCE_PX = 10;

/**
 * What on an item is a control of its own: a press there is the control's,
 * not the item's. The checkbox is one (it selects on its own click), and so
 * are the star and the ⋮ button. A wrapper cell that swallows its clicks
 * carries `data-fe-control` so a finger on its padding is not an open either.
 */
export const ITEM_CONTROL_SELECTOR =
  'button, a, input, select, textarea, label, [role="checkbox"], [data-fe-control]';

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
  /** The click was on the item's checkbox — the one click that selects. */
  check?: boolean;
}

/** The one place a view turns a click on an item into a `ClickMod`. */
export function clickMod(ev: MouseEvent, touch: boolean): ClickMod {
  return { ctrl: ev.ctrlKey || ev.metaKey, shift: ev.shiftKey, touch };
}

/**
 * The one place a view turns a click on an item's CHECKBOX into a `ClickMod`.
 * `ctrl` because a tick adds to a selection rather than replacing it; `shift`
 * passes through so a shift-tick still extends the range from the anchor.
 */
export function checkMod(ev: MouseEvent): ClickMod {
  return { ctrl: true, shift: ev.shiftKey, check: true };
}

export interface RowTouchOptions<T> {
  /** A tap on the item outside its controls: open it, before any click. */
  onTap?: (item: T) => void;
}

/** Whether the press started on one of the item's own controls. */
function onControl(ev: Event): boolean {
  const el = ev.target as Element | null;
  if (typeof el?.closest !== 'function') return false;
  const control = el.closest(ITEM_CONTROL_SELECTOR);
  const item = ev.currentTarget as Element | null;
  // Only a control INSIDE the item counts: the listing itself may sit in a
  // label or a link on some host page, and that must not disarm every tap.
  return control !== null && (item === null || typeof item.contains !== 'function' || item.contains(control));
}

export function useRowTouch<T>(onLongPress: (item: T, at: TouchPoint) => void, options: RowTouchOptions<T> = {}) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let target: T | null = null;
  let origin: TouchPoint = { clientX: 0, clientY: 0 };
  let longPressed = false;
  let tapEndedAt = 0;
  let startedOnItem = false;

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
    startedOnItem = !onControl(ev);
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
   * listing under the finger (FilePane's open guard catches that too).
   */
  function onTouchEnd(ev?: TouchEvent) {
    stopTimer();
    const item = target;
    const tapped = item !== null && !longPressed;
    if (tapped) tapEndedAt = Date.now();
    target = null;
    if (tapped && startedOnItem && options.onTap) {
      if (ev?.cancelable) ev.preventDefault();
      options.onTap(item as T);
    }
    startedOnItem = false;
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
