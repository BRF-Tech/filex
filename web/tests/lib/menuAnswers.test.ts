// #196 - the answers a right-click menu depends on (which per-folder
// permissions are held at a path, may this person start encrypting there),
// remembered per person and storage so a menu opens on them at once
// (packages/core lib/menuAnswers).
//
// The first round made the menu wait up to 400 ms for those answers; the
// maintainers' rule since (2026-10-08) is that a menu does not wait on the
// network: the answers are asked when a folder is listed, kept in
// localStorage (memory where it is refused), refreshed when the live socket
// says they went stale, and forgotten at sign-out. These tests hold the
// store's promises: whose answers they are, how long, how many, what a
// sign-out leaves, and which ones are "hot" around a folder.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { forgetPersonalPrefs } from '@brftech/filex-core/src/lib/prefs';
import {
  MENU_ANSWERS_FRESH_MS,
  MENU_ANSWERS_MAX_AGE_MS,
  MENU_ANSWERS_MAX_ROWS,
  MENU_ANSWERS_MAX_STORAGES,
  MENU_ANSWERS_PREFIX,
  clearMenuAnswers,
  menuAnswerStore,
  menuScopeId,
  type MenuAnswerStore,
} from '@brftech/filex-core/src/lib/menuAnswers';

const SIG = { p: 'files.delete', e: 'true|approval' };
const AYSE = 'https://files.example|7|3|';
const CEM = 'https://files.example|8|3|';

let clock = 1_800_000_000_000;
const now = () => clock;
const opened: MenuAnswerStore[] = [];

function store(opts: Parameters<typeof menuAnswerStore>[0] = {}): MenuAnswerStore {
  const s = menuAnswerStore({ now, ...opts });
  opened.push(s);
  return s;
}

function keys(): string[] {
  const out: string[] = [];
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i);
    if (k && k.startsWith(MENU_ANSWERS_PREFIX)) out.push(k);
  }
  return out;
}

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  clock = 1_800_000_000_000;
});
afterEach(() => {
  for (const s of opened.splice(0)) s.dispose();
  clearMenuAnswers();
  vi.useRealTimers();
});

describe('whose answers', () => {
  it('a page of the same person reads them back at once - as answers to ask again, not as fresh ones', () => {
    const first = store();
    first.attach(AYSE, SIG);
    first.put('e:file', 'main://rapor.pdf', 'allowed');
    first.put('p', 'main://rapor.pdf', ['files.delete']);
    first.flush();

    const next = store();
    next.attach(AYSE, SIG);
    expect(next.peek('e:file', 'main://rapor.pdf')).toBe('allowed');
    expect(next.peek('p', 'main://rapor.pdf')).toEqual(['files.delete']);
    expect(next.fresh('e:file', 'main://rapor.pdf'), 'a page that was closed may have missed a change').toBe(false);
  });

  it('another person on the same browser never reads them, and the key does not spell out who', () => {
    const ayse = store();
    ayse.attach(AYSE, SIG);
    ayse.put('e:file', 'main://rapor.pdf', 'allowed');
    ayse.flush();

    const cem = store();
    cem.attach(CEM, SIG);
    expect(cem.peek('e:file', 'main://rapor.pdf')).toBeUndefined();
    expect(keys().length).toBeGreaterThan(0);
    for (const k of keys()) expect(k).not.toMatch(/files\.example|\|7\|/);
    expect(menuScopeId(AYSE)).toBe(menuScopeId(AYSE));
    expect(menuScopeId(AYSE)).not.toBe(menuScopeId(CEM));
    expect(menuScopeId(AYSE)).toMatch(/^[0-9a-f]{16}$/);
  });

  it('one store that is handed a different person forgets the first one', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:folder', 'main://Proje', 'request');
    s.attach(CEM, SIG);
    expect(s.peek('e:folder', 'main://Proje')).toBeUndefined();
  });

  it('what a page asked before it knew who it was is kept, and written once it knows', () => {
    const s = store();
    s.put('e:folder', 'main://Proje', 'request');
    expect(keys()).toEqual([]);
    s.attach(AYSE, SIG);
    expect(s.peek('e:folder', 'main://Proje')).toBe('request');
    s.flush();
    const next = store();
    next.attach(AYSE, SIG);
    expect(next.peek('e:folder', 'main://Proje')).toBe('request');
  });

  it('answers asked under another rule set are dropped, one family at a time', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('p', 'main://a', ['files.delete']);
    s.put('e:folder', 'main://a', 'allowed');
    s.flush();

    const roleChanged = store();
    roleChanged.attach(AYSE, { p: 'files.delete,files.share', e: SIG.e });
    expect(roleChanged.peek('p', 'main://a'), 'the permission list changed').toBeUndefined();
    expect(roleChanged.peek('e:folder', 'main://a'), 'the encryption policy did not').toBe('allowed');

    roleChanged.sign('e', 'true|permitted');
    expect(roleChanged.peek('e:folder', 'main://a')).toBeUndefined();
  });
});

