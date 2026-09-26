// Every request the desktop main process makes goes to an address built by
// server-url.ts. filex can be served under a sub-path (FILEX_BASE_PATH,
// https://example.com/filex/), and `new URL('/api/…', serverUrl)` — which six
// places used — drops that path: the app then asked the host's root.
//
// Run:  node --experimental-strip-types --test desktop/test/server-url.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import { isServerApiUrl, serverBasePath, serverUrl } from '../src/server-url.ts';

test('a path joins onto the server URL with the server\'s own path kept', () => {
  assert.equal(serverUrl('https://example.com/filex', '/api/files/manager').href, 'https://example.com/filex/api/files/manager');
  assert.equal(serverUrl('https://example.com/filex/', '/api/files/manager').href, 'https://example.com/filex/api/files/manager');
  assert.equal(serverUrl('https://example.com/apps/filex', '/s/t0k').href, 'https://example.com/apps/filex/s/t0k');
  assert.equal(serverUrl('https://example.com/filex', '/admin/').href, 'https://example.com/filex/admin/');
  // the precise failure this replaced
  assert.equal(new URL('/api/files/manager', 'https://example.com/filex').href, 'https://example.com/api/files/manager');
});

test('at the root it is the address it always was', () => {
  assert.equal(serverUrl('https://fm.example.com', '/api/files/manager').href, 'https://fm.example.com/api/files/manager');
  assert.equal(serverUrl('https://fm.example.com/', '/files/edit').href, 'https://fm.example.com/files/edit');
  const u = serverUrl('http://localhost:5212', '/api/notifications');
  u.searchParams.set('unread', 'true');
  assert.equal(u.href, 'http://localhost:5212/api/notifications?unread=true');
});

test('the API surface is recognised under the server path only', () => {
  assert.equal(isServerApiUrl('https://example.com/filex/api/files/manager?action=download', 'https://example.com/filex'), true);
  assert.equal(isServerApiUrl('https://example.com/api/files/manager', 'https://example.com/filex'), false);
  assert.equal(isServerApiUrl('https://example.com/filex/files/edit?path=a', 'https://example.com/filex'), false);
  assert.equal(isServerApiUrl('https://fm.example.com/api/files/manager', 'https://fm.example.com'), true);
  assert.equal(isServerApiUrl('https://other.example.com/api/x', 'https://fm.example.com'), false);
  assert.equal(isServerApiUrl('not a url', 'https://fm.example.com'), false);
});

test('a pasted address keeps the server path and drops the page', () => {
  assert.equal(serverBasePath('/'), '');
  assert.equal(serverBasePath(''), '');
  assert.equal(serverBasePath('/admin/'), '');
  assert.equal(serverBasePath('/admin/login'), '');
  assert.equal(serverBasePath('/drive/explore'), '');
  assert.equal(serverBasePath('/s/t0k'), '');
  assert.equal(serverBasePath('/filex'), '/filex');
  assert.equal(serverBasePath('/filex/'), '/filex');
  assert.equal(serverBasePath('/filex/admin/login'), '/filex');
  assert.equal(serverBasePath('/filex/drive/explore'), '/filex');
  assert.equal(serverBasePath('/apps/filex/files/edit'), '/apps/filex');
  // a page whose own path contains a door name later on is still cut at the first
  assert.equal(serverBasePath('/filex/admin/files'), '/filex');
});

// The gate: a root-relative path resolved against an account's server URL is
// the bug this module exists for, so none may come back. (Comments are
// ignored; the helper's own doc names the pattern on purpose.)
test('no module resolves a root-relative path against a server URL', async () => {
  const { readdirSync, readFileSync } = await import('node:fs');
  const path = await import('node:path');
  const { fileURLToPath } = await import('node:url');
  const src = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src');
  const bad = /new URL\(\s*[`'"]\/[^`'"]*[`'"]\s*,\s*[^)]*server/i;
  const offenders: string[] = [];
  for (const name of readdirSync(src)) {
    if (!/\.(ts|cts|mts)$/.test(name)) continue;
    const code = readFileSync(path.join(src, name), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ''))
      .replace(/(^|[^:])\/\/.*$/gm, '$1');
    code.split('\n').forEach((line, i) => {
      if (bad.test(line)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, [], `use serverUrl() from server-url.ts:\n${offenders.join('\n')}`);
});

test('the sign-in field keeps a sub-path server\'s address and drops the page', async () => {
  const { normalizeServerUrl } = await import('../src/server-url.ts');
  assert.equal(normalizeServerUrl('fm.example.com'), 'https://fm.example.com');
  assert.equal(normalizeServerUrl('https://fm.example.com/admin/'), 'https://fm.example.com');
  assert.equal(normalizeServerUrl('https://example.com/filex'), 'https://example.com/filex');
  assert.equal(normalizeServerUrl('https://example.com/filex/drive/explore#main://x'), 'https://example.com/filex');
  assert.equal(normalizeServerUrl('example.com/filex/admin/login?desktop_state=x'), 'https://example.com/filex');
  assert.throws(() => normalizeServerUrl('http://example.com/filex'), /https/);
  assert.equal(normalizeServerUrl('http://localhost:5212/filex/'), 'http://localhost:5212/filex');
});
