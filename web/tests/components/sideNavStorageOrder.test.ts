// SideNav — reordering the storages (GitHub #57).
//
// The panel is presentational: it draws the storages in the order it is
// handed and announces the order a gesture produced (`reorder-storages`, the
// whole list; `[]` = back to the default). The explorer stores it. These tests
// hold the menu half — the keyboard's way in; the drop arithmetic is
// tests/lib/storageOrder.test.ts, and the drag itself is measured in a real
// browser (e2e/tests/158-sidebar-storage-order.spec.ts), because happy-dom
// lays nothing out.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';
import { closeRowMenus, menuEntries, pickMenuItem } from '../helpers/rowMenu';

const THREE = [{ name: 'Work' }, { name: 'Photos' }, { name: 'Archive' }];

function nav(extra: Record<string, unknown> = {}) {
  return mount(SideNav, {
    attachTo: document.body,
    props: { expanded: true, activeView: '', storages: THREE, locale: 'en', ...extra },
  });
}

async function rightClick(w: ReturnType<typeof nav>, name: string) {
  await w.find(`[data-testid="sidenav-storage-${name}"]`).trigger('contextmenu', { clientX: 40, clientY: 200 });
  await nextTick();
  await nextTick();
}

/** Close the open menu the way a person does — a click beside it. ⚠ Not
 *  `closeRowMenus()`: that pulls the DOM out from under a menu Vue still
 *  thinks is open, so the next `show()` on the same menu renders nothing. */
async function closeMenu() {
  (document.querySelector('.fe-ctx-backdrop') as HTMLElement | null)?.click();
  await nextTick();
}

afterEach(() => {
  closeRowMenus();
  document.body.innerHTML = '';
});