describe('fresh and stale', () => {
  it('an answer of this era is fresh; a new era leaves it shown and asks it again', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a.txt', 'denied');
    expect(s.fresh('e:file', 'main://a.txt')).toBe(true);
    const rev = s.rev.value;
    s.stale();
    expect(s.fresh('e:file', 'main://a.txt')).toBe(false);
    expect(s.peek('e:file', 'main://a.txt'), 'still shown while it is asked again').toBe('denied');
    expect(s.rev.value, 'a computed that read it runs again').toBeGreaterThan(rev);
  });

  it('an answer also ages out', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('p', 'main://a', []);
    clock += MENU_ANSWERS_FRESH_MS - 1;
    expect(s.fresh('p', 'main://a')).toBe(true);
    clock += 1;
    expect(s.fresh('p', 'main://a')).toBe(false);
  });

  it('the fallback of a failed question is shown for the page and never remembered', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a.txt', 'allowed', false);
    expect(s.fresh('e:file', 'main://a.txt'), 'not asked again on every render').toBe(true);
    s.flush();
    const next = store();
    next.attach(AYSE, SIG);
    expect(next.peek('e:file', 'main://a.txt')).toBeUndefined();
  });

  it('a path with no storage in it is answered in memory, so it is not asked on every render', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('p', '', ['files.create']);
    expect(s.fresh('p', '')).toBe(true);
    s.flush();
    expect(keys().some((k) => k.endsWith('|'))).toBe(false);
  });
});

describe('bounded', () => {
  it('a storage keeps the most recently used rows', () => {
    const s = store();
    s.attach(AYSE, SIG);
    for (let i = 0; i < MENU_ANSWERS_MAX_ROWS + 10; i++) s.put('e:file', `main://f${i}.txt`, 'allowed');
    expect(s.peek('e:file', 'main://f0.txt')).toBeUndefined();
    expect(s.peek('e:file', 'main://f9.txt')).toBeUndefined();
    expect(s.peek('e:file', 'main://f10.txt')).toBe('allowed');
    expect(s.peek('e:file', `main://f${MENU_ANSWERS_MAX_ROWS + 9}.txt`)).toBe('allowed');
  });

  it('a person keeps the most recently used storages', () => {
    const s = store();
    s.attach(AYSE, SIG);
    for (let i = 0; i < MENU_ANSWERS_MAX_STORAGES + 2; i++) {
      clock += 1000;
      s.put('e:file', `depo${i}://a.txt`, 'allowed');
      s.flush();
    }
    const next = store();
    next.attach(AYSE, SIG);
    expect(next.peek('e:file', 'depo0://a.txt')).toBeUndefined();
    expect(next.peek('e:file', 'depo1://a.txt')).toBeUndefined();
    expect(next.peek('e:file', `depo${MENU_ANSWERS_MAX_STORAGES + 1}://a.txt`)).toBe('allowed');
    // One key per storage kept, and one index.
    expect(keys()).toHaveLength(MENU_ANSWERS_MAX_STORAGES + 1);
  });

  it('a storage too big for its key keeps its newest rows', () => {
    const s = store();
    s.attach(AYSE, SIG);
    const long = 'x'.repeat(240);
    for (let i = 0; i < MENU_ANSWERS_MAX_ROWS; i++) s.put('e:file', `main://${long}/${i}.txt`, 'allowed');
    s.flush();
    const next = store();
    next.attach(AYSE, SIG);
    expect(next.peek('e:file', `main://${long}/${MENU_ANSWERS_MAX_ROWS - 1}.txt`)).toBe('allowed');
    expect(next.peek('e:file', `main://${long}/0.txt`)).toBeUndefined();
  });

  it('nothing older than the age limit is read back, and its key is removed', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a.txt', 'allowed');
    s.flush();
    clock += MENU_ANSWERS_MAX_AGE_MS + 1;
    const next = store();
    next.attach(AYSE, SIG);
    expect(next.peek('e:file', 'main://a.txt')).toBeUndefined();
    expect(keys().filter((k) => k.includes('main'))).toEqual([]);
  });

  it('a version it does not know, or a value in the wrong shape, is not read', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://keep.txt', 'allowed');
    s.flush();
    const key = keys().find((k) => k.endsWith('|main'))!;
    const doc = JSON.parse(localStorage.getItem(key)!);
    doc.r.push(['e:file|main://odd.txt', 'perhaps', clock], ['p|main://odd.txt', 'files.delete', clock], ['x:y|main://odd.txt', 'allowed', clock]);
    localStorage.setItem(key, JSON.stringify(doc));
    const shapes = store();
    shapes.attach(AYSE, SIG);
    expect(shapes.peek('e:file', 'main://keep.txt')).toBe('allowed');
    expect(shapes.peek('e:file', 'main://odd.txt')).toBeUndefined();
    expect(shapes.peek('p', 'main://odd.txt')).toBeUndefined();

    localStorage.setItem(key, JSON.stringify({ ...doc, v: 2 }));
    const newer = store();
    newer.attach(AYSE, SIG);
    expect(newer.peek('e:file', 'main://keep.txt')).toBeUndefined();
  });
});

