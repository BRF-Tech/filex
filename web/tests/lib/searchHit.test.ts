// A search hit is not a listing row — task #47.
//
// `/api/files/search` answers with raw node rows: an in-storage relative
// `path`, a numeric `storage_id` and (since the multi-storage fix) the drive's
// NAME. Everything the palette can do with a hit — open it, download it, drag
// it out — needs the wire address the listing uses (`name://rel`). Until this
// module it was built inline twice (FileExplorer's `openSearchHit` and
// `advHitToNode`); download and drag-out need the same address, and four
// copies of one rule is how "open works, download 404s" happens.
//
// The other half is the desktop app's rail: several accounts at once. Their
// hits come back tagged with the account they belong to, and the palette
// draws one group per account, own account first, each with its own cap.

import { describe, expect, it } from 'vitest';

import {
  groupHitsByAccount,
  hitItem,
  hitStorageName,
  hitToNode,
} from '@brftech/filex-core/src/lib/searchHit';
import type { GlobalSearchHit } from '@brftech/filex-core/src/composables/useFileApi';

const where = { configured: ['docs', 'media'], current: 'docs' };

describe('hitStorageName — which drive a hit lives on', () => {
  it('trusts the name the server sent', () => {
    expect(hitStorageName({ storage: 'media', path: 'a.txt' }, where)).toBe('media');
  });
  it('falls back to storage_name (older servers), then the only drive, then the open one', () => {
    expect(hitStorageName({ storage_name: 'media', path: 'a.txt' }, where)).toBe('media');
    expect(hitStorageName({ path: 'a.txt' }, { configured: ['only'], current: 'docs' })).toBe('only');
    expect(hitStorageName({ path: 'a.txt' }, where)).toBe('docs');
  });
});

describe('hitItem — the address the download and the drag-out hand over', () => {
  it('a file hit becomes one file item, named like the file, at name://rel', () => {
    expect(hitItem({ storage: 'docs', path: '/Hukuk/sözleşme.docx', name: 'sözleşme.docx', type: 'file' }, where))
      .toEqual({ path: 'docs://Hukuk/sözleşme.docx', basename: 'sözleşme.docx', type: 'file' });
  });
  it('a folder hit becomes a dir item; a missing name comes from the path', () => {
    expect(hitItem({ storage: 'docs', path: 'Projeler/2026', type: 'dir' }, where))
      .toEqual({ path: 'docs://Projeler/2026', basename: '2026', type: 'dir' });
  });
  it('a hit with no drive of its own lands on the fallback drive', () => {
    expect(hitItem({ path: 'a.txt', name: 'a.txt' }, where)?.path).toBe('docs://a.txt');
  });
  it('an unaddressable hit is null — no path, or no drive to put it on', () => {
    expect(hitItem({ storage: 'docs', path: '' }, where)).toBeNull();
    expect(hitItem({ path: 'a.txt' }, { configured: [], current: '' })).toBeNull();
  });
  it('is the same address the advanced search draws the row at (hitToNode)', () => {
    const h: GlobalSearchHit = { storage: 'media', path: 'Film/klip.mp4', name: 'klip.mp4', type: 'file' };
    expect(hitItem(h, where)?.path).toBe(hitToNode(h, 'docs').path);
  });
});

describe('groupHitsByAccount — one group per signed-in account', () => {
  const a = { id: 'a', label: 'fm.example.com' };
  const b = { id: 'b', label: 'files.other.org' };
  const hit = (name: string, account?: typeof a): GlobalSearchHit => ({ name, path: name, storage: 'docs', account });

  it('hits without an account are one unlabelled group (the single-account case)', () => {
    const groups = groupHitsByAccount([hit('1'), hit('2')], 8);
    expect(groups).toHaveLength(1);
    expect(groups[0].account).toBeUndefined();
    expect(groups[0].hits.map((h) => h.name)).toEqual(['1', '2']);
  });

  it('keeps the order accounts first appear in, and each account keeps its own order', () => {
    const groups = groupHitsByAccount([hit('a1', a), hit('b1', b), hit('a2', a), hit('b2', b)], 8);
    expect(groups.map((g) => g.account?.id)).toEqual(['a', 'b']);
    expect(groups[0].hits.map((h) => h.name)).toEqual(['a1', 'a2']);
    expect(groups[1].hits.map((h) => h.name)).toEqual(['b1', 'b2']);
  });

  it('caps each account separately — a busy account cannot push another out', () => {
    const many = Array.from({ length: 12 }, (_, i) => hit(`a${i}`, a));
    const groups = groupHitsByAccount([...many, hit('b1', b)], 8);
    expect(groups[0].hits).toHaveLength(8);
    expect(groups[1].hits.map((h) => h.name)).toEqual(['b1']);
  });
});
