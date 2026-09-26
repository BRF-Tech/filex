// useReorderDrag — the one "drag a row to a new place" gesture (#57), shared
// by the navigation panel's storages and the admin Storages table.
//
// The rules these tests hold (the real-browser half is e2e 158):
//   - a mouse press is a CLICK until it travels DRAG_SLOP; only then a drag;
//   - the drop gap is read from the rows' boxes, and a release in the row's
//     own two gaps is not a drop (nothing is written);
//   - the click a release produces is swallowed;
//   - a finger on a whole row must hold still for the long press first —
//     moving earlier is a scroll and ends the gesture; held and lifted in
//     place it is `onHold` (the row's menu), not a drop;
//   - Escape abandons a drag.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount } from '@vue/test-utils';

import { useReorderDrag, type ReorderDrag } from '@brftech/filex-core/src/composables/useReorderDrag';

const KEYS = ['a', 'b', 'c'];
/** Three 30px rows stacked from y=0: a 0-30, b 30-60, c 60-90. */
function rowEl(key: string) {
  const i = KEYS.indexOf(key);
  return { getBoundingClientRect: () => ({ top: i * 30, height: 30, bottom: i * 30 + 30 }) } as unknown as Element;
}

let drag: ReorderDrag;
const onDrop = vi.fn();
const onHold = vi.fn();

function setup(touchDelayMs?: number) {
  const Host = defineComponent({
    setup() {
      drag = useReorderDrag({ keys: () => KEYS, rowEl, touchDelayMs, onDrop, onHold });
      return () => h('div');
    },
  });
  return mount(Host);
}

function pointer(type: string, y: number, pointerType = 'mouse') {
  const ev = new MouseEvent(type, { clientX: 10, clientY: y, button: 0, bubbles: true }) as MouseEvent & {
    pointerId: number;
    pointerType: string;
  };
  Object.defineProperty(ev, 'pointerId', { value: 1 });
  Object.defineProperty(ev, 'pointerType', { value: pointerType });
  return ev as unknown as PointerEvent;
}

beforeEach(() => {
  onDrop.mockClear();
  onHold.mockClear();
});
afterEach(() => {
  vi.useRealTimers();
});

describe('a mouse', () => {
  it('is a click until it has travelled the slop', () => {
    const w = setup();
    drag.onPointerDown('a', pointer('pointerdown', 15));
    window.dispatchEvent(pointer('pointermove', 17));
    expect(drag.dragKey.value).toBeNull();
    window.dispatchEvent(pointer('pointerup', 17));
    expect(onDrop).not.toHaveBeenCalled();
    w.unmount();
  });

  it('drags past the slop, marks the gap, drops there, and swallows the release click', () => {
    const w = setup();
    drag.onPointerDown('a', pointer('pointerdown', 15));
    window.dispatchEvent(pointer('pointermove', 82));
    expect(drag.dragKey.value).toBe('a');
    expect(drag.dropGap.value).toBe(3);
    window.dispatchEvent(pointer('pointerup', 82));
    expect(onDrop).toHaveBeenCalledWith('a', 3);
    expect(drag.dragKey.value).toBeNull();
    const click = new MouseEvent('click', { cancelable: true });
    const stop = vi.spyOn(click, 'stopPropagation');
    drag.onClickCapture(click);
    expect(stop).toHaveBeenCalled();
    w.unmount();
  });

  it('a release in its own two gaps writes nothing', () => {
    const w = setup();
    drag.onPointerDown('b', pointer('pointerdown', 45));
    window.dispatchEvent(pointer('pointermove', 70));
    expect(drag.dropGap.value).toBeNull();
    window.dispatchEvent(pointer('pointerup', 70));
    expect(onDrop).not.toHaveBeenCalled();
    w.unmount();
  });

  it('Escape abandons the drag', () => {
    const w = setup();
    drag.onPointerDown('a', pointer('pointerdown', 15));
    window.dispatchEvent(pointer('pointermove', 82));
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(drag.dragKey.value).toBeNull();
    window.dispatchEvent(pointer('pointerup', 82));
    expect(onDrop).not.toHaveBeenCalled();
    w.unmount();
  });
});

