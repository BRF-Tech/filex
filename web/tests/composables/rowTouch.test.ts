// useRowTouch - the one click and touch grammar of the list, the grid and the
// gallery (issue #26). These tests hold what a finger's LIFT does; the
// real-browser half is e2e 102.
//
//   - a tap on the item opens it at touchend and cancels the touchend, so the
//     mouse events the browser would emulate after it never reach the new
//     listing under the finger;
//   - a long press opens the item's menu, and the lift that ends it is
//     cancelled too: the browser may still take it for a tap and click
//     whatever the menu put under the finger (the 0.56 nightly: the sheet's
//     Share row);
//   - a tap on one of the item's own controls (the checkbox) and a finger that
//     scrolled are left alone - their click is theirs.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount } from '@vue/test-utils';

import { LONG_PRESS_MS, useRowTouch } from '@brftech/filex-core/src/composables/useRowTouch';

type Touch = ReturnType<typeof useRowTouch<string>>;

let touch: Touch;
const onLongPress = vi.fn();
const onTap = vi.fn();

function setup() {
  const Host = defineComponent({
    setup() {
      touch = useRowTouch<string>(onLongPress, { onTap });
      return () => h('div');
    },
  });
  return mount(Host);
}

/** An item with a checkbox inside, the way the views draw one. */
function itemEl() {
  const item = document.createElement('div');
  item.setAttribute('data-fe-path', 'store://a.txt');
  const name = document.createElement('span');
  name.textContent = 'a.txt';
  const check = document.createElement('button');
  check.setAttribute('role', 'checkbox');
  item.append(name, check);
  document.body.append(item);
  return { item, name, check };
}

function start(item: Element, on: Element, x = 20, y = 20) {
  touch.onTouchStart('a.txt', {
    touches: [{ clientX: x, clientY: y }],
    target: on,
    currentTarget: item,
  } as unknown as TouchEvent);
}

function move(x: number, y: number) {
  touch.onTouchMove({ touches: [{ clientX: x, clientY: y }] } as unknown as TouchEvent);
}

/** The lift, dispatched for real so `defaultPrevented` is the browser's answer. */
function lift(item: Element): Event {
  const ev = new Event('touchend', { cancelable: true, bubbles: true });
  const handler = (e: Event) => touch.onTouchEnd(e as TouchEvent);
  item.addEventListener('touchend', handler);
  item.dispatchEvent(ev);
  item.removeEventListener('touchend', handler);
  return ev;
}

beforeEach(() => {
  vi.useFakeTimers();
  onLongPress.mockClear();
  onTap.mockClear();
});
// <body> is emptied by setup.ts (teardownDom), after every page is unmounted.
afterEach(() => {
  vi.useRealTimers();
});

describe('the lift that ends a long press', () => {
  it('opens the menu once and cancels the touchend, so no emulated click reaches the menu', () => {
    const w = setup();
    const { item, name } = itemEl();
    start(item, name);
    vi.advanceTimersByTime(LONG_PRESS_MS + 300);
    expect(onLongPress).toHaveBeenCalledTimes(1);
    expect(onLongPress).toHaveBeenCalledWith('a.txt', { clientX: 20, clientY: 20 });
    const ev = lift(item);
    expect(ev.defaultPrevented, 'the lift after a long press is not a tap').toBe(true);
    expect(onTap).not.toHaveBeenCalled();
    w.unmount();
  });

  it('is cancelled even when the finger moved after the menu opened', () => {
    const w = setup();
    const { item, name } = itemEl();
    start(item, name);
    vi.advanceTimersByTime(LONG_PRESS_MS + 50);
    move(80, 20);
    expect(lift(item).defaultPrevented).toBe(true);
    expect(onTap).not.toHaveBeenCalled();
    w.unmount();
  });

  it('cancels only its own lift: the next tap on a control is left alone', () => {
    const w = setup();
    const { item, name, check } = itemEl();
    start(item, name);
    vi.advanceTimersByTime(LONG_PRESS_MS + 50);
    lift(item);
    start(item, check);
    vi.advanceTimersByTime(80);
    expect(lift(item).defaultPrevented, 'the checkbox keeps its click').toBe(false);
    w.unmount();
  });
});

describe('a tap', () => {
  it('on the item opens it at touchend and cancels the touchend', () => {
    const w = setup();
    const { item, name } = itemEl();
    start(item, name);
    vi.advanceTimersByTime(80);
    expect(lift(item).defaultPrevented).toBe(true);
    expect(onTap).toHaveBeenCalledWith('a.txt');
    expect(onLongPress).not.toHaveBeenCalled();
    w.unmount();
  });

  it('on the checkbox is the checkbox’s: not opened, not cancelled', () => {
    const w = setup();
    const { item, check } = itemEl();
    start(item, check);
    vi.advanceTimersByTime(80);
    expect(lift(item).defaultPrevented).toBe(false);
    expect(onTap).not.toHaveBeenCalled();
    w.unmount();
  });

  it('a finger that scrolled is neither a tap nor a long press', () => {
    const w = setup();
    const { item, name } = itemEl();
    start(item, name);
    move(20, 60);
    vi.advanceTimersByTime(LONG_PRESS_MS + 50);
    expect(lift(item).defaultPrevented).toBe(false);
    expect(onTap).not.toHaveBeenCalled();
    expect(onLongPress).not.toHaveBeenCalled();
    w.unmount();
  });
});
