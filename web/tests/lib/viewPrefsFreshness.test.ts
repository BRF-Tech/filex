// How a folder looks: per folder, with a DEFAULT beneath it that is set on
// purpose — and all of it on the account, the newest document winning.
//
// ⚠⚠ Two reports shaped this file, one on top of the other:
//
//   2026-09-13 — "I sorted folder X by NAME in one browser and by SIZE in
//   another, and each browser kept its own." The arrangement had to live on
//   the ACCOUNT, be re-read when a tab comes back to the front, and the newer
//   document had to win whole.
//
//   2026-09-21 — "Explore içindeki değişikliklerimiz o klasör özelinde
//   olmalı; tüm klasörlerde görünüm değişikliği geçerli oluyor." Reproduced
//   the same day: grid in folder A, and folder B — never touched — opened as
//   grid, because every click also wrote a "global default" (`g`). A change
//   now belongs to its folder only, and what an untouched folder opens as is
//   a default a person sets in their settings (`p`), else the operator's for
//   the instance, else filex's own.
//
// Pinned here, because each is needed and none alone is enough:
//   1. a change in a folder writes THAT folder and nothing else;
//   2. the person's default and the instance's resolve in that order, and the
//      instance's is never saved into the person's document;
//   3. the retired `g` is not promoted to a default; the retired global
//      columns ARE kept, as the person's default columns;
//   4. freshness: a tab re-reads, the newer document wins whole, and a change
//      not yet sent outranks the server's copy.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  __flushViewPrefs,
  __resetViewPrefs,
  attachViewPrefsStore,
  columnWidth,
  defaultFolderView,
  folderPrefs,
  onViewPrefsApplied,
  personFolderDefault,
  refreshViewPrefs,
  rememberFolder,
  resolveFolderView,
  setColumnWidth,
  setInstanceFolderDefault,
  setPersonFolderDefault,
} from '@brftech/filex-core';

/** A fake account document, as the server would hold it. */
function server(initial: Record<string, unknown> | null = {}) {
  let doc: Record<string, unknown> | null = initial;
  const saves: Array<Record<string, unknown>> = [];
  return {
    saves,
    set: (next: Record<string, unknown>) => {
      doc = next;
    },
    get: () => doc,
    transport: {
      load: async () => doc,
      save: (d: unknown) => {
        doc = JSON.parse(JSON.stringify(d)) as Record<string, unknown>;
        saves.push(doc);
      },
    },
  };
}

async function settle() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

beforeEach(() => {
  __resetViewPrefs();
  localStorage.clear();
});
afterEach(() => {
  __resetViewPrefs();
  vi.useRealTimers();
});

describe('a change belongs to the folder it was made in', () => {
  it('grid in folder A leaves folder B — never touched — on the default', async () => {
    // ⚠⚠ The owner's report, as a test. What was wrong before was not
    // folder A's memory but folder B's answer: it followed the "global
    // default" every click wrote.
    const s = server({});
    attachViewPrefsStore(s.transport);
    await settle();

    rememberFolder('s/A', { v: 'grid' });
    __flushViewPrefs();

    expect(resolveFolderView('s/A').v).toBe('grid');
    expect(resolveFolderView('s/B').v).toBeUndefined(); // filex's own default
    expect(defaultFolderView()).toEqual({});
    // Nothing global went out on the wire either — no `g`, no `p`.
    expect(s.saves.at(-1)).not.toHaveProperty('g');
    expect(s.saves.at(-1)).not.toHaveProperty('p');
    expect(s.saves.at(-1)).toMatchObject({ f: { 's/A': { v: 'grid' } } });
  });

  it('a folder that changed only its view keeps FOLLOWING the default sort', async () => {
    const s = server({});
    attachViewPrefsStore(s.transport);
    await settle();
    rememberFolder('s/A', { v: 'grid' });
    setPersonFolderDefault({ k: 'size', d: 'desc' });
    // The folder never chose a sort, so a default chosen later reaches it…
    expect(resolveFolderView('s/A')).toEqual({ v: 'grid', k: 'size', d: 'desc' });
    // …and a folder that did choose one is not overwritten by it.
    rememberFolder('s/C', { k: 'name', d: 'asc' });
    expect(resolveFolderView('s/C')).toEqual({ k: 'name', d: 'asc' });
  });
});

