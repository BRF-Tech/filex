// The new-folder and rename dialogs refuse filex's own names in the reader's
// language before the request goes out. The server refuses them either way
// (403 RESERVED_NAME, backend/internal/api/handlers/reserved_guard.go); this is
// the courtesy of saying why, in Turkish for a Turkish reader, instead of an
// English error toast after the fact.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import NewFolderModal from '@brftech/filex-core/src/modals/NewFolderModal.vue';
import RenameModal from '@brftech/filex-core/src/modals/RenameModal.vue';
import { INTERNAL_DIR_NAMES, KEEP_MARKER_NAME } from '@brftech/filex-core/src/lib/internalPaths';

const mounted: VueWrapper[] = [];
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  document.body.innerHTML = '';
});

async function submitFolder(locale: 'en' | 'tr', value: string) {
  const w = mount(NewFolderModal, { props: { open: true, locale }, attachTo: document.body });
  mounted.push(w);
  await w.vm.$nextTick();
  const input = document.body.querySelector('input.fe-input') as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event('input'));
  (document.body.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
  await w.vm.$nextTick();
  return w;
}

async function submitRename(locale: 'en' | 'tr', value: string) {
  const w = mount(RenameModal, { props: { open: true, locale, currentName: 'Rapor.docx' }, attachTo: document.body });
  mounted.push(w);
  await w.vm.$nextTick();
  const input = document.body.querySelector('input.fe-input') as HTMLInputElement;
  input.value = value;
  input.dispatchEvent(new Event('input'));
  (document.body.querySelector('form') as HTMLFormElement).dispatchEvent(new Event('submit'));
  await w.vm.$nextTick();
  return w;
}

describe('reserved names in the new-folder dialog', () => {
  for (const name of [...INTERNAL_DIR_NAMES, KEEP_MARKER_NAME]) {
    it(`refuses ${name} and says why`, async () => {
      const w = await submitFolder('tr', name);
      expect(w.emitted('submit')).toBeUndefined();
      const err = document.body.querySelector('.fe-form__error')?.textContent ?? '';
      expect(err).toContain(name);
      expect(err).toContain('filex’in kendi kullanımına ayrılmış');
    });
  }
  it('still creates an ordinary folder, even one that starts with a dot', async () => {
    const w = await submitFolder('en', '.config');
    expect(w.emitted('submit')?.[0]).toEqual(['.config']);
  });
});

describe('reserved names in the rename dialog', () => {
  it('refuses .versions in English and in Turkish', async () => {
    let w = await submitRename('en', '.versions');
    expect(w.emitted('submit')).toBeUndefined();
    expect(document.body.querySelector('[data-testid="rename-error"]')?.textContent).toContain(
      'reserved for filex’s own use',
    );
    w.unmount();
    mounted.pop();
    document.body.innerHTML = '';
    w = await submitRename('tr', '.filex-trash');
    expect(w.emitted('submit')).toBeUndefined();
    expect(document.body.querySelector('[data-testid="rename-error"]')?.textContent).toContain('Başka bir ad seç');
  });
  it('renames to an ordinary name', async () => {
    const w = await submitRename('en', 'Rapor 2026.docx');
    expect(w.emitted('submit')?.[0]).toEqual(['Rapor 2026.docx']);
  });
});
