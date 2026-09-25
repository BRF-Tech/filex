// A dialog whose work is still on the server says so, does not take the same
// order twice, and shows the answer when the server says no.
//
// ⚠ Rename, New folder and Delete sent their request and waited with the
// button live and nothing on screen. Renaming a folder on an object store
// copies every object inside the request, so a second press met the
// half-copied folder and was refused as "already here". When the server said
// no to a new folder or a delete, the dialog stayed open with nothing in it:
// the failure went to the host's console only (audit, 2026-09-26).
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import RenameModal from '@brftech/filex-core/src/modals/RenameModal.vue';
import NewFolderModal from '@brftech/filex-core/src/modals/NewFolderModal.vue';
import DeleteConfirmModal from '@brftech/filex-core/src/modals/DeleteConfirmModal.vue';

afterEach(() => {
  document.body.innerHTML = '';
});

function button(label: RegExp): HTMLButtonElement | undefined {
  return Array.from(document.body.querySelectorAll('button')).find((b) =>
    label.test(b.textContent ?? ''),
  ) as HTMLButtonElement | undefined;
}

function typeAndEnter(value: string) {
  const input = document.body.querySelector('input[type="text"]') as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event('input'));
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
}

function alertText(): string {
  return document.body.querySelector('[role="alert"]')?.textContent?.trim() ?? '';
}

describe('Rename while the rename runs', () => {
  it('keeps Save shut, says what it is doing, and ignores Enter', async () => {
    const w = mount(RenameModal, {
      props: { open: true, locale: 'en', currentName: 'Kayıtlar', busy: true },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    const save = button(/Renaming/);
    expect(save, 'the busy label').toBeTruthy();
    expect(save!.disabled).toBe(true);
    typeAndEnter('Arşiv');
    await w.vm.$nextTick();
    expect(w.emitted('submit'), 'a second order while the first runs').toBeUndefined();
    w.unmount();
  });

  it('takes the order again once it is free, in Turkish too', async () => {
    const w = mount(RenameModal, {
      props: { open: true, locale: 'tr', currentName: 'Kayıtlar', busy: false },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    expect(button(/^\s*Kaydet\s*$/)?.disabled).toBe(false);
    await w.setProps({ busy: true });
    expect(button(/adlandırılıyor/)?.disabled).toBe(true);
    await w.setProps({ busy: false });
    typeAndEnter('Arşiv');
    await w.vm.$nextTick();
    expect(w.emitted('submit')?.at(-1)).toEqual(['Arşiv']);
    w.unmount();
  });
});

describe('New folder', () => {
  it('shows the server’s answer in the dialog that asked', async () => {
    const w = mount(NewFolderModal, {
      props: { open: true, locale: 'en', error: null },
      attachTo: document.body,
    });
    await w.setProps({ error: 'You do not have permission to do this.' });
    expect(alertText()).toBe('You do not have permission to do this.');
    w.unmount();
  });

  it('keeps Create shut while the folder is being made, and ignores Enter', async () => {
    const w = mount(NewFolderModal, {
      props: { open: true, locale: 'en', busy: true },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    const create = button(/Creating/);
    expect(create, 'the busy label').toBeTruthy();
    expect(create!.disabled).toBe(true);
    typeAndEnter('2026');
    await w.vm.$nextTick();
    expect(w.emitted('submit')).toBeUndefined();
    w.unmount();
  });
});

describe('Delete', () => {
  it('shows the server’s answer and keeps the button shut while it works', async () => {
    const w = mount(DeleteConfirmModal, {
      props: { open: true, locale: 'tr', count: 3, busy: true, error: null },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    const confirm = button(/taşınıyor/);
    expect(confirm, 'the busy label').toBeTruthy();
    expect(confirm!.disabled).toBe(true);
    confirm!.click();
    expect(w.emitted('confirm')).toBeUndefined();

    await w.setProps({ busy: false, error: 'Bu işlem için yetkiniz yok.' });
    expect(alertText()).toBe('Bu işlem için yetkiniz yok.');
    button(/^\s*Çöpe at\s*$/)!.click();
    expect(w.emitted('confirm')).toHaveLength(1);
    w.unmount();
  });
});
