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

/** The server's sentences (`capabilities.newdoc_withheld`, #217), as an
 *  administrator's capabilities carry them. */
const WORDS = {
  onlyoffice: 'Office documents need an office engine, the Office editor app or ONLYOFFICE, and neither is set up here.',
  drawio: 'Diagrams need draw.io, which is not set up here.',
  admin: 'Set them up in the admin panel under Apps or External services.',
};

async function open(existing: string[] = [], types: NewDocType[] = TYPES, extra: Record<string, unknown> = {}) {
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
      withheldWords: WORDS,
      ...extra,
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

// #217 (filex-office-editor 0.1.0 measured in filex 0.55): an office editor
// app offers .docx, .xlsx and .pptx rows on an install with no document
// server. The built-in .odt/.ods/.odp rows stayed held back, so the dialog
// said "Office documents need a document server (ONLYOFFICE)" right above the
// app's own .docx row. The line is said only when NOTHING here makes the
// family - and its words are the server's.
describe('office documents an app makes are not said to need a document server (#217)', () => {
  const office = (ext: string, mime: string): NewDocType => ({ ext, group: 'document', mime, requires: 'onlyoffice', ext_required: true });
  const appRow = (ext: string, mime: string): NewDocType => ({
    ext,
    key: `app:office-editor:${ext}`,
    group: 'app',
    mime,
    requires: 'app',
    ext_required: true,
    app: { plugin: 'office-editor', view: 'editor', label: { en: `Office (${ext})` } },
  });
  const BUILT_IN: NewDocType[] = [
    { ext: 'txt', group: 'text', mime: 'text/plain; charset=utf-8', ext_required: false },
    office('docx', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'),
    office('xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'),
    office('pptx', 'application/vnd.openxmlformats-officedocument.presentationml.presentation'),
    office('odt', 'application/vnd.oasis.opendocument.text'),
    office('ods', 'application/vnd.oasis.opendocument.spreadsheet'),
    office('odp', 'application/vnd.oasis.opendocument.presentation'),
  ];
  const APP_ROWS: NewDocType[] = [
    appRow('docx', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'),
    appRow('xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'),
    appRow('pptx', 'application/vnd.openxmlformats-officedocument.presentationml.presentation'),
  ];
  const line = (w: VueWrapper) => {
    const el = w.find('[data-testid="newdoc-withheld"]');
    return el.exists() ? el.text() : '';
  };

  it('says nothing about ONLYOFFICE while an app offers office rows', async () => {
    const { w } = await open([], [...BUILT_IN, ...APP_ROWS], { onlyOfficeReady: false, canConfigure: true });
    expect(w.find('[data-testid="newdoc-type-app:office-editor:docx"]').exists()).toBe(true);
    expect(w.find('[data-testid="newdoc-type-odt"]').exists(), 'the built-in rows stay held back').toBe(false);
    expect(line(w)).not.toMatch(/ONLYOFFICE|document server/i);
    expect(line(w)).not.toContain(WORDS.admin);
  });

  it('still says it - in the server’s words - when nothing makes them', async () => {
    const { w } = await open([], BUILT_IN, { onlyOfficeReady: false, canConfigure: true });
    expect(line(w)).toBe(`${WORDS.onlyoffice} ${WORDS.admin}`);
  });

  it('the line is the server’s: a sentence it did not send is not made up here', async () => {
    const { w } = await open([], BUILT_IN, { onlyOfficeReady: false, withheldWords: { onlyoffice: 'Bu sunucunun cümlesi.' } });
    expect(line(w)).toBe('Bu sunucunun cümlesi.');
    mounted?.unmount();
    mounted = null;
    const { w: older } = await open([], BUILT_IN, { onlyOfficeReady: false, withheldWords: null });
    expect(older.find('[data-testid="newdoc-withheld"]').exists()).toBe(false);
  });
});

// 0.55 full chain (Firefox, e2e 168): on a slow server the dialog's first
// suggestion (the dry run for "Untitled.txt") answered after the person had
// started typing, and replaced the field under them: "Untitled.txtnutes.txt".
// A suggestion only ever fills a name nobody has typed.
describe('a late suggestion does not replace what the person typed', () => {
  it('keeps the typed name when the dry run answers after the first keystroke', async () => {
    let answer: (v: { dry_run: true; path: string; name: string; taken: boolean }) => void = () => undefined;
    const newFileCheck = vi.fn(
      (path: string, name: string) =>
        new Promise<{ dry_run: true; path: string; name: string; taken: boolean }>((resolve) => {
          if (name.startsWith('Untitled')) answer = resolve;
          else resolve({ dry_run: true, path: `${path}${name}`, name, taken: false });
        }),
    );
    const index = vi.fn(async () => ({
      adapter: 'main',
      storages: ['main'],
      dirname: 'main://',
      read_only: false,
      perm: 'owner',
      files: [],
    }));
    const w = mount(NewDocumentModal, {
      attachTo: document.body,
      props: {
        open: false,
        locale: 'en',
        api: { index, newFile: vi.fn(), newFileCheck } as never,
        types: TYPES,
        currentPath: 'main://',
        storages: ['main'],
        onlyOfficeReady: true,
      },
    });
    mounted = w;
    await w.setProps({ open: true });
    await settle();
    expect(newFileCheck).toHaveBeenCalledWith('main://', 'Untitled.txt', 'txt');

    const input = w.get<HTMLInputElement>('[data-testid="newdoc-name"]');
    await input.setValue('minutes.txt');
    answer({ dry_run: true, path: 'main://Untitled.txt', name: 'Untitled.txt', taken: false });
    await settle();
    expect(input.element.value).toBe('minutes.txt');
  });
});
