// The advanced search's narrowing travels to the SERVER (task #207, audit D6).
//
// Before 0.54 the dialog's type / date / size / folder / owner choices
// narrowed, in the browser, the first 250 name hits the server returned: "files
// over 100 MB modified this week" was answered from a window, and an MCP agent
// or the CLI could not ask it at all. advFilterParams turns the choices into
// the server's parameters (internal/nodefilter Parse); the server applies them
// before it cuts its page.
import { describe, expect, it } from 'vitest';

import { advFilterParams } from '@brftech/filex-core/src/lib/advSearch';
import { EMPTY_FILTERS, type DriveFilters } from '@brftech/filex-core/src/lib/fileFilters';

const NOW = new Date(2026, 9, 8, 15, 30).getTime(); // local time
const MB = 1024 * 1024;
const F = (over: Partial<DriveFilters>): DriveFilters => ({ ...EMPTY_FILTERS, ...over });

describe('advFilterParams', () => {
  it('nothing chosen sends nothing (and hidden only when hidden files are not shown)', () => {
    expect(advFilterParams(F({}), NOW)).toEqual({});
    expect(advFilterParams(F({}), NOW, { showHidden: true })).toEqual({});
    expect(advFilterParams(F({}), NOW, { showHidden: false })).toEqual({ hidden: 'false' });
  });

  it('type: a kind word, and Folders is the server\'s dir', () => {
    expect(advFilterParams(F({ type: 'image' }), NOW)).toEqual({ type: 'image' });
    expect(advFilterParams(F({ type: 'folder' }), NOW)).toEqual({ type: 'dir' });
  });

  it('modified: the viewer\'s own midnight, as epoch milliseconds', () => {
    const today = new Date(2026, 9, 8).getTime();
    expect(advFilterParams(F({ modified: 'today' }), NOW)).toEqual({ modified_after: String(today) });
    expect(advFilterParams(F({ modified: '7d' }), NOW)).toEqual({ modified_after: String(NOW - 7 * 86_400_000) });
    const y = advFilterParams(F({ modified: 'year' }), NOW);
    expect(y.modified_after).toBe(String(new Date(2026, 0, 1).getTime()));
    expect(y.modified_before).toBe(String(new Date(2027, 0, 1).getTime() - 1));
  });

  it('size: the bands as inclusive bytes, a folder-free bound on the server', () => {
    expect(advFilterParams(F({ size: 'lt1' }), NOW)).toEqual({ max_size: String(MB - 1) });
    expect(advFilterParams(F({ size: 'gt100' }), NOW)).toEqual({ min_size: String(100 * MB) });
    expect(advFilterParams(F({ size: 'range', sizeMin: 10, sizeMax: null }), NOW)).toEqual({ min_size: '10' });
  });

  it('folder and owner', () => {
    expect(advFilterParams(F({ pathMode: 'here', pathBase: 'docs://Reports/' }), NOW)).toEqual({ under: 'docs://Reports' });
    expect(advFilterParams(F({ pathMode: 'skip', pathBase: 'docs://Old' }), NOW)).toEqual({ not_under: 'docs://Old' });
    expect(advFilterParams(F({ pathMode: 'here', pathBase: '' }), NOW)).toEqual({});
    expect(advFilterParams(F({ people: 'me' }), NOW)).toEqual({ owner: 'me' });
    expect(advFilterParams(F({ people: 'u:42' }), NOW)).toEqual({ owner: '42' });
  });
});
