// ⌘K across accounts — the two server calls the main process makes for a
// hit that belongs to an account the window is NOT showing (task #47).
//
// Run:  node --experimental-strip-types --test desktop/test/remote-search.test.ts
//
// The explorer is mounted for one account and holds no credential for any
// other, so searching, downloading and dragging another account's file go
// through the main process. These are the addresses it builds; they must be
// the same ones the explorer itself uses for its own account
// (`/api/files/search`, `/api/files/manager?action=download`), or one account
// would search and download differently from the next.

import assert from 'node:assert/strict';
import test from 'node:test';

import { remoteDownloadUrl, remoteSearchUrl, searchResults } from '../src/remote-search.ts';

test('the search address is the explorer\'s own: /api/files/search, q + limit + scope', () => {
  const u = new URL(remoteSearchUrl('https://fm.example.com/', 'ceza şartı', { limit: 8, scope: 'all' }));
  assert.equal(u.origin + u.pathname, 'https://fm.example.com/api/files/search');
  assert.equal(u.searchParams.get('q'), 'ceza şartı');
  assert.equal(u.searchParams.get('limit'), '8');
  assert.equal(u.searchParams.get('scope'), 'all');
});

test('a server installed under a sub-path keeps it', () => {
  const u = new URL(remoteSearchUrl('https://example.com/filex', 'x', { limit: 8, scope: 'all' }));
  assert.equal(u.pathname, '/filex/api/files/search');
});

test('limit and scope are bounded to what the palette may ask', () => {
  const u = new URL(remoteSearchUrl('https://fm.example.com', 'x', { limit: 5000, scope: 'everything' }));
  assert.equal(u.searchParams.get('limit'), '50');
  assert.equal(u.searchParams.get('scope'), 'all');
});

test('the download address is the listing\'s: manager?action=download&path=<name://rel>', () => {
  const u = new URL(remoteDownloadUrl('https://fm.example.com', 'docs://Hukuk/sözleşme #2.docx'));
  assert.equal(u.origin + u.pathname, 'https://fm.example.com/api/files/manager');
  assert.equal(u.searchParams.get('action'), 'download');
  assert.equal(u.searchParams.get('path'), 'docs://Hukuk/sözleşme #2.docx');
});

test('a download address is refused for anything that is not name://rel', () => {
  assert.throws(() => remoteDownloadUrl('https://fm.example.com', '../../etc/passwd'));
  assert.throws(() => remoteDownloadUrl('https://fm.example.com', ''));
});

test('results: the array the server sent, or nothing', () => {
  assert.deepEqual(searchResults({ results: [{ name: 'a.txt' }] }), [{ name: 'a.txt' }]);
  assert.deepEqual(searchResults({ results: null }), []);
  assert.deepEqual(searchResults('not json'), []);
  assert.deepEqual(searchResults({ results: [{ name: 'a' }, 'x', null] }), [{ name: 'a' }]);
});
