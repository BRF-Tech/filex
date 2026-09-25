// ⌘K "Everywhere" hits are things you can ACT on, not only open — task #47.
//
// Before: a hit row was one button, and its only verb was "open" (the palette
// closed and `open-hit` fired). Downloading a file found by content meant
// opening its folder first; dragging it onto the desktop meant finding it in
// the listing. Both verbs already exist on a listing row, so a hit row gets
// the same two, through the same hooks — the palette only says WHICH hit.
//
// And the desktop app holds several accounts at once. Their hits arrive
// tagged with the account they came from, and the palette draws one group per
// account under a badge, each capped on its own.

import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import CommandPalette from '@brftech/filex-core/src/components/CommandPalette.vue';
import type { GlobalSearchHit } from '@brftech/filex-core/src/composables/useFileApi';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const settle = async (w: VueWrapper) => {
  // the palette debounces the everywhere query by 250 ms
  await new Promise((r) => setTimeout(r, 400));
  await w.vm.$nextTick();
};

let wrapper: VueWrapper | null = null;
afterEach(() => {
  wrapper?.unmount();
  wrapper = null;
});

function open(hits: GlobalSearchHit[], extra: Record<string, unknown> = {}) {
  wrapper = mount(CommandPalette, {
    props: {
      open: false,
      locale: 'en' as const,
      files: [] as FileNode[],
      viewMode: 'list' as const,
      globalSearch: async () => hits,
      ...extra,
    },
    attachTo: document.body,
  });
  return wrapper;
}

async function search(w: VueWrapper, q: string) {
  await w.setProps({ open: true });
  await w.vm.$nextTick();
  await w.find('.fe-cmdp__input').setValue(q);
  await settle(w);
}

const report: GlobalSearchHit = {
  id: 7, storage_id: 1, storage: 'docs', name: 'sözleşme.docx', path: 'Hukuk/sözleşme.docx',
  type: 'file', matched: 'content', snippet: '… «ceza» şartı …',
};
const folder: GlobalSearchHit = { id: 8, storage_id: 1, storage: 'docs', name: 'Hukuk', path: 'Hukuk', type: 'dir', matched: 'name' };

