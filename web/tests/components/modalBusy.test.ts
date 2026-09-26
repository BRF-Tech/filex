// One rule for a dialog whose work is still on its way, in the one dialog every
// explorer screen is drawn in (modals/Modal.vue).
//
// ⚠ The series that made the explorer say what it is doing (#59, #65) grew
// three different answers to "may this dialog close now?": Rename, New folder
// and Delete closed anyway and lost the server's refusal; an app's screen
// silently refused to close while its × looked live; the converter blocked
// its backdrop and asked with the browser's own confirm() — unthemed, not
// right-to-left, and blocked outright in an iframe sandboxed without
// allow-modals. Now: `busy` on Modal — Escape and a click outside do nothing,
// × is shut, the dialog says aria-busy — and where the work may be given up
// by closing (the converter), × asks INSIDE the dialog.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import Modal from '@brftech/filex-core/src/modals/Modal.vue';

const mounted: VueWrapper[] = [];

function open(props: Record<string, unknown>) {
  const w = mount(Modal, {
    props: { open: true, title: 'Rename', locale: 'en', ...props },
    slots: { default: '<input type="text" />' },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

function escape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
}

function clickOutside() {
  const backdrop = document.querySelector('.fe-modal__backdrop') as HTMLElement;
  backdrop.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  backdrop.dispatchEvent(new MouseEvent('click', { bubbles: true }));
}

function closeButton() {
  return document.querySelector('.fe-modal__close') as HTMLButtonElement;
}

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

describe('a dialog whose work is on its way', () => {
  it('is not closed by Escape, a click outside or ×, and says it is busy', async () => {
    const w = open({ busy: true });
    await w.vm.$nextTick();
    escape();
    clickOutside();
    expect(closeButton().disabled, '× looked live while it did nothing').toBe(true);
    closeButton().click();
    expect(w.emitted('close')).toBeUndefined();
    expect(document.querySelector('[role="dialog"]')?.getAttribute('aria-busy')).toBe('true');
  });

  it('closes as before once the work is done', async () => {
    const w = open({ busy: true });
    await w.setProps({ busy: false });
    expect(document.querySelector('[role="dialog"]')?.hasAttribute('aria-busy')).toBe(false);
    expect(closeButton().disabled).toBe(false);
    escape();
    expect(w.emitted('close')).toHaveLength(1);
    clickOutside();
    closeButton().click();
    expect(w.emitted('close')).toHaveLength(3);
  });
});

describe('a dialog whose work can be given up by closing it', () => {
  const busyClose = { question: 'The conversion is still running. Stop it and close the window?' };

  it('asks inside the dialog, never with the browser’s confirm()', async () => {
    const native = vi.fn(() => true);
    vi.stubGlobal('confirm', native);
    const w = open({ busy: true, busyClose });
    await w.vm.$nextTick();
    expect(closeButton().disabled, 'a stoppable job keeps × live').toBe(false);
    closeButton().click();
    await w.vm.$nextTick();
    expect(native).not.toHaveBeenCalled();
    const ask = document.querySelector('[data-testid="modal-busy-close"]');
    expect(ask?.textContent).toContain(busyClose.question);
    expect(w.emitted('close')).toBeUndefined();
    // Escape and an outside click still do nothing while it runs.
    clickOutside();
    expect(w.emitted('close')).toBeUndefined();
  });

  it('keeps going on "Keep going", and closes on "Stop and close"', async () => {
    const w = open({ busy: true, busyClose });
    await w.vm.$nextTick();
    closeButton().click();
    await w.vm.$nextTick();
    (document.querySelector('[data-testid="modal-busy-close-keep"]') as HTMLButtonElement).click();
    await w.vm.$nextTick();
    expect(document.querySelector('[data-testid="modal-busy-close"]')).toBeNull();
    expect(w.emitted('close')).toBeUndefined();

    closeButton().click();
    await w.vm.$nextTick();
    const stop = document.querySelector('[data-testid="modal-busy-close-confirm"]') as HTMLButtonElement;
    expect(stop.textContent?.trim()).toBe('Stop and close');
    stop.click();
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('drops the question when the work ends on its own, and speaks Turkish', async () => {
    const w = open({ busy: true, busyClose, locale: 'tr' });
    await w.vm.$nextTick();
    closeButton().click();
    await w.vm.$nextTick();
    expect(document.querySelector('[data-testid="modal-busy-close-keep"]')?.textContent?.trim()).toBe('Devam etsin');
    expect(document.querySelector('[data-testid="modal-busy-close-confirm"]')?.textContent?.trim()).toBe(
      'Durdur ve kapat',
    );
    await w.setProps({ busy: false });
    expect(document.querySelector('[data-testid="modal-busy-close"]')).toBeNull();
  });

  it('names its own way out when closing stops nothing', async () => {
    const w = open({ busy: true, busyClose: { question: 'Still saving.', confirm: 'Close' } });
    await w.vm.$nextTick();
    closeButton().click();
    await w.vm.$nextTick();
    expect(document.querySelector('[data-testid="modal-busy-close-confirm"]')?.textContent?.trim()).toBe('Close');
  });
});
