// A rename that did not happen says why, in the dialog that asked for it.
//
// ⚠ Before this, every failure was invisible: the explorer only emitted an
// `error` event (the stock web app logs it to the console), and the dialog
// stayed open with nothing in it — the Save button simply did nothing. The
// server now refuses a rename onto a taken name with 409 NAME_TAKEN instead of
// replacing that file, which made the silence matter: the one answer a person
// can act on ("pick another name") was the one they never saw.
//
// The dialog also used to ignore a name with a slash in it, or "." / "..",
// without a word.
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import RenameModal from '@brftech/filex-core/src/modals/RenameModal.vue';

afterEach(() => {
  document.body.innerHTML = '';
});

function errorText(): string | null {
  return document.body.querySelector('[data-testid="rename-error"]')?.textContent?.trim() ?? null;
}

describe('RenameModal', () => {
  it('shows the error it is handed, where the name is typed', async () => {
    const w = mount(RenameModal, {
      props: { open: true, locale: 'en', currentName: 'a.txt', error: 'b.txt is already here. Choose another name.' },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    expect(errorText()).toBe('b.txt is already here. Choose another name.');
    expect(document.body.querySelector('[data-testid="rename-error"]')?.getAttribute('role')).toBe('alert');
    w.unmount();
  });

  it('drops a stale error as soon as the name is edited', async () => {
    const w = mount(RenameModal, {
      props: { open: true, locale: 'en', currentName: 'a.txt', error: 'b.txt is already here. Choose another name.' },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    const input = document.body.querySelector('input') as HTMLInputElement;
    input.value = 'c.txt';
    input.dispatchEvent(new Event('input'));
    await w.vm.$nextTick();
    expect(errorText()).toBeNull();
    w.unmount();
  });

  it('a new error from the server replaces the old one', async () => {
    const w = mount(RenameModal, {
      props: { open: true, locale: 'en', currentName: 'a.txt', error: null },
      attachTo: document.body,
    });
    await w.setProps({ error: 'Service unavailable' });
    expect(errorText()).toBe('Service unavailable');
    w.unmount();
  });

  for (const bad of ['x/y', 'x\\y', '.', '..']) {
    it(`says why ${JSON.stringify(bad)} is not a name instead of doing nothing`, async () => {
      const w = mount(RenameModal, {
        props: { open: true, locale: 'en', currentName: 'a.txt' },
        attachTo: document.body,
      });
      await w.vm.$nextTick();
      const input = document.body.querySelector('input') as HTMLInputElement;
      input.value = bad;
      input.dispatchEvent(new Event('input'));
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
      await w.vm.$nextTick();
      expect(w.emitted('submit')).toBeUndefined();
      expect(errorText()).toBe('Invalid character');
      w.unmount();
    });
  }

  it('a good name is submitted', async () => {
    const w = mount(RenameModal, {
      props: { open: true, locale: 'tr', currentName: 'a.txt' },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    const input = document.body.querySelector('input') as HTMLInputElement;
    input.value = 'b.txt';
    input.dispatchEvent(new Event('input'));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    await w.vm.$nextTick();
    expect(w.emitted('submit')).toEqual([['b.txt']]);
    expect(errorText()).toBeNull();
    w.unmount();
  });
});