describe('a hit row can be downloaded without opening it', () => {
  it('the row has a Download button, and pressing it says which hit — nothing else', async () => {
    const w = open([report]);
    await search(w, 'ceza');
    const btn = w.find('[data-testid="palette-hit-download"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    expect(w.emitted('download-hit')?.[0]?.[0]).toMatchObject({ path: 'Hukuk/sözleşme.docx' });
    // Not an open: the palette stays up and nothing navigates.
    expect(w.emitted('open-hit')).toBeUndefined();
    expect(w.emitted('close')).toBeUndefined();
  });

  it('the host can withhold the verb for a hit it cannot serve', async () => {
    const w = open([report], { hitCan: (_h: GlobalSearchHit, action: string) => action !== 'download' });
    await search(w, 'ceza');
    expect(w.find('[data-testid="palette-hit-download"]').exists()).toBe(false);
  });

  it('choosing the row itself still opens it', async () => {
    const w = open([report]);
    await search(w, 'ceza');
    await w.find('.fe-cmdp__item--hit').trigger('click');
    expect(w.emitted('open-hit')?.[0]?.[0]).toMatchObject({ path: 'Hukuk/sözleşme.docx' });
  });
});

describe('a hit row can be dragged out', () => {
  it('the row is draggable and its dragstart names the hit', async () => {
    const w = open([report, folder]);
    await search(w, 'hukuk');
    const rows = w.findAll('.fe-cmdp__item--hit');
    expect(rows).toHaveLength(2);
    expect(rows[0].attributes('draggable')).toBe('true');
    await rows[0].trigger('dragstart');
    const got = w.emitted('drag-hit')?.[0];
    expect(got?.[0]).toMatchObject({ path: 'Hukuk/sözleşme.docx' });
    expect(got?.[1]).toBeInstanceOf(Event);
  });

  it('a hit the host cannot drag is not draggable at all', async () => {
    const w = open([report, folder], { hitCan: (h: GlobalSearchHit, action: string) => !(action === 'drag' && h.type === 'dir') });
    await search(w, 'hukuk');
    const rows = w.findAll('.fe-cmdp__item--hit');
    expect(rows[0].attributes('draggable')).toBe('true');
    expect(rows[1].attributes('draggable')).toBe('false');
    await rows[1].trigger('dragstart');
    expect(w.emitted('drag-hit')).toBeUndefined();
  });

  it('a drag let go INSIDE the palette is swallowed and reported — never an upload', async () => {
    const w = open([report]);
    await search(w, 'ceza');
    const backdrop = w.find('.fe-cmdp__backdrop');
    const ev = new Event('drop', { bubbles: true, cancelable: true });
    backdrop.element.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(w.emitted('drop-inside')).toHaveLength(1);
  });
});

describe('several accounts: one group per account, under its badge', () => {
  const mine = { id: 'acc-1', label: 'fm.example.com', detail: 'ada@example.com', color: '#4f7ce8' };
  const other = { id: 'acc-2', label: 'files.other.org', detail: 'ada@other.org', color: '#c2643a' };
  const tagged = (n: string, account: typeof mine): GlobalSearchHit => ({
    id: Number(n.replace(/\D/g, '')) || 1, storage_id: 1, storage: 'docs', name: `${n}.txt`, path: `${n}.txt`, type: 'file', account,
  });

  it('draws a badge per account, own account first, and the rows under it', async () => {
    const w = open([tagged('a1', mine), tagged('b1', other), tagged('a2', mine)]);
    await search(w, 'txt');
    const heads = w.findAll('[data-testid="palette-account-group"]');
    expect(heads.map((h) => h.attributes('data-account'))).toEqual(['acc-1', 'acc-2']);
    expect(heads[0].text()).toContain('fm.example.com');
    expect(heads[1].text()).toContain('files.other.org');
    const rows = w.findAll('.fe-cmdp__item--hit').map((r) => r.attributes('data-account'));
    expect(rows).toEqual(['acc-1', 'acc-1', 'acc-2']);
  });

  it('caps each account on its own', async () => {
    const lots = Array.from({ length: 11 }, (_, i) => tagged(`a${i + 1}`, mine));
    const w = open([...lots, tagged('b1', other)]);
    await search(w, 'txt');
    const rows = w.findAll('.fe-cmdp__item--hit').map((r) => r.attributes('data-account'));
    expect(rows.filter((r) => r === 'acc-1')).toHaveLength(8);
    expect(rows.filter((r) => r === 'acc-2')).toHaveLength(1);
  });

  it('the keyboard walks the rows in the order they are drawn', async () => {
    const w = open([tagged('a1', mine), tagged('b1', other), tagged('a2', mine)]);
    await search(w, 'txt');
    // ↓ twice from the first row lands on the third DRAWN row: b1 (a2 is drawn
    // second, inside its own account's group).
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }));
    await w.vm.$nextTick();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    await w.vm.$nextTick();
    expect(w.emitted('open-hit')?.[0]?.[0]).toMatchObject({ name: 'b1.txt', account: { id: 'acc-2' } });
  });

  it('a single account draws no badge at all', async () => {
    const w = open([report]);
    await search(w, 'ceza');
    expect(w.findAll('[data-testid="palette-account-group"]')).toHaveLength(0);
  });
});

// #71 — a bearer session's drag needs a link the server mints, and dragstart is
// too late to ask for it. The palette says when the pointer rests on / presses
// a hit it would let drag, and says nothing for one it would not.
describe('a hit row asks for its drag-out link before the drag', () => {
  it('hover and press on a draggable hit say which hit', async () => {
    const w = open([report]);
    await search(w, 'ceza');
    const row = w.find('.fe-cmdp__item--hit');
    await row.trigger('mouseenter');
    await row.trigger('pointerdown');
    const got = w.emitted('warm-hit') ?? [];
    expect(got).toHaveLength(2);
    expect(got[0]?.[0]).toMatchObject({ path: 'Hukuk/sözleşme.docx' });
  });

  it('a hit the host will not let drag asks for nothing', async () => {
    const w = open([folder], { hitCan: (_h: GlobalSearchHit, action: string) => action !== 'drag' });
    await search(w, 'hukuk');
    const row = w.find('.fe-cmdp__item--hit');
    await row.trigger('mouseenter');
    await row.trigger('pointerdown');
    expect(w.emitted('warm-hit')).toBeUndefined();
  });
});
