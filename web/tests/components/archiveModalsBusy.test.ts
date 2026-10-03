// The archive dialogs send once per press, Enter included, and "Extract here"
// says it is working while the server reads the archive.
//
// ⚠ Before the server can queue an extraction it downloads the whole archive
// and inspects it, which for a large one on an object store is minutes. The
// dialogs' buttons waited, but Enter in their name box sent the form again: a
// second download, a second inspection, a second job. "Extract here" has no
// dialog, so it showed nothing at all in that time and a second choice of it
// started another.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import ArchiveExtractModal from '@brftech/filex-core/src/modals/ArchiveExtractModal.vue';
import ArchiveCreateModal from '@brftech/filex-core/src/modals/ArchiveCreateModal.vue';
import ArchivePasswordModal from '@brftech/filex-core/src/modals/ArchivePasswordModal.vue';

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

// The explorer's one busy rule (modals/Modal `busy`): while the server reads
// the archive, Escape and × do not close the dialog under its request.
describe.each([
  ['Extract to folder', ArchiveExtractModal, { archiveName: 'fotolar.zip', suggestedFolder: 'fotolar' }],
  ['Create archive', ArchiveCreateModal, { count: 2, suggestedName: 'yedek' }],
  ['Archive password', ArchivePasswordModal, { archiveName: 'gizli.zip' }],
] as const)('"%s" while busy', (_n, component, extra) => {
  it('is not closed by Escape or ×', async () => {
    const w = mount(component as never, {
      props: { open: true, locale: 'en', busy: true, ...extra },
      attachTo: document.body,
    });
    await nextTick();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    const close = document.querySelector('.fe-modal__close') as HTMLButtonElement;
    expect(close.disabled, '× looked live').toBe(true);
    expect(w.emitted('close')).toBeUndefined();
    expect(document.querySelector('[role="dialog"]')?.getAttribute('aria-busy')).toBe('true');
    w.unmount();
  });
});

describe('"Extract here"', () => {
  // FileExplorer is not mounted in unit tests; its wiring is read off the
  // source, like the other explorer wiring tests.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const start = src.slice(src.indexOf('async function startArchiveExtraction'), src.indexOf('async function submitArchiveExtract'));

  it('a second choice while the first is being prepared starts nothing, and says so', () => {
    // ⚠ Scoped to THAT archive: #67's shared flag dropped an "Extract here"
    // without a word whenever an unrelated archive was being created.
    expect(start).toMatch(
      /if \(archiveExtracting\.value\.has\(target\.path\)\) \{\s*flashToast\(t\('archive\.already_preparing', \{ name: target\.basename \}\)\);\s*return;\s*\}/,
    );
    expect(start, 'the extraction still waits on an archive being created').not.toMatch(/archiveBusy\.value/);
    const create = src.slice(src.indexOf('async function submitArchiveCreate'), src.indexOf('async function startArchiveExtraction'));
    expect(create).toMatch(/archiveCreateBusy\.value = true;/);
    expect(src).toMatch(/<ArchiveCreateModal[\s\S]*?:busy="archiveCreateBusy"/);
    expect(src).toMatch(/<ArchiveExtractModal[\s\S]*?:busy="!!archiveTarget && archiveExtracting\.has\(archiveTarget\.path\)"/);
  });

  it('says the archive is being read while the server prepares it', () => {
    expect(start).toContain("t('archive.preparing_extract'");
  });
});
