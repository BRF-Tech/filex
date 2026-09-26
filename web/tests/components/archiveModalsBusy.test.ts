// The archive dialogs send once per press, Enter included, and "Extract here"
// says it is working while the server reads the archive.
//
// ⚠ Before the server can queue an extraction it downloads the whole archive
// and inspects it, which for a large one on an object store is minutes. The
// dialogs' buttons waited, but Enter in their name box sent the form again: a
// second download, a second inspection, a second job. "Extract here" has no
// dialog, so it showed nothing at all in that time and a second choice of it
// started another.
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import ArchiveExtractModal from '@brftech/filex-core/src/modals/ArchiveExtractModal.vue';
import ArchiveCreateModal from '@brftech/filex-core/src/modals/ArchiveCreateModal.vue';

afterEach(() => {
  document.body.innerHTML = '';
});

describe('"Extract to folder"', () => {
  it('Enter while it is busy sends nothing more, and the box is shut', async () => {
    const w = mount(ArchiveExtractModal, {
      props: { open: false, locale: 'en', archiveName: 'fotolar.zip', suggestedFolder: 'fotolar' },
      attachTo: document.body,
    });
    await w.setProps({ open: true });
    await nextTick();
    await w.find('form').trigger('submit');
    expect(w.emitted('submit'), 'the first press').toHaveLength(1);

    await w.setProps({ busy: true });
    await w.find('form').trigger('submit');
    expect(w.emitted('submit'), 'Enter while busy sent it again').toHaveLength(1);
    expect(w.find('input').attributes('disabled')).toBeDefined();
    w.unmount();
  });
});

describe('"Create archive"', () => {
  it('Enter while it is busy sends nothing more', async () => {
    const w = mount(ArchiveCreateModal, {
      props: { open: false, locale: 'en', count: 2, suggestedName: 'yedek' },
      attachTo: document.body,
    });
    await w.setProps({ open: true });
    await nextTick();
    await w.setProps({ busy: true });
    await w.find('form').trigger('submit');
    expect(w.emitted('submit'), 'Enter while busy sent it').toBeUndefined();
    w.unmount();
  });
});

describe('"Extract here"', () => {
  // FileExplorer is not mounted in unit tests; its wiring is read off the
  // source, like the other explorer wiring tests.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const start = src.slice(src.indexOf('async function startArchiveExtraction'), src.indexOf('async function submitArchiveExtract'));

  it('a second choice while the first is being prepared starts nothing', () => {
    expect(start).toMatch(/if \(archiveBusy\.value\) return;/);
  });

  it('says the archive is being read while the server prepares it', () => {
    expect(start).toContain("t('archive.preparing_extract'");
  });
});
