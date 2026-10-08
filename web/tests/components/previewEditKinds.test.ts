// Which files the preview offers Edit on, and which it opens as an office
// document, is the SERVER's answer (`capabilities.edit_kinds`, #211 audit B2),
// seeded by the test setup from the drift file the Go tests hold to it.
//
// ⚠ The list that stood in PreviewModal disagreed with save-text both ways:
// Edit on `.graphql` and `.mmd`, which save-text refused with a 415, and no
// Edit on `.properties`, `.lua` or a `Makefile`, which it saves. And the
// explorer's own office list had no `.docm`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import { resetServerRules, takeServerRules } from '@brftech/filex-core/src/lib/serverRules';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const RULES = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/rule-mirrors.json');

const fileNamed = (basename: string, extension: string, mime = '') =>
  ({
    path: `depo://${basename}`,
    basename,
    type: 'file',
    extension,
    mime_type: mime,
    size: 12,
    last_modified: 1_757_376_000,
  }) as FileNode;

const opened: VueWrapper[] = [];
function viewer(file: FileNode, extra: Record<string, unknown> = {}) {
  const w = mount(PreviewModal, {
    props: {
      open: true,
      locale: 'en',
      openMode: 'view',
      newTabEnabled: true,
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      file,
      ...extra,
    },
  });
  opened.push(w);
  return w;
}
const editButton = (w: VueWrapper) =>
  w.findAll('button.fe-viewer__act').filter((b) => b.attributes('title') === en['viewer.edit']);

const settle = async () => {
  await new Promise((r) => setTimeout(r, 0));
  await flushPromises();
};

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200, statusText: 'OK', text: async () => '' }));
});
afterEach(() => {
  opened.splice(0).forEach((w) => w.unmount());
  // Put the server's rules back for whatever runs next in this file.
  takeServerRules(JSON.parse(readFileSync(RULES, 'utf8')));
});

describe('Edit is offered on exactly what the server saves', () => {
  it.each([
    ['app.properties', 'properties'],
    ['init.lua', 'lua'],
    ['schema.graphql', 'graphql'],
    ['Makefile', ''],
  ])('%s has an Edit', async (name, ext) => {
    const w = viewer(fileNamed(name, ext));
    await settle();
    expect(editButton(w), `${name} should offer Edit`).toHaveLength(1);
  });

  it('a .csv drawn as a table has no Edit of its own (its viewer only shows)', async () => {
    const w = viewer(fileNamed('data.csv', 'csv', 'text/csv'));
    await settle();
    expect(editButton(w)).toHaveLength(0);
  });

  it('a .docm is an office document — the old list did not know it', async () => {
    const w = viewer(fileNamed('macro.docm', 'docm'), { onlyOfficeBase: null });
    await settle();
    expect(w.find('[data-testid="office-fallback"]').exists()).toBe(true);
  });

  it('before the server has said, nothing is guessed', async () => {
    resetServerRules();
    const w = viewer(fileNamed('app.properties', 'properties'));
    await settle();
    expect(editButton(w)).toHaveLength(0);
  });
});
