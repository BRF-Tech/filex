// The panel's dialog (components/ui/Modal.vue) when the BROWSER closes it:
// Chromium closes a modal <dialog> on a second Escape even though its
// `cancel` was prevented (without a click between the two, the second
// cancel cannot be prevented - the close-watcher rule against a page that
// traps its reader). A dialog that must stay (`preventClose`: an install on
// its way, store fe review #1) is opened again; any other one tells its
// owner, so `modelValue` never says open over a closed dialog.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import Modal from '@/components/ui/Modal.vue';
import { unmountAll } from '../helpers/teardown';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

async function opened(preventClose: boolean) {
  const w = mount(Modal, { props: { modelValue: false, title: 'x', preventClose }, attachTo: document.body });
  await w.setProps({ modelValue: true });
  await nextTick();
  const dlg = w.find('dialog').element as HTMLDialogElement;
  expect(dlg.open).toBe(true);
  return { w, dlg };
}

/** What the browser does on that second Escape: the dialog is closed, then `close` fires. */
function closedByTheBrowser(dlg: HTMLDialogElement) {
  dlg.removeAttribute('open');
  dlg.dispatchEvent(new Event('close'));
}

describe('a dialog the browser closes by itself', () => {
  afterEach(() => unmountAll());

  it('stays open when it must (preventClose), and tells nobody it closed', async () => {
    const { w, dlg } = await opened(true);
    const show = vi.spyOn(dlg, 'showModal');
    closedByTheBrowser(dlg);
    expect(show).toHaveBeenCalledTimes(1);
    expect(dlg.open).toBe(true);
    expect(w.emitted('update:modelValue')).toBeUndefined();
  });

  it('any other one tells its owner, once', async () => {
    const { w, dlg } = await opened(false);
    closedByTheBrowser(dlg);
    expect(w.emitted('update:modelValue')).toEqual([[false]]);
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('a close the component made itself is not answered twice', async () => {
    const { w, dlg } = await opened(true);
    const show = vi.spyOn(dlg, 'showModal');
    await w.setProps({ modelValue: false });
    await nextTick();
    dlg.dispatchEvent(new Event('close'));
    expect(show).not.toHaveBeenCalled();
    expect(w.emitted('update:modelValue')).toBeUndefined();
  });

  // The change above must not keep any OTHER dialog from closing: a dialog
  // without preventClose (the most of them) still closes on Escape, on its
  // ×, on the backdrop and when the browser closes it; one with it (an
  // install on its way, a reset password being shown) closes on none.
  it('a dialog without preventClose still closes on Escape, its × and the backdrop', async () => {
    for (const how of ['escape', 'x', 'backdrop'] as const) {
      const { w, dlg } = await opened(false);
      if (how === 'escape') {
        const ev = new Event('cancel', { cancelable: true });
        dlg.dispatchEvent(ev);
        expect(ev.defaultPrevented, how).toBe(false);
      } else if (how === 'x') {
        await w.find('button[aria-label]').trigger('click');
      } else {
        dlg.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      }
      expect(w.emitted('update:modelValue'), how).toEqual([[false]]);
      expect(w.emitted('close'), how).toHaveLength(1);
      w.unmount();
    }
  });

  it('a dialog with preventClose closes on none of them, and has no ×', async () => {
    const { w, dlg } = await opened(true);
    const ev = new Event('cancel', { cancelable: true });
    dlg.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(w.find('button[aria-label]').exists()).toBe(false);
    dlg.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(w.emitted('update:modelValue')).toBeUndefined();
    expect(w.emitted('close')).toBeUndefined();
  });
});
