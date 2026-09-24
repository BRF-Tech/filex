// The folder chooser, mounted.
//
// The rules it rests on are tested in tests/lib/destinationTree.test.ts; what
// is proved here is that the dialog OBEYS them — that the Choose button is off
// where it should be off, that it says why, and that a blocked folder cannot be
// walked into. A picker whose rules are right and whose button is enabled
// anyway is a picker that queues an operation the backend then refuses.

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import DestinationPickerModal from '@brftech/filex-core/src/modals/DestinationPickerModal.vue';

type Listing = {
  adapter: string;
  storages: string[];
  dirname: string;
  read_only: boolean;
  perm?: string;
  files: Array<Record<string, unknown>>;
};

const d = (path: string, perm?: string) => ({
  path,
  basename: path.replace(/\/$/, '').split('/').pop(),
  type: 'dir',
  ...(perm === undefined ? {} : { perm }),
});

/** A tiny fake tree across two storages. */
const TREE: Record<string, Listing> = {
  'main://': {
    adapter: 'main',
    storages: ['main', 's3'],
    dirname: 'main://',
    read_only: false,
    perm: 'owner',
    files: [d('main://docs'), d('main://arsiv'), d('main://salt', 'viewer')],
  },
  'main://docs': {
    adapter: 'main',
    storages: ['main', 's3'],
    dirname: 'main://docs',
    read_only: false,
    perm: 'owner',
    files: [d('main://docs/2026'), { path: 'main://docs/nda.pdf', basename: 'nda.pdf', type: 'file' }],
  },
  'main://docs/2026': {
    adapter: 'main',
    storages: ['main', 's3'],
    dirname: 'main://docs/2026',
    read_only: false,
    perm: 'owner',
    files: [],
  },
  'main://salt': {
    adapter: 'main',
    storages: ['main', 's3'],
    dirname: 'main://salt',
    read_only: false,
    perm: 'viewer',
    files: [],
  },
  's3://': {
    adapter: 's3',
    storages: ['main', 's3'],
    dirname: 's3://',
    read_only: false,
    perm: 'editor',
    files: [d('s3://yedek')],
  },
};

async function open(props: Record<string, unknown> = {}) {
  const idx = vi.fn(async (p: string) => {
    const hit = TREE[p];
    if (!hit) throw new Error('404');
    return hit;
  });
  const w = mount(DestinationPickerModal, {
    props: { open: true, api: { index: idx }, locale: 'en', storages: ['main', 's3'], ...props },
  });
  await flush();
  return { w, idx };
}

async function flush() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
  await new Promise((r) => setTimeout(r, 0));
}

const rowFor = (w: ReturnType<typeof mount>, label: string) =>
  w.find(`[data-testid="destpicker-row-${label}"]`);
const confirm = (w: ReturnType<typeof mount>) => w.find('[data-testid="destpicker-confirm"]');
const reason = (w: ReturnType<typeof mount>) => w.find('[data-testid="destpicker-reason"]');

