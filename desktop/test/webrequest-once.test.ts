// Electron keeps ONE listener per webRequest event and session: a second
// `webRequest.onBeforeSendHeaders(...)` anywhere replaces the first without a
// word. The first one is the credential injector (main.ts wireWebRequest);
// losing it sends every image preview, media player and download back to
// 401. The document windows' request watch (src/doc-diag.ts, #366) lives on
// four more events and would go just as quietly. So every webRequest
// listener of the app is registered in wireWebRequest and nowhere else.
//
// Run:  node --experimental-strip-types --test desktop/test/webrequest-once.test.ts

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const SRC = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src');

const EVENTS = [
  'onBeforeRequest',
  'onBeforeSendHeaders',
  'onSendHeaders',
  'onHeadersReceived',
  'onResponseStarted',
  'onBeforeRedirect',
  'onCompleted',
  'onErrorOccurred',
];

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return sources(p);
    return /\.(ts|cts|mts)$/.test(e.name) ? [p] : [];
  });
}

/** Comment lines blanked, line numbers kept. Not a parser on purpose: a
 *  block-comment stripper reads the `${o}/*` filter in wireWebRequest as the
 *  start of a comment and swallows the function. */
function stripComments(code: string): string {
  return code
    .split('\n')
    .map((l) => (/^\s*(\/\/|\/\*|\*)/.test(l) ? '' : l))
    .join('\n');
}

/** `<event>(` calls, with where they are. */
function registrations(): Map<string, string[]> {
  const found = new Map<string, string[]>(EVENTS.map((e) => [e, []]));
  for (const file of sources(SRC)) {
    stripComments(readFileSync(file, 'utf8'))
      .split('\n')
      .forEach((line, i) => {
        for (const ev of EVENTS) {
          if (new RegExp(`\\.${ev}\\s*\\(`).test(line)) found.get(ev)!.push(`${path.relative(SRC, file)}:${i + 1}`);
        }
      });
  }
  return found;
}

test('no webRequest event is registered twice', () => {
  for (const [ev, at] of registrations()) {
    assert.ok(at.length <= 1, `${ev} is registered ${at.length} times - the later one replaces the earlier:\n${at.join('\n')}`);
  }
});

test('the injector and the document watch are registered, all in wireWebRequest', () => {
  const main = stripComments(readFileSync(path.join(SRC, 'main.ts'), 'utf8'));
  const body = /function wireWebRequest\(\): void \{([\s\S]*?)\n\}/.exec(main)?.[1] ?? '';
  assert.ok(body, 'main.ts has wireWebRequest');
  for (const ev of ['onBeforeSendHeaders', 'onBeforeRequest', 'onResponseStarted', 'onCompleted', 'onErrorOccurred']) {
    assert.match(body, new RegExp(`\\.${ev}\\(\\{ urls \\}`), `${ev} in wireWebRequest`);
  }
  // onBeforeRequest holds the request until its callback runs: the call is in
  // a finally, so a throwing watcher cannot hang a request.
  assert.match(body, /onBeforeRequest\(\{ urls \}, \(details, done\) => \{\s*try \{[\s\S]*?\} finally \{\s*done\(\{\}\);/);
  for (const [ev, at] of registrations()) {
    for (const where of at) {
      const line = Number(where.split(':').pop());
      assert.ok(where.startsWith('main.ts:'), `${ev} outside main.ts: ${where}`);
      const start = main.split('\n').findIndex((l) => l.startsWith('function wireWebRequest('));
      const end = start + body.split('\n').length;
      assert.ok(line > start && line <= end + 1, `${ev} outside wireWebRequest: ${where}`);
    }
  }
});

test('every document window is watched', () => {
  const main = stripComments(readFileSync(path.join(SRC, 'main.ts'), 'utf8'));
  const make = /function makeDocumentWindow\([\s\S]*?\n\}/.exec(main)?.[0] ?? '';
  assert.match(make, /docDiag\.track\(win\.webContents, \{ kind, name: title, home: originOf\(acc\.serverUrl\) \}\)/);
  // And each of its callers says which kind it is.
  const calls = [...main.matchAll(/makeDocumentWindow\(acc, [^\n]*/g)].map((m) => m[0]);
  assert.equal(calls.length, 3);
  for (const c of calls) assert.match(c, /'(viewer|editor|store)'/, c);
});