describe('SideNav — the storage order menu', () => {
  it('offers move up / move down / sort by name / reset order on a storage row', async () => {
    const w = nav();
    await rightClick(w, 'Photos');
    expect(menuEntries().map((e) => e.label)).toEqual(['Move up', 'Move down', 'Sort by name', 'Use default order']);
    w.unmount();
  });

  it('greys out what cannot happen: up on the first, down on the last, reset with no order of your own', async () => {
    const w = nav();
    await rightClick(w, 'Work');
    const first = menuEntries();
    expect(first.find((e) => e.label === 'Move up')?.disabled).toBe(true);
    expect(first.find((e) => e.label === 'Move down')?.disabled).toBe(false);
    expect(first.find((e) => e.label === 'Use default order')?.disabled).toBe(true);
    await closeMenu();
    await rightClick(w, 'Archive');
    expect(menuEntries().find((e) => e.label === 'Move down')?.disabled).toBe(true);
    w.unmount();
  });

  it('move down announces the whole new order', async () => {
    const w = nav();
    await rightClick(w, 'Work');
    await pickMenuItem('sidenav-storage-move-down');
    expect(w.emitted('reorder-storages')).toEqual([[['Photos', 'Work', 'Archive']]]);
    w.unmount();
  });

  it('move up announces the whole new order', async () => {
    const w = nav();
    await rightClick(w, 'Archive');
    await pickMenuItem('sidenav-storage-move-up');
    expect(w.emitted('reorder-storages')).toEqual([[['Work', 'Archive', 'Photos']]]);
    w.unmount();
  });

  it('sort by name writes a name-sorted order, not a mode', async () => {
    const w = nav();
    await rightClick(w, 'Work');
    await pickMenuItem('sidenav-storage-sort-name');
    expect(w.emitted('reorder-storages')).toEqual([[['Archive', 'Photos', 'Work']]]);
    w.unmount();
  });

  it('sort by name is greyed out when the list already is', async () => {
    const w = nav({ storages: [{ name: 'Archive' }, { name: 'Photos' }, { name: 'Work' }] });
    await rightClick(w, 'Work');
    const sort = menuEntries().find((e) => e.label === 'Sort by name');
    expect(sort?.disabled).toBe(true);
    w.unmount();
  });

  it('use default order announces the empty order when the person has one', async () => {
    const w = nav({ storageOrderCustom: true });
    await rightClick(w, 'Photos');
    expect(menuEntries().find((e) => e.label === 'Use default order')?.disabled).toBe(false);
    await pickMenuItem('sidenav-storage-default-order');
    expect(w.emitted('reorder-storages')).toEqual([[[]]]);
    w.unmount();
  });

  it('is reachable from the keyboard: Shift+F10 and the Menu key open it on the focused row', async () => {
    const w = nav();
    await w.find('[data-testid="sidenav-storage-Photos"]').trigger('keydown', { key: 'F10', shiftKey: true });
    await nextTick();
    await nextTick();
    expect(menuEntries().map((e) => e.label)).toContain('Move up');
    await closeMenu();
    await w.find('[data-testid="sidenav-storage-Archive"]').trigger('keydown', { key: 'ContextMenu' });
    await nextTick();
    await nextTick();
    expect(menuEntries().find((e) => e.label === 'Move down')?.disabled).toBe(true);
    w.unmount();
  });

  it('survives the browser echoing the key as a contextmenu on the menu itself', async () => {
    // On Windows the Menu key raises the browser's own contextmenu on the
    // key's RELEASE, at whatever has focus — by then the menu's first entry —
    // and the backdrop closes on a contextmenu. Headless Chromium never sees
    // that system event, so it is dispatched here by hand.
    const w = nav();
    await w.find('[data-testid="sidenav-storage-Photos"]').trigger('keydown', { key: 'F10', shiftKey: true });
    await nextTick();
    await nextTick();
    const first = document.querySelector('.fe-ctx .fe-ctx__item') as HTMLElement;
    first.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }));
    await nextTick();
    expect(menuEntries().length).toBe(4);
    // …and a real right click somewhere else is not swallowed.
    let reached = 0;
    const count = () => reached++;
    document.body.addEventListener('contextmenu', count);
    document.body.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }));
    document.body.removeEventListener('contextmenu', count);
    expect(reached).toBe(1);
    w.unmount();
  });

  it("a finger: a long press lifted in place opens the sheet, and the browser's long-tap contextmenu does not close it", async () => {
    // A browser that shows its context menu on the long TAP (the finger
    // lifting) aims it at the point under the finger — by then the sheet's
    // backdrop, which closes on a contextmenu. Playwright's touch emulation
    // sends no such event, so it is dispatched here by hand. (The emulated
    // CLICK it does send is covered in tests/lib/reorderDrag.test.ts.)
    vi.useFakeTimers();
    try {
      const w = nav();
      const rowEl = w.find('[data-testid="sidenav-storage-Photos"]').element;
      const touch = (type: string) => {
        const ev = new MouseEvent(type, { clientX: 20, clientY: 20, bubbles: true });
        Object.defineProperty(ev, 'pointerId', { value: 7 });
        Object.defineProperty(ev, 'pointerType', { value: 'touch' });
        return ev;
      };
      rowEl.dispatchEvent(touch('pointerdown'));
      vi.advanceTimersByTime(600);
      window.dispatchEvent(touch('pointerup'));
      await nextTick();
      await nextTick();
      expect(document.querySelector('.fe-sheet')).not.toBeNull();
      const backdrop = document.querySelector('.fe-ctx-backdrop') as HTMLElement;
      backdrop.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }));
      await nextTick();
      expect(document.querySelector('.fe-sheet')).not.toBeNull();
      const sheetLabels = Array.from(document.querySelectorAll('.fe-sheet .fe-ctx__label')).map((l) => l.textContent?.trim());
      expect(sheetLabels).toContain('Move up');
      w.unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it('keys by uid when the host sends one', async () => {
    const w = nav({ storages: [{ name: 'Work', uid: 'u-1' }, { name: 'Photos', uid: 'u-2' }] });
    await rightClick(w, 'Work');
    await pickMenuItem('sidenav-storage-move-down');
    expect(w.emitted('reorder-storages')).toEqual([[['u-2', 'u-1']]]);
    w.unmount();
  });

  it('keeps its right click to itself — the explorer behind it must not open its own menu on top', async () => {
    // ⚠ FileExplorer's root answers EVERY contextmenu that bubbles to it with
    // the blank-canvas menu (`onContextCanvas`). Measured in the browser: the
    // row menu opened, then the canvas menu's backdrop landed over it and no
    // entry could be clicked ("fe-ctx-backdrop intercepts pointer events").
    const w = nav();
    let reachedHost = 0;
    const host = () => reachedHost++;
    document.body.addEventListener('contextmenu', host);
    try {
      await rightClick(w, 'Photos');
      expect(menuEntries().length).toBe(4);
      expect(reachedHost).toBe(0);
    } finally {
      document.body.removeEventListener('contextmenu', host);
      w.unmount();
    }
  });

  it('with a single storage there is nothing to order and no menu', async () => {
    const w = nav({ storages: [{ name: 'Only' }] });
    await rightClick(w, 'Only');
    expect(menuEntries()).toEqual([]);
    w.unmount();
  });

  it('speaks Turkish with Turkish letters', async () => {
    const w = nav({ locale: 'tr' });
    await rightClick(w, 'Photos');
    expect(menuEntries().map((e) => e.label)).toEqual([
      'Yukarı taşı',
      'Aşağı taşı',
      'Ada göre sırala',
      'Varsayılan sırayı kullan',
    ]);
    w.unmount();
  });
});