describe('DestinationPickerModal', () => {
  it('opens at the current folder and lists its subfolders', async () => {
    const { w, idx } = await open({ startAt: 'main://' });
    expect(idx).toHaveBeenCalledWith('main://');
    expect(rowFor(w, 'docs').exists()).toBe(true);
    expect(rowFor(w, 'arsiv').exists()).toBe(true);
  });

  it('spans storages — the drives level lists every storage', async () => {
    const { w } = await open({ startAt: '' });
    // No current folder + two storages = the drives list.
    expect(rowFor(w, 'main').exists()).toBe(true);
    expect(rowFor(w, 's3').exists()).toBe(true);
    // …and a drive cannot itself be the destination; you open it first.
    expect(confirm(w).attributes('disabled')).toBeDefined();
    expect(reason(w).text()).toMatch(/Open a storage/i);
  });

  it('walks into a folder and lets it be chosen', async () => {
    const { w } = await open({ startAt: 'main://' });
    await rowFor(w, 'docs').trigger('click');
    await flush();
    expect(confirm(w).attributes('disabled')).toBeUndefined();
    await confirm(w).trigger('click');
    expect(w.emitted('pick')).toEqual([['main://docs']]);
  });

  // ⚠ Listed, not hidden: the grant that allows writing may be on a subfolder,
  // so the row has to stay openable. What must not happen is Choose staying
  // enabled inside it.
  it('shows a read-only folder but will not let you choose it', async () => {
    const { w } = await open({ startAt: 'main://' });
    const row = rowFor(w, 'salt');
    expect(row.exists()).toBe(true);
    expect(row.classes()).toContain('is-locked');
    await row.trigger('click');
    await flush();
    expect(confirm(w).attributes('disabled')).toBeDefined();
    expect(reason(w).text()).toMatch(/cannot write/i);
  });

  it('marks the folder being moved and refuses to open it', async () => {
    const { w } = await open({ startAt: 'main://', moving: ['main://docs'], mode: 'move' });
    const row = rowFor(w, 'docs');
    expect(row.classes()).toContain('is-blocked');
    expect(row.attributes('disabled')).toBeDefined();
    // Clicking a dead end must not navigate into it.
    await row.trigger('click');
    await flush();
    expect(rowFor(w, 'arsiv').exists()).toBe(true);
  });

  // The self/descendant refusal has to hold for the CURRENT folder too, not
  // just for the rows: the user can reach `main://docs` through the breadcrumb
  // or by having started there.
  it('refuses the current folder when it is inside what is being moved', async () => {
    const { w } = await open({ startAt: 'main://docs', moving: ['main://docs'], mode: 'move' });
    expect(confirm(w).attributes('disabled')).toBeDefined();
    expect(reason(w).text()).toMatch(/into itself/i);
  });

  // The descendant half of the rule, which the 'self' case above cannot reach:
  // the user walked INTO a subfolder of what they are moving.
  it('refuses a folder inside what is being moved', async () => {
    const { w } = await open({
      startAt: 'main://docs/2026',
      moving: ['main://docs'],
      mode: 'move',
    });
    expect(confirm(w).attributes('disabled')).toBeDefined();
    expect(reason(w).text()).toMatch(/one of its own subfolders/i);
  });

  it('names its button after the verb', async () => {
    const { w: move } = await open({ startAt: 'main://', mode: 'move' });
    expect(confirm(move).text()).toBe('Move here');
    const { w: copy } = await open({ startAt: 'main://', mode: 'copy' });
    expect(confirm(copy).text()).toBe('Copy here');
  });

  it('speaks Turkish', async () => {
    const { w } = await open({ startAt: 'main://', mode: 'move', locale: 'tr' });
    expect(confirm(w).text()).toBe('Buraya taşı');
  });

  it('says so when a folder cannot be listed instead of showing an empty one', async () => {
    const idx = vi.fn(async () => {
      throw new Error('boom');
    });
    const w = mount(DestinationPickerModal, {
      props: { open: true, api: { index: idx }, locale: 'en', storages: ['main'], startAt: 'main://' },
    });
    await flush();
    expect(reason(w).text()).toMatch(/cannot be opened/i);
    expect(confirm(w).attributes('disabled')).toBeDefined();
  });

  it('caches a listing so walking back up does not re-fetch', async () => {
    const { w, idx } = await open({ startAt: 'main://' });
    await rowFor(w, 'docs').trigger('click');
    await flush();
    await w.find('[data-testid="destpicker-up"]').trigger('click');
    await flush();
    expect(idx.mock.calls.filter((c) => c[0] === 'main://')).toHaveLength(1);
  });

  it('stays inert while the parent is running the operation', async () => {
    const { w } = await open({ startAt: 'main://docs', busy: true });
    expect(confirm(w).attributes('disabled')).toBeDefined();
  });
});

// An app plugin's `file-chooser` asks the SAME dialog for a file: the files
// are listed beside the folders, a file is ticked rather than walked into,
// and the answer is the file's wire path. A folder pick never sees a file
// row, so nothing above changes.
describe('DestinationPickerModal — pick: file', () => {
  it('folder mode never lists files', async () => {
    const { w } = await open({ startAt: 'main://docs' });
    expect(rowFor(w, '2026').exists()).toBe(true);
    expect(rowFor(w, 'nda.pdf').exists()).toBe(false);
  });

  it('lists files, ticks one, and answers its path', async () => {
    const { w } = await open({ startAt: 'main://docs', mode: 'choose', pick: 'file' });
    expect(rowFor(w, '2026').exists()).toBe(true);
    const file = rowFor(w, 'nda.pdf');
    expect(file.exists()).toBe(true);
    expect(confirm(w).attributes('disabled')).toBeDefined();
    expect(reason(w).text()).toMatch(/select a file/i);
    await file.trigger('click');
    await flush();
    expect(file.classes()).toContain('is-picked');
    expect(confirm(w).attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="destpicker-target"]').text()).toContain('nda.pdf');
    await confirm(w).trigger('click');
    expect(w.emitted('pick')).toEqual([['main://docs/nda.pdf']]);
  });

  it('walking into another folder drops the tick', async () => {
    const { w } = await open({ startAt: 'main://docs', mode: 'choose', pick: 'file' });
    await rowFor(w, 'nda.pdf').trigger('click');
    await rowFor(w, '2026').trigger('click');
    await flush();
    expect(confirm(w).attributes('disabled')).toBeDefined();
  });
});