describe('where localStorage is not there', () => {
  it('nothing to write to (a private window, blocked site data): the answers live in memory', () => {
    const s = store({ storage: () => null });
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a.txt', 'request');
    s.flush();
    expect(s.peek('e:file', 'main://a.txt')).toBe('request');
    expect(s.fresh('e:file', 'main://a.txt')).toBe(true);
    expect(keys()).toEqual([]);
  });

  it('a storage that refuses every write (a full quota) costs nothing but the memory', () => {
    const refusing = {
      length: 0,
      key: () => null,
      getItem: () => null,
      setItem: () => {
        throw new DOMException('full', 'QuotaExceededError');
      },
      removeItem: () => undefined,
      clear: () => undefined,
    } as unknown as Storage;
    const s = store({ storage: () => refusing });
    expect(() => {
      s.attach(AYSE, SIG);
      for (let i = 0; i < 50; i++) s.put('p', `main://d${i}`, ['files.delete']);
      s.flush();
    }).not.toThrow();
    expect(s.peek('p', 'main://d49')).toEqual(['files.delete']);
  });
});

describe('sign-out', () => {
  it('forgets every key and what every open explorer holds, and writes nothing after', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a.txt', 'allowed');
    s.flush();
    const other = store();
    other.attach(CEM, SIG);
    other.put('e:file', 'main://b.txt', 'denied');
    other.flush();
    expect(keys().length).toBeGreaterThan(0);

    forgetPersonalPrefs();

    expect(keys()).toEqual([]);
    expect(s.peek('e:file', 'main://a.txt')).toBeUndefined();
    expect(other.peek('e:file', 'main://b.txt')).toBeUndefined();
    s.put('e:file', 'main://c.txt', 'allowed');
    vi.advanceTimersByTime(5_000);
    s.flush();
    expect(keys(), 'a store whose person signed out writes nothing until it is told who is next').toEqual([]);
  });

  it('a write that was waiting when the person signed out does not bring the answers back', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a.txt', 'allowed');
    clearMenuAnswers();
    vi.advanceTimersByTime(5_000);
    expect(keys()).toEqual([]);
  });
});

describe('the hot tier around a folder', () => {
  it('the root and its first level, the parent level, the folder, its level and the level below - nothing farther', () => {
    const s = store();
    s.attach(AYSE, SIG);
    const hot = [
      ['e:new_folder', 'main://'],
      ['e:file', 'main://top.txt'],
      ['e:folder', 'main://a'],
      ['e:folder', 'main://a/b'],
      ['e:file', 'main://a/b/sib.txt'],
      ['p', 'main://a/b/c'],
      ['e:file', 'main://a/b/c/x.txt'],
      ['e:folder', 'main://a/b/c/d'],
      ['e:file', 'main://a/b/c/d/y.txt'],
    ] as const;
    const cold = [
      ['e:file', 'main://a/b/c/d/e/z.txt'],
      ['e:file', 'main://a/other/q.txt'],
      ['e:file', 'main://elsewhere/deep/w.txt'],
      ['e:file', 'other://a/b/c/x.txt'],
    ] as const;
    for (const [q, p] of [...hot, ...cold]) s.put(q, p, 'allowed');
    const got = s.around('main://a/b/c').map((x) => `${x.q} ${x.path}`).sort();
    expect(got).toEqual(hot.map(([q, p]) => `${q} ${p}`).sort());
  });

  it('at a storage root: the root, its first level and the level below it', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:new_folder', 'main://', 'allowed');
    s.put('e:folder', 'main://a', 'allowed');
    s.put('e:file', 'main://a/x.txt', 'allowed');
    s.put('e:file', 'main://a/b/y.txt', 'allowed');
    expect(s.around('main://').map((x) => x.path).sort()).toEqual(['main://', 'main://a', 'main://a/x.txt']);
  });

  it('the remembered rows of a page that was closed are hot too: they are what a new page asks first', () => {
    const s = store();
    s.attach(AYSE, SIG);
    s.put('e:file', 'main://a/x.txt', 'allowed');
    s.flush();
    const next = store();
    next.attach(AYSE, SIG);
    expect(next.around('main://a')).toEqual([{ q: 'e:file', path: 'main://a/x.txt' }]);
  });
});
