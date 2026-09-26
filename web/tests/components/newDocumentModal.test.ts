// The New document dialog, mounted (#56).
//
// The dialog used to draw the type's extension as a read-only suffix beside
// the name field, so a Plain text document was always `<name>.txt` and
// `LICENSE`, `Makefile`, `test.conf` or `example.custom` could not be made.
// What is proved here is the dialog's half of the fix: the field holds the
// whole name, the type only prefills it, a type switch keeps what the person
// typed, and Create sends the name as the WHOLE name (`exactName`). The name
// arithmetic itself is in tests/lib/newDocName.test.ts.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import NewDocumentModal from '@brftech/filex-core/src/modals/NewDocumentModal.vue';
import type { NewDocType } from '@brftech/filex-core/src/types/FileNode';

const TYPES: NewDocType[] = [
  { ext: 'txt', group: 'text', mime: 'text/plain; charset=utf-8', ext_required: false },
  { ext: 'md', group: 'text', mime: 'text/markdown; charset=utf-8', ext_required: false },
  {
    ext: 'docx',
    group: 'document',
    mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    requires: 'onlyoffice',
    ext_required: true,
  },
];

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};
const debounce = () => new Promise((r) => setTimeout(r, 260));

let mounted: VueWrapper | null = null;
afterEach(() => {
  mounted?.unmount();
  mounted = null;
});

async function open(existing: string[] = [], types: NewDocType[] = TYPES) {
  const index = vi.fn(async () => ({
    adapter: 'main',
    storages: ['main'],
    dirname: 'main://',
    read_only: false,
    perm: 'owner',
    files: existing.map((n) => ({ path: `main://${n}`, basename: n, type: 'file' })),
  }));
  const newFile = vi.fn(async (path: string, name: string, type: string) => ({
    path: `${path}${name}`,
    name,
    ext: type,
    size: 0,
    mime: 'text/plain',
  }));
  const w = mount(NewDocumentModal, {
    attachTo: document.body,
    props: {
      open: false,
      locale: 'en',
      api: { index, newFile } as never,
      types,
      currentPath: 'main://',
      storages: ['main'],
      onlyOfficeReady: true,
    },
  });
  mounted = w;
  await w.setProps({ open: true });
  await settle();
  return { w, newFile, input: () => w.get<HTMLInputElement>('[data-testid="newdoc-name"]') };
}

describe('the name field holds the whole name', () => {
  it('is prefilled with the type’s default extension, and draws no read-only suffix', async () => {
    const { w, input } = await open();
    expect(input().element.value).toBe('Untitled.txt');
    expect(w.find('.fe-newdoc__suffix').exists()).toBe(false);
  });

  it('focusing it selects the stem, like a rename', async () => {
    const { input } = await open();
    await input().trigger('focus');
    await settle();
    expect(input().element.selectionStart).toBe(0);
    expect(input().element.selectionEnd).toBe('Untitled'.length);
  });

  it('an untouched suggestion follows the type', async () => {
    const { w, input } = await open();
    await w.get('[data-testid="newdoc-type-md"]').trigger('click');
    await settle();
    expect(input().element.value).toBe('Untitled.md');
  });
});

describe('switching the type keeps what the person typed', () => {
  it('swaps the default extension, and leaves the person’s own alone', async () => {
    const { w, input } = await open();
    await input().setValue('notes.txt');
    await w.get('[data-testid="newdoc-type-md"]').trigger('click');
    await settle();
    expect(input().element.value).toBe('notes.md');

    await input().setValue('LICENSE');
    await w.get('[data-testid="newdoc-type-txt"]').trigger('click');
    await settle();
    expect(input().element.value).toBe('LICENSE');
  });
});

describe('Create sends the whole name', () => {
  it.each(['LICENSE', 'test.conf', 'example.custom'])('%s as Plain text', async (name) => {
    const { w, newFile, input } = await open();
    await input().setValue(name);
    await debounce();
    await settle();
    const create = w.get('[data-testid="newdoc-create"]');
    expect(create.attributes('disabled')).toBeUndefined();
    await create.trigger('click');
    await settle();
    expect(newFile).toHaveBeenCalledWith('main://', name, 'txt', { exactName: true });
    expect(w.emitted('created')?.[0]?.[0]).toMatchObject({ name, ext: 'txt' });
  });

  it('warns about a name already in the folder — the name as typed, not with .txt added', async () => {
    const { w, input } = await open(['LICENSE']);
    await input().setValue('LICENSE');
    await debounce();
    await settle();
    expect(w.get('[data-testid="newdoc-collision"]').text()).toContain('LICENSE is already here');
    expect(w.get('[data-testid="newdoc-create"]').attributes('disabled')).toBeDefined();
  });
});

describe('where the extension is not optional', () => {
  it('an office type says it keeps its extension, and what the file will be called', async () => {
    const { w, input } = await open();
    await w.get('[data-testid="newdoc-type-docx"]').trigger('click');
    await settle();
    expect(input().element.value).toBe('Untitled.docx');
    await input().setValue('report');
    await settle();
    expect(w.get('[data-testid="newdoc-ext-hint"]').text()).toContain('report.docx');
    expect(w.get('[data-testid="newdoc-create"]').attributes('disabled')).toBeUndefined();
  });

  it('an empty .docx made as Plain text is refused before the server refuses it', async () => {
    const { w, newFile, input } = await open();
    await input().setValue('x.docx');
    await settle();
    expect(w.get('[data-testid="newdoc-name-error"]').text()).toContain('.docx');
    const create = w.get('[data-testid="newdoc-create"]');
    expect(create.attributes('disabled')).toBeDefined();
    await create.trigger('click');
    expect(newFile).not.toHaveBeenCalled();
  });

  it('a server from before #56 appends to every type, and the dialog says so instead of promising LICENSE', async () => {
    const old = TYPES.map(({ ext_required: _drop, ...rest }) => rest as NewDocType);
    const { w, input } = await open([], old);
    await input().setValue('LICENSE');
    await settle();
    expect(w.get('[data-testid="newdoc-ext-hint"]').text()).toContain('LICENSE.txt');
  });
});