describe('a finger on a whole row', () => {
  it('that moves before the long press is scrolling, not dragging', () => {
    vi.useFakeTimers();
    const w = setup();
    drag.onPointerDown('a', pointer('pointerdown', 15, 'touch'));
    window.dispatchEvent(pointer('pointermove', 40, 'touch'));
    vi.advanceTimersByTime(1000);
    expect(drag.dragKey.value).toBeNull();
    window.dispatchEvent(pointer('pointermove', 82, 'touch'));
    window.dispatchEvent(pointer('pointerup', 82, 'touch'));
    expect(onDrop).not.toHaveBeenCalled();
    w.unmount();
  });

  it('held, lifts the row; then moved, drags it', () => {
    vi.useFakeTimers();
    const w = setup();
    drag.onPointerDown('c', pointer('pointerdown', 75, 'touch'));
    vi.advanceTimersByTime(600);
    expect(drag.dragKey.value).toBe('c');
    window.dispatchEvent(pointer('pointermove', 5, 'touch'));
    window.dispatchEvent(pointer('pointerup', 5, 'touch'));
    expect(onDrop).toHaveBeenCalledWith('c', 0);
    expect(onHold).not.toHaveBeenCalled();
    w.unmount();
  });

  it('held and lifted in place is the row menu, not a drop', () => {
    vi.useFakeTimers();
    const w = setup();
    drag.onPointerDown('b', pointer('pointerdown', 45, 'touch'));
    vi.advanceTimersByTime(600);
    window.dispatchEvent(pointer('pointerup', 45, 'touch'));
    expect(onHold).toHaveBeenCalledWith('b');
    expect(onDrop).not.toHaveBeenCalled();
    w.unmount();
  });

  it('cancels the mouse events the browser would emulate for the lifted finger', () => {
    // Measured (e2e 158, Chromium touch emulation): after the long press the
    // browser went on to mousedown / mouseup / CLICK at the point under the
    // finger — by then the menu sheet's backdrop, whose click closes it. The
    // sheet opened and was gone in 50 ms. Cancelling that touchend is what
    // stops the emulated click (the same rule useRowTouch follows).
    vi.useFakeTimers();
    const w = setup();
    drag.onPointerDown('b', pointer('pointerdown', 45, 'touch'));
    vi.advanceTimersByTime(600);
    window.dispatchEvent(pointer('pointerup', 45, 'touch'));
    const lifted = new Event('touchend', { cancelable: true });
    window.dispatchEvent(lifted);
    expect(lifted.defaultPrevented).toBe(true);
    // …only that one: the next touch is somebody else's business.
    const next = new Event('touchend', { cancelable: true });
    window.dispatchEvent(next);
    expect(next.defaultPrevented).toBe(false);
    w.unmount();
  });

  it('leaves a plain tap alone (it is a click on the row)', () => {
    vi.useFakeTimers();
    const w = setup();
    drag.onPointerDown('b', pointer('pointerdown', 45, 'touch'));
    vi.advanceTimersByTime(100);
    window.dispatchEvent(pointer('pointerup', 45, 'touch'));
    const lifted = new Event('touchend', { cancelable: true });
    window.dispatchEvent(lifted);
    expect(lifted.defaultPrevented).toBe(false);
    w.unmount();
  });
});

describe('a finger on a handle (no long press)', () => {
  it('drags at once', () => {
    const w = setup(0);
    drag.onPointerDown('a', pointer('pointerdown', 15, 'touch'));
    window.dispatchEvent(pointer('pointermove', 82, 'touch'));
    window.dispatchEvent(pointer('pointerup', 82, 'touch'));
    expect(onDrop).toHaveBeenCalledWith('a', 3);
    w.unmount();
  });
});
