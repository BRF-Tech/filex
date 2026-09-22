// "More results than shown" — a search answer that was cut says so.
//
// ⚠⚠ Measured before this existed: on a server without the search index, the
// top search box read a 1,000-row window of names (400 per storage at the
// root), and a term that matched more than that came back as a plain list —
// 1,000 rows with nothing on screen saying they were not all of them. With the
// index it was 250 hits, with the same silence. The server now answers
// `truncated: true` on both endpoints (backend: handlers.Manager.vfSearch,
// handlers.Search.Search); this is the half that makes a person see it.
//
// The explorer is far too large to mount here, so, like splitOffered.test.ts,
// the wiring is pinned by reading FileExplorer.vue; the decision itself lives
// in lib/advSearch and is tested as a function.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { advSearchTruncated } from '@brftech/filex-core/src/lib/advSearch';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

describe('advSearchTruncated — the server’s word wins', () => {
  it('a server that says its answer was cut is believed, however few rows came back', () => {
    // A full window of names on the server, of which only 12 survived the
    // query's other words: those 12 are not all there is.
    expect(advSearchTruncated(12, 250, true)).toBe(true);
  });

  it('a server that says it was NOT cut is believed, even past the page size', () => {
    // The index-less fallback may return more rows than the index's 250 page.
    expect(advSearchTruncated(400, 250, false)).toBe(false);
  });

  it('an older server says nothing: a full page is a cut page, as before', () => {
    expect(advSearchTruncated(250, 250)).toBe(true);
    expect(advSearchTruncated(249, 250)).toBe(false);
    expect(advSearchTruncated(12, 250, undefined)).toBe(false);
  });
});

const EXPLORER = readFileSync(
  path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'),
  'utf8',
);

describe('FileExplorer says when a search was cut', () => {
  it('draws a status strip in the banners, over a search result only', () => {
    const banners = EXPLORER.match(/<template #banners>([\s\S]*?)<template #body>/);
    expect(banners, 'the #banners slot is gone — where do window-state strips go now?').not.toBeNull();
    const strip = banners![1].match(/<div\s+v-if="([^"]+)"[^>]*\brole="status"[^>]*>\s*\{\{\s*t\('search\.truncated'\)\s*\}\}/);
    expect(strip, 'no role="status" strip carrying search.truncated in the banners').not.toBeNull();
    const cond = strip![1];
    expect(cond).toContain('searchTruncated');
    // Not over the trash or a panel view, whatever an earlier search said.
    expect(cond).toContain('searchQuery');
    expect(cond).toContain('!navView');
    expect(cond).toContain('!trashActive');
  });

  it('every load starts un-cut, and a search takes the answer’s word for it', () => {
    const body = EXPLORER.match(/async function load\(path\?: string\) \{([\s\S]*?)\n\}\n/);
    expect(body, 'load() is gone').not.toBeNull();
    // First statement: an earlier search's verdict must not outlive it.
    expect(body![1]).toMatch(/^\s*(\/\*[\s\S]*?\*\/\s*|\/\/[^\n]*\n\s*)*searchTruncated\.value = false;/);
    expect(body![1]).toMatch(/searchTruncated\.value =[^;]*advSearchTruncated\([^;]*resp\.truncated/);
  });

  it('the advanced search count uses the same verdict', () => {
    const body = EXPLORER.match(/async function advSearchCount\([^)]*\)[^{]*\{([\s\S]*?)\n\}\n/);
    expect(body, 'advSearchCount is gone').not.toBeNull();
    expect(body![1]).toMatch(/capped:\s*truncated/);
  });

  it('says it in both languages, with no number in it', () => {
    for (const [lang, cat] of [['en', en], ['tr', tr]] as const) {
      const text = cat['search.truncated'];
      expect(text, `${lang}: search.truncated`).toBeTruthy();
      expect(text, `${lang}: a count here would be a count of the window, not of the matches`).not.toMatch(/\{/);
    }
  });
});
