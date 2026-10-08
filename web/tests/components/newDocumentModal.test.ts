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
  // #211 (audit B18): the server's dry run - taken by exact name (the create's
  // own Stat), and its " (n)" numbering for the free name.
  const newFileCheck = vi.fn(async (path: string, name: string) => {
    const taken = existing.includes(name);
    let suggested: string | undefined;
    if (taken) {
      const dot = name.lastIndexOf('.');
      const stem = dot > 0 ? name.slice(0, dot) : name;
      const ext = dot > 0 ? name.slice(dot) : '';
      for (let i = 2; i < 100 && !suggested; i++) {
        if (!existing.includes(`${stem} (${i})${ext}`)) suggested = `${stem} (${i})${ext}`;
      }
    }
    return { dry_run: true, path: `${path}${name}`, name, taken, ...(taken ? { code: 'NAME_TAKEN', suggested } : {}) };
  });
  const w = mount(NewDocumentModal, {
    attachTo: document.body,
    props: {
      open: false,
      locale: 'en',
      api: { index, newFile, newFileCheck } as never,
      types,
      currentPath: 'main://',
      storages: ['main'],
      onlyOfficeReady: true,
    },
  });
  mounted = w;
  await w.setProps({ open: true });
  await settle();
  return { w, newFile, newFileCheck, input: () => w.get<HTMLInputElement>('[data-testid="newdoc-name"]') };
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

  it('prefills the server’s free name when Untitled is taken', async () => {
    const { input, newFileCheck } = await open(['Untitled.txt', 'Untitled (2).txt']);
    expect(input().element.value).toBe('Untitled (3).txt');
    expect(newFileCheck).toHaveBeenCalledWith('main://', 'Untitled.txt', 'txt');
  });

  // The server compares names as the store does; the dialog no longer
  // lower-cases them itself, so a case-sensitive store's free name is free.
  it('takes the server’s word that another spelling is free', async () => {
    const { w, input } = await open(['report.md']);
    await input().setValue('Report.md');
    await debounce();
    await settle();
    expect(w.find('[data-testid="newdoc-collision"]').exists()).toBe(false);
    expect(w.get('[data-testid="newdoc-create"]').attributes('disabled')).toBeUndefined();
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

// The maintainer, 2026-09-27: an app's rows in the New menu (`new_documents`). The
// server lists them with the other types (a key, the app, its label); the
// dialog offers them under "Apps" in the app's own words, even where the
// built-in kind of the same extension is withheld, and asks the server for
// the ROW (its key), not the extension.
describe('an app’s rows', () => {
  const APP_TYPES: NewDocType[] = [
    ...TYPES,
    { ext: 'drawio', group: 'diagram', mime: 'application/vnd.jgraph.mxfile', requires: 'drawio', ext_required: true },
    {
      ext: 'drawio',
      key: 'app:drawio:drawio',
      group: 'app',
      mime: 'application/vnd.jgraph.mxfile',
      requires: 'app',
      ext_required: true,
      app: { plugin: 'drawio', view: 'editor', label: { en: 'Whiteboard (draw.io)', tr: 'Beyaz tahta (draw.io)' } },
    },
  ];

  it('are offered under Apps, in the app’s own words, and made by their key', async () => {
    const { w, newFile, input } = await open([], APP_TYPES);
    expect(w.find('[data-testid="newdoc-type-drawio"]').exists()).toBe(false);
    const tile = w.get('[data-testid="newdoc-type-app:drawio:drawio"]');
    expect(tile.text()).toContain('Whiteboard (draw.io)');
    expect(w.text()).toContain('Apps');
    await tile.trigger('click');
    await settle();
    expect(input().element.value).toBe('Untitled.drawio');
    await input().setValue('Plan');
    await debounce();
    await settle();
    await w.get('[data-testid="newdoc-create"]').trigger('click');
    await settle();
    expect(newFile).toHaveBeenCalledWith('main://', 'Plan', 'app:drawio:drawio', { exactName: true });
    expect(w.emitted('created')?.[0]?.[0]).toMatchObject({ app: { plugin: 'drawio', view: 'editor' } });
  });
});

describe('a kind an app makes is not said to be missing', () => {
  it('does not say "Diagrams need draw.io" when an app offers the .drawio row', async () => {
    const withApp: NewDocType[] = [
      ...TYPES,
      { ext: 'drawio', group: 'diagram', mime: 'application/vnd.jgraph.mxfile', requires: 'drawio', ext_required: true },
      {
        ext: 'drawio',
        key: 'app:drawio:drawio',
        group: 'app',
        mime: 'application/vnd.jgraph.mxfile',
        requires: 'app',
        ext_required: true,
        app: { plugin: 'drawio', view: 'editor', label: { en: 'draw.io diagram' } },
      },
    ];
    const { w } = await open([], withApp);
    const line = w.find('[data-testid="newdoc-withheld"]');
    expect(line.exists() ? line.text() : '').not.toMatch(/draw\.io/i);

    // Without the app, the line still says it.
    mounted?.unmount();
    mounted = null;
    const { w: w2 } = await open([], withApp.slice(0, -1));
    expect(w2.get('[data-testid="newdoc-withheld"]').text()).toMatch(/draw\.io/i);
  });
});