describe('the defaults: the person’s, then the instance’s', () => {
  it('the person’s default is SENT, and mirrored locally only for the first paint', async () => {
    const s = server({});
    attachViewPrefsStore(s.transport);
    await settle();

    setPersonFolderDefault({ v: 'gallery', k: 'size', d: 'desc' });
    __flushViewPrefs();
    expect(s.saves.at(-1)).toMatchObject({ p: { v: 'gallery', k: 'size', d: 'desc' } });
    // ⚠ The mirror is written too — the document cannot arrive before the
    // first frame, and a listing that paints by name and then jumps to size
    // is worse. It mirrors the DEFAULT, never a click.
    expect(JSON.parse(localStorage.getItem('filex.list-sort') ?? '{}')).toEqual({ key: 'size', dir: 'desc' });
    expect(localStorage.getItem('brf-file-explorer:view-mode')).toBe('gallery');
  });

  it('the instance default applies under the person’s, field by field — and is never saved as theirs', async () => {
    // ⚠⚠ The palette's defect from this same release (instanceThemes.ts
    // `applyInstanceDefault`): applying the operator's answer must not RECORD
    // it as the person's choice, or the operator can never change it for them.
    const s = server({ p: { v: 'list' }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    setInstanceFolderDefault('{"v":"grid","k":"modified","d":"desc","hidden":["owner"]}');

    expect(defaultFolderView()).toEqual({ v: 'list', k: 'modified', d: 'desc' });

    setPersonFolderDefault({ k: 'name', d: 'asc' });
    __flushViewPrefs();
    const saved = s.saves.at(-1)!;
    expect(saved.p).toEqual({ v: 'list', k: 'name', d: 'asc' });
    // Nothing of the instance's travelled into the person's document.
    expect(JSON.stringify(saved)).not.toContain('modified');
    expect(JSON.stringify(saved)).not.toContain('owner');
  });

  it('choosing ONE field of your own records that field — not the instance’s others with it', async () => {
    // ⚠ The sharper form of the rule above. A person with no default of their
    // own picks only a view; the operator's sort must stay the OPERATOR's, so
    // that when the operator changes it, this person follows. Merging the
    // instance answer into the person's before saving is exactly the
    // palette's defect (it passed the previous test: that person had chosen
    // every field anyway — measured by mutation, 2026-09-21).
    const s = server({ u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    setInstanceFolderDefault({ v: 'grid', k: 'modified', d: 'desc' });
    setPersonFolderDefault({ v: 'list' });
    __flushViewPrefs();
    expect(s.saves.at(-1)!.p).toEqual({ v: 'list' });
    expect(personFolderDefault()).toEqual({ v: 'list' });
    // …and the operator's later change still reaches them.
    setInstanceFolderDefault({ v: 'grid', k: 'size', d: 'asc' });
    expect(defaultFolderView()).toEqual({ v: 'list', k: 'size', d: 'asc' });
  });

  it('clearing a field of the person’s default hands it back to the instance', async () => {
    const s = server({ p: { v: 'gallery' }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    setInstanceFolderDefault({ v: 'grid' });
    expect(defaultFolderView().v).toBe('gallery');
    setPersonFolderDefault({ v: undefined });
    expect(personFolderDefault()).toEqual({});
    expect(defaultFolderView().v).toBe('grid');
  });

  it('an unreadable instance default is no default at all', () => {
    setInstanceFolderDefault('{"v":"tiles","k":"colour"}');
    expect(defaultFolderView()).toEqual({});
    setInstanceFolderDefault('not json');
    expect(defaultFolderView()).toEqual({});
  });
});

describe('the retired keys', () => {
  it('`g` — the old "last click anywhere" — is NOT promoted to a default', async () => {
    // Promoting it would ship the leak frozen into the settings screen: the
    // person would read a "default" they never chose.
    const s = server({ on: false, g: { v: 'grid', k: 'size', d: 'desc' }, f: {}, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    __flushViewPrefs();
    expect(defaultFolderView()).toEqual({});
    // …and the document is rewritten once in the new shape, without it.
    expect(s.saves.at(-1)).not.toHaveProperty('g');
    expect(s.saves.at(-1)).toMatchObject({ on: true });
  });

  it('the old global COLUMNS are kept, as the person’s default columns', async () => {
    // A width was only ever changed by a deliberate drag; dropping it would
    // throw away work somebody did on purpose.
    const s = server({ c: { w: { size: 130 }, hidden: ['owner'] }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    __flushViewPrefs();
    expect(personFolderDefault().c).toEqual({ w: { size: 130 }, hidden: ['owner'] });
    expect(columnWidth('size')).toBe(130);
    expect(s.saves.at(-1)).not.toHaveProperty('c');
    expect(s.saves.at(-1)).toMatchObject({ p: { c: { w: { size: 130 } } } });
  });

  it('the stored document OVERWRITES what this browser had cached', async () => {
    // ⚠ localStorage is a cache of the account's answer; a cache that
    // outranks the thing it caches is the bug.
    localStorage.setItem('filex.list-sort', JSON.stringify({ key: 'name', dir: 'asc' }));
    const s = server({ p: { k: 'size', d: 'desc' }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    expect(defaultFolderView()).toEqual({ k: 'size', d: 'desc' });
    expect(JSON.parse(localStorage.getItem('filex.list-sort') ?? '{}')).toEqual({ key: 'size', dir: 'desc' });
  });
});

describe('freshness: the other browser’s newer choice', () => {
  it('is read when the tab comes back to the front, and applied', async () => {
    const s = server({ f: { 's/A': { v: 'list', t: 1 } }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    expect(folderPrefs('s/A')).toEqual({ v: 'list' });

    const seen: string[] = [];
    onViewPrefsApplied(() => seen.push(folderPrefs('s/A')?.v ?? ''));

    // …the other browser switches that folder to grid, and stamps it later.
    s.set({ f: { 's/A': { v: 'grid', t: 2 } }, u: 2_000 });
    refreshViewPrefs(true);
    await settle();
    expect(folderPrefs('s/A')).toEqual({ v: 'grid' });
    expect(seen).toEqual(['grid']);
  });

  it('an OLDER document does not undo what is on screen', async () => {
    const s = server({ u: 5_000 });
    attachViewPrefsStore(s.transport);
    await settle();

    rememberFolder('s/A', { k: 'size', d: 'desc' });
    __flushViewPrefs();
    // The save stamped the document; a refresh now sees its own copy back.
    refreshViewPrefs(true);
    await settle();
    expect(folderPrefs('s/A')).toEqual({ k: 'size', d: 'desc' });
  });

  it('a change this tab has not SENT yet outranks the server’s copy', async () => {
    // ⚠ The person is mid-gesture: their own tab must not be overwritten by
    // what they did five minutes ago somewhere else; their write goes first.
    vi.useFakeTimers();
    const s = server({ p: { c: { w: { modified: 100 } } }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();

    setColumnWidth('modified', 240); // debounced, not sent yet
    s.set({ p: { c: { w: { modified: 299 } } }, u: 9_000 }); // the other browser, newer
    refreshViewPrefs(true);
    await settle();
    expect(columnWidth('modified')).toBe(240);
    // …and the pending write went out rather than being dropped.
    expect(s.saves.at(-1)).toMatchObject({ p: { c: { w: { modified: 240 } } } });
  });

  it('the whole document wins — it is not merged field by field', async () => {
    // ⚠ Merging produces a third arrangement neither browser asked for.
    const s = server({ p: { k: 'name', c: { w: { size: 100 } } }, u: 1_000 });
    attachViewPrefsStore(s.transport);
    await settle();
    expect(columnWidth('size')).toBe(100);

    s.set({ p: { k: 'size', d: 'desc', c: { w: { modified: 180 } } }, u: 2_000 });
    refreshViewPrefs(true);
    await settle();
    expect(defaultFolderView()).toEqual({ k: 'size', d: 'desc' });
    // `size` is back to its default width because the winning document does
    // not mention it — not 100, which would be a merge.
    expect(columnWidth('size')).not.toBe(100);
    expect(columnWidth('modified')).toBe(180);
  });
});
