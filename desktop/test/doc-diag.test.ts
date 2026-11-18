// What a document window leaves in the log (task #366): src/doc-diag.ts.
//
// Run:  node --experimental-strip-types --test desktop/test/doc-diag.test.ts
//
// The failure this exists for: on 2026-11-18 an "Open with filex" window took
// no keystrokes for half an hour and filex-desktop.log said nothing about it.
// The other half of the point is the opposite failure: a diagnostic that
// writes a credential to a file people attach to bug reports. The last
// tests below drive a whole window with filex's own token shapes in every
// URL and message, and look for any eight characters of them in the lines.

import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';

import {
  CONSOLE_LIMIT,
  DocDiagnostics,
  FrameWatch,
  MAX_PENDING,
  PendingRequests,
  RateGate,
  REQUEST_LIMIT,
  SLOW_MS,
  opaque,
  redactPath,
  redactText,
  redactUrl,
  type FrameLike,
} from '../src/doc-diag.ts';

const HOME = 'https://files.example.com';

// Shaped like the real ones (backend: share/service.go hex(16), coedit hex(32),
// archive/upload tickets base64url(32), a JWT), random enough that eight of
// their characters never occur in a log line by chance.
//
// ⚠ Made at run time, never written as literals: a token-shaped string in
// the source is what the public repository's secret scan (gitleaks: `jwt`,
// `generic-api-key`) refuses to publish, fake or not. Seeded, so every run
// tests the same strings.
function fake(seed: number, length: number, alphabet: string): string {
  let x = seed;
  let out = '';
  for (let i = 0; i < length; i++) {
    x = (Math.imul(x, 1103515245) + 12345) >>> 0;
    out += alphabet[(x >>> 16) % alphabet.length];
  }
  return out;
}
const HEX = '0123456789abcdef';
const B64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
const ALNUM = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
const SHARE = fake(11, 32, HEX);
const COEDIT = fake(23, 64, HEX);
const TICKET = fake(37, 43, B64URL);
const JWT_TOKEN = [
  Buffer.from('{"alg":"HS256"}').toString('base64url'),
  Buffer.from('{"sub":"alice","exp":1763480000}').toString('base64url'),
  fake(41, 43, B64URL),
].join('.');
const BEARER = ['fxp', fake(53, 32, ALNUM)].join('_');
const API_KEY = fake(67, 34, ALNUM);
// A secret that looks like words: only its KEY can tell it is one.
const PASSPHRASE = 'correct-horse-battery-staple';
const SECRETS = [SHARE, COEDIT, TICKET, JWT_TOKEN, BEARER, API_KEY, PASSPHRASE];

/** No eight consecutive characters of any secret, anywhere in `text`. */
function assertNoSecret(text: string): void {
  for (const s of SECRETS) {
    for (let i = 0; i + 8 <= s.length; i++) {
      const piece = s.slice(i, i + 8);
      assert.ok(!text.includes(piece), `a piece of a secret reached the log (offset ${i} of a ${s.length}-char secret)`);
    }
  }
}

// ─────────────────────────── redaction ───────────────────────────

test('a request to the account is its path; the query keeps names and masks credentials', () => {
  const url =
    `${HOME}/api/files/edit?path=/Docs/B%C3%BCt%C3%A7e%20%C3%96zeti.docx&token=${BEARER}` +
    `&key=${API_KEY}&ticket=${TICKET}&X-Amz-Signature=${COEDIT}&access_token=${JWT_TOKEN}&app=office/editor` +
    `&share_password=${PASSPHRASE}&pin=4821&code=482913`;
  const out = redactUrl(url, HOME);
  assert.equal(
    out,
    '/api/files/edit?path=/Docs/Bütçe Özeti.docx&token=*&key=*&ticket=*&X-Amz-Signature=*&access_token=*&app=office/editor' +
      '&share_password=*&pin=*&code=*',
  );
});

test("a link's secret in the path is masked: share, file request, download and upload tickets", () => {
  assert.equal(redactUrl(`${HOME}/s/${SHARE}`, HOME), '/s/*');
  assert.equal(redactUrl(`${HOME}/api/public/d/${SHARE}/upload`, HOME), '/api/public/d/*/upload');
  assert.equal(redactUrl(`${HOME}/z/${TICKET}`, HOME), '/z/*');
  assert.equal(redactUrl(`${HOME}/u/${TICKET}`, HOME), '/u/*');
  assert.equal(redactUrl(`${HOME}/api/public/s/${SHARE}/pin`, HOME), '/api/public/s/*/pin');
  // The place decides, not the shape: a link secret that reads as words.
  assert.equal(redactUrl(`${HOME}/s/${PASSPHRASE}`, HOME), '/s/*');
  assert.equal(redactUrl(`${HOME}/api/public/d/${PASSPHRASE}/captcha`, HOME), '/api/public/d/*/captcha');
});

test('a credential-looking segment is masked wherever it is, an extension does not hide it', () => {
  assert.equal(redactUrl(`${HOME}/api/coedit/${COEDIT}/ws`, HOME), '/api/coedit/*/ws');
  assert.equal(redactUrl(`${HOME}/cache/${TICKET}.json`, HOME), '/cache/*');
  assert.equal(redactUrl(`${HOME}/x/${JWT_TOKEN}`, HOME), '/x/*');
});

test('names stay readable: Turkish names, spaces, dated folders, ids, camera files', () => {
  for (const name of [
    'Bütçe Özeti.docx',
    'VPN_Request_Form - NEW.doc',
    '2026-11-18-backup-final',
    '1de4c47f-0315-4dd7-bb63-e20d82ddbb0f',
    'IMG_20261118_123456.jpg',
    'getElementsByClassName',
  ]) {
    assert.equal(opaque(name), false, name);
  }
  assert.equal(redactPath('/Docs/2026-11-18-backup-final/IMG_20261118_123456.jpg'), '/Docs/2026-11-18-backup-final/IMG_20261118_123456.jpg');
});

test('the shapes of real credentials are opaque', () => {
  for (const s of SECRETS.filter((x) => x !== PASSPHRASE)) assert.equal(opaque(s), true, `a ${s.length}-char secret`);
  // …and one that reads as words is not: its key has to give it away.
  assert.equal(opaque(PASSPHRASE), false);
});

test('no fragment, no user:password, another origin keeps its host, the websocket of the server is the server', () => {
  assert.equal(redactUrl(`https://alice:${API_KEY}@files.example.com/a#access_token=${JWT_TOKEN}`, HOME), '/a');
  assert.equal(redactUrl('https://docs.example.org/web-apps/app.js?ver=8', HOME), 'https://docs.example.org/web-apps/app.js?ver=8');
  assert.equal(redactUrl(`wss://files.example.com/api/coedit/ws?client=c1&token=${COEDIT}`, HOME), '/api/coedit/ws?client=c1&token=*');
  assert.equal(redactUrl('data:text/html;base64,PGh0bWw+', HOME), 'data:…');
  assert.equal(redactUrl('file:///C:/Users/alice/Desktop/x.doc', HOME), 'file:…');
});

test('a long path keeps its head and its file name', () => {
  const deep = '/' + Array.from({ length: 30 }, (_, i) => `folder-${i}`).join('/') + '/Rapor.docx';
  const out = redactUrl(`${HOME}${deep}`, HOME);
  assert.ok(out.length <= 160, String(out.length));
  assert.ok(out.startsWith('/folder-0/'));
  assert.ok(out.endsWith('/Rapor.docx'));
  assert.ok(out.includes('…'));
});

test('a free text: URLs, Bearer, key=value and "key":"value" are masked, the words stay', () => {
  const msg =
    `Failed to load resource: the server responded with a status of 401 (${HOME}/api/files/lease?path=/a.doc&token=${BEARER}) ` +
    `Authorization: Bearer ${BEARER} {"ticket":"${TICKET}","name":"Rapor.docx"} key=${API_KEY} ${JWT_TOKEN} Unexpected token '<' ` +
    `retrying with bearer ${PASSPHRASE}`;
  const out = redactText(msg, HOME);
  assertNoSecret(out);
  assert.match(out, /Failed to load resource: the server responded with a status of 401 \(\/api\/files\/lease\?path=\/a\.doc&token=\*\)/);
  assert.match(out, /Bearer \*/);
  assert.match(out, /"ticket":"\*","name":"Rapor\.docx"/);
  assert.match(out, /key=\*/);
  assert.match(out, /Unexpected token '<'/);
});

test('a free text is clipped', () => {
  assert.ok(redactText('x '.repeat(1000)).length <= 300);
});

// ─────────────────────────── the gate ───────────────────────────

test('the gate writes at most `limit` lines a period and counts the rest', () => {
  const g = new RateGate(3, 60_000);
  assert.deepEqual(g.pass('a', 0), { repeated: 0, dropped: 0 });
  assert.equal(g.pass('a', 1), null);
  assert.equal(g.pass('a', 2), null);
  assert.deepEqual(g.pass('b', 3), { repeated: 2, dropped: 0 });
  assert.deepEqual(g.pass('c', 4), { repeated: 0, dropped: 0 });
  assert.equal(g.pass('d', 5), null);
  assert.equal(g.pass('e', 6), null);
  // A new period: written, with what was held.
  assert.deepEqual(g.pass('f', 60_001), { repeated: 0, dropped: 2 });
  assert.equal(g.drain(), null);
});

test('a message repeating for longer than a period is written again with its count', () => {
  const g = new RateGate(10, 60_000);
  g.pass('same', 0);
  for (let t = 1000; t < 60_000; t += 1000) assert.equal(g.pass('same', t), null);
  assert.deepEqual(g.pass('same', 60_000), { repeated: 59, dropped: 0 });
  g.pass('same', 60_500);
  assert.deepEqual(g.drain(), { repeated: 1, dropped: 0 });
  assert.equal(g.drain(), null);
});

// ─────────────────────────── requests in flight ───────────────────────────

const req = (id: number, over: Partial<{ wc: number; start: number }> = {}) => ({
  id,
  wc: over.wc ?? 1,
  method: 'GET',
  path: `/r/${id}`,
  type: 'xhr',
  start: over.start ?? 0,
});

test('a redirect keeps the time the request first left at', () => {
  const p = new PendingRequests();
  p.start(req(1, { start: 0 }));
  p.start(req(1, { start: 5000 }));
  assert.equal(p.get(1)?.start, 0);
});

test('the list of requests in flight is bounded, the oldest goes', () => {
  const p = new PendingRequests(3);
  for (let i = 1; i <= 5; i++) p.start(req(i));
  assert.equal(p.size, 3);
  assert.equal(p.evicted, 2);
  assert.equal(p.get(1), undefined);
  assert.ok(p.get(5));
  assert.equal(MAX_PENDING, 512);
});

test('overdue names a waiting request once, never one that has its answer', () => {
  const p = new PendingRequests();
  p.start(req(1, { start: 0 }));
  p.start(req(2, { start: 0 }));
  p.get(2)!.responded = true;
  assert.deepEqual(p.overdue(SLOW_MS - 1, SLOW_MS).map((x) => x.id), []);
  assert.deepEqual(p.overdue(SLOW_MS, SLOW_MS).map((x) => x.id), [1]);
  assert.deepEqual(p.overdue(SLOW_MS * 3, SLOW_MS).map((x) => x.id), []);
});

test("a closed window's requests are forgotten", () => {
  const p = new PendingRequests();
  p.start(req(1, { wc: 1 }));
  p.start(req(2, { wc: 2 }));
  p.dropWindow(1);
  assert.equal(p.size, 1);
  assert.ok(p.get(2));
});

// ─────────────────────────── frames ───────────────────────────

test('a frame seen alive whose process stopped is gone at the second look, once', () => {
  const w = new FrameWatch();
  const f = { key: '7', pid: 4242, url: 'https://docs.example.org/editor' };
  assert.deepEqual(w.check([f], new Set([4242])), []);
  assert.deepEqual(w.check([f], new Set([1])), []);
  const gone = w.check([f], new Set([1]));
  assert.equal(gone.length, 1);
  assert.equal(gone[0].lastPid, 4242);
  assert.equal(gone[0].url, 'https://docs.example.org/editor');
  assert.deepEqual(w.check([f], new Set([1])), []);
});

test('the shape Electron 44 leaves: a dead frame, read once alive', () => {
  const w = new FrameWatch();
  w.check([{ key: '4', pid: 56936, url: 'http://localhost:1/frame' }], new Set([56936]));
  const dead = { key: '4', pid: 0, url: '', dead: true };
  assert.deepEqual(w.check([dead], new Set()), []);
  const gone = w.check([dead], new Set());
  assert.deepEqual(gone, [{ key: '4', pid: 0, url: 'http://localhost:1/frame', lastPid: 56936 }]);
});

test('a frame detached for a moment (a reload) is not gone', () => {
  const w = new FrameWatch();
  w.check([{ key: '4', pid: 10, url: 'a' }], new Set([10]));
  assert.deepEqual(w.check([{ key: '4', pid: 0, url: '', dead: true }], new Set()), []);
  assert.deepEqual(w.check([{ key: '4', pid: 11, url: 'b' }], new Set([11])), []);
  assert.deepEqual(w.check([{ key: '4', pid: 0, url: '', dead: true }], new Set([11])), []);
  // …and a detached frame whose process still runs is the old page unloading.
  assert.deepEqual(w.check([{ key: '4', pid: 0, url: '', dead: true }], new Set([11])), []);
});

test('a frame that moved to a new process is a navigation, not a crash', () => {
  const w = new FrameWatch();
  w.check([{ key: '7', pid: 100, url: 'a' }], new Set([100]));
  assert.deepEqual(w.check([{ key: '7', pid: 200, url: 'b' }], new Set([200])), []);
  // Starting up: the new process is not listed yet, the old one has exited.
  assert.deepEqual(w.check([{ key: '7', pid: 300, url: 'c' }], new Set()), []);
  assert.deepEqual(w.check([{ key: '7', pid: 300, url: 'c' }], new Set()), []);
});

test('a frame never seen alive is not reported, and one reloaded and crashed again is', () => {
  const w = new FrameWatch();
  assert.deepEqual(w.check([{ key: '1', pid: 5, url: 'a' }], new Set()), []);
  assert.deepEqual(w.check([{ key: '1', pid: 5, url: 'a' }], new Set()), []);
  w.check([{ key: '2', pid: 10, url: 'a' }], new Set([10]));
  w.check([{ key: '2', pid: 10, url: 'a' }], new Set());
  assert.equal(w.check([{ key: '2', pid: 10, url: 'a' }], new Set()).length, 1);
  w.check([{ key: '2', pid: 11, url: 'a' }], new Set([11]));
  w.check([{ key: '2', pid: 11, url: 'a' }], new Set());
  assert.equal(w.check([{ key: '2', pid: 11, url: 'a' }], new Set()).length, 1);
});

test('a frame removed from the page is forgotten', () => {
  const w = new FrameWatch();
  w.check([{ key: '3', pid: 10, url: 'a' }], new Set([10]));
  assert.deepEqual(w.check([], new Set()), []);
  assert.deepEqual(w.check([{ key: '3', pid: 0, url: '', dead: true }], new Set()), []);
  assert.deepEqual(w.check([{ key: '3', pid: 0, url: '', dead: true }], new Set()), []);
});

// ─────────────────────────── a whole window ───────────────────────────

class FakeContents extends EventEmitter {
  pid = 3100;
  subframes: FrameLike[] = [];
  readonly id: number;
  constructor(id: number) {
    super();
    this.id = id;
  }
  getOSProcessId(): number {
    return this.pid;
  }
  get mainFrame(): FrameLike {
    const main: FrameLike = {
      processId: 1,
      osProcessId: this.pid,
      frameTreeNodeId: 1,
      url: `${HOME}/files/edit`,
      get framesInSubtree() {
        return [main, ...fake.subframes];
      },
    };
    const fake = this;
    return main;
  }
}

function rig(over: { live?: Set<number> } = {}) {
  let now = 1_000_000;
  const lines: string[] = [];
  const entries: Array<{ tag: string; step: string; detail: any }> = [];
  let timers = 0;
  let stopped = 0;
  const diag = new DocDiagnostics({
    log: (tag, step, detail) => {
      entries.push({ tag, step, detail });
      // The line exactly as src/log.ts writes it.
      lines.push(`[${tag}] ${step}${detail === undefined ? '' : ' ' + JSON.stringify(detail)}`);
    },
    now: () => now,
    liveProcesses: () => over.live ?? new Set(),
    uptime: () => 7 * 3600,
    every: () => {
      timers++;
      return () => {
        stopped++;
      };
    },
  });
  return {
    diag,
    lines,
    entries,
    find: (step: string) => entries.filter((e) => e.step === step),
    tick: (ms: number) => {
      now += ms;
    },
    timers: () => timers,
    stopped: () => stopped,
  };
}

test('opened and closed: kind, file name, how long the process had run, how long the window was open', () => {
  const r = rig();
  const wc = new FakeContents(11);
  const label = r.diag.track(wc, { kind: 'editor', name: 'VPN_Request_Form - NEW.doc', home: HOME });
  assert.equal(label, 'doc#1');
  assert.deepEqual(r.find('opened')[0].detail, {
    win: 'doc#1',
    kind: 'editor',
    name: 'VPN_Request_Form - NEW.doc',
    uptimeMin: 420,
  });
  assert.equal(r.timers(), 1);
  r.tick(90_000);
  wc.emit('destroyed');
  assert.deepEqual(r.find('closed')[0].detail, { win: 'doc#1', ms: 90_000 });
  assert.equal(r.stopped(), 1);
  assert.equal(r.diag.watching, 0);
});

test('unresponsive then responsive says how long; closed while stuck says so', () => {
  const r = rig();
  const wc = new FakeContents(12);
  r.diag.track(wc, { kind: 'viewer', name: 'a.docx', home: HOME });
  wc.emit('did-finish-load');
  wc.emit('unresponsive');
  wc.emit('unresponsive');
  r.tick(42_000);
  wc.emit('responsive');
  assert.equal(r.find('unresponsive').length, 1);
  assert.deepEqual(r.find('unresponsive')[0].detail, { win: 'doc#1', pid: 3100 });
  assert.deepEqual(r.find('responsive')[0].detail, { win: 'doc#1', ms: 42_000 });
  wc.emit('unresponsive');
  r.tick(5000);
  wc.emit('destroyed');
  assert.equal(r.find('closed')[0].detail.unresponsiveMs, 5000);
});

test('the renderer gone: reason and exit code', () => {
  const r = rig();
  const wc = new FakeContents(13);
  r.diag.track(wc, { kind: 'viewer', name: 'a.docx', home: HOME });
  wc.emit('render-process-gone', {}, { reason: 'oom', exitCode: -536870904 });
  assert.deepEqual(r.find('renderer gone')[0].detail, { win: 'doc#1', reason: 'oom', exitCode: -536870904 });
});

test('a load that failed is written, an aborted one is not', () => {
  const r = rig();
  const wc = new FakeContents(14);
  r.diag.track(wc, { kind: 'viewer', name: 'a.docx', home: HOME });
  wc.emit('did-fail-load', {}, -3, 'ERR_ABORTED', `${HOME}/files/edit`, true);
  wc.emit('did-fail-load', {}, -105, 'ERR_NAME_NOT_RESOLVED', `https://docs.example.org/x?token=${BEARER}`, false);
  const fail = r.find('load failed');
  assert.equal(fail.length, 1);
  assert.deepEqual(fail[0].detail, {
    win: 'doc#1',
    code: -105,
    error: 'ERR_NAME_NOT_RESOLVED',
    url: 'https://docs.example.org/x?token=*',
    main: false,
  });
});

test('console: only errors, in the Electron 44 shape and the old one, rate limited with repeats counted', () => {
  const r = rig();
  const wc = new FakeContents(15);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  wc.emit('console-message', { level: 'warning', message: 'just a warning', lineNumber: 1, sourceId: '' });
  wc.emit('console-message', { level: 'error', message: 'Uncaught TypeError: x is undefined', lineNumber: 1234, sourceId: `${HOME}/office/app.js?v=1` });
  // Before Electron 35 the fields came as arguments.
  wc.emit('console-message', {}, 3, 'legacy error', 9, `${HOME}/old.js`);
  const errs = r.find('console error');
  assert.equal(errs.length, 2);
  assert.deepEqual(errs[0].detail, { win: 'doc#1', msg: 'Uncaught TypeError: x is undefined', src: '/office/app.js?v=1:1234' });
  assert.equal(errs[1].detail.msg, 'legacy error');

  // A loop: one line, then a count.
  for (let i = 0; i < 50; i++) wc.emit('console-message', { level: 'error', message: 'loop', lineNumber: 1, sourceId: '' });
  for (let i = 0; i < 30; i++) wc.emit('console-message', { level: 'error', message: `distinct ${i}`, lineNumber: 1, sourceId: '' });
  const now = r.find('console error');
  assert.equal(now.length, CONSOLE_LIMIT);
  assert.equal(now[3].detail.repeated, 49);
  wc.emit('destroyed');
  const held = r.find('held back')[0].detail;
  // Three lines were written before the distinct ones: the rest of the
  // minute's ten went to them, the others were held.
  assert.equal(held.console.dropped, 30 - (CONSOLE_LIMIT - 3));
});

test("a frame's console error names the frame", () => {
  const r = rig();
  const wc = new FakeContents(16);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  const frame = { processId: 2, osProcessId: 3200, frameTreeNodeId: 9, url: 'https://docs.example.org/web-apps/editor.html', framesInSubtree: [] };
  wc.emit('console-message', { level: 'error', message: 'boom', lineNumber: 3, sourceId: 'https://docs.example.org/sdk.js', frame });
  const e = r.find('console error')[0].detail;
  assert.equal(e.frame, 'https://docs.example.org/web-apps/editor.html');
  assert.equal(e.src, 'https://docs.example.org/sdk.js:3');
});

const details = (id: number, over: Record<string, unknown> = {}) => ({
  id,
  url: `${HOME}/api/files/append?path=/Docs/a.doc&token=${BEARER}`,
  method: 'POST',
  resourceType: 'xhr',
  webContentsId: 21,
  ...over,
});

test("requests: a failure, with how long it took; another window's are not this file's business", () => {
  const r = rig();
  const wc = new FakeContents(21);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  r.diag.requestStart(details(1));
  r.tick(1200);
  r.diag.requestError({ ...details(1), error: 'net::ERR_CONNECTION_RESET' });
  r.diag.requestStart(details(2, { webContentsId: 99 }));
  r.diag.requestError({ ...details(2, { webContentsId: 99 }), error: 'net::ERR_CONNECTION_RESET' });
  const f = r.find('request failed');
  assert.equal(f.length, 1);
  assert.deepEqual(f[0].detail, {
    win: 'doc#1',
    method: 'POST',
    path: '/api/files/append?path=/Docs/a.doc&token=*',
    type: 'xhr',
    error: 'net::ERR_CONNECTION_RESET',
    ms: 1200,
  });
  assert.equal(r.diag.inFlight, 0);
});

test('requests: waiting is written once by the sweep, the late answer again; a quick abort is not written', () => {
  const r = rig();
  const wc = new FakeContents(21);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  r.diag.requestStart(details(1));
  r.diag.requestStart(details(2));
  r.tick(300);
  r.diag.requestError({ ...details(2), error: 'net::ERR_ABORTED' });
  r.tick(SLOW_MS);
  r.diag.sweep();
  r.diag.sweep();
  const w = r.find('request waiting');
  assert.equal(w.length, 1);
  assert.equal(w[0].detail.ms, SLOW_MS + 300);
  r.tick(10_000);
  r.diag.requestResponse({ ...details(1), statusCode: 200 });
  r.diag.requestDone({ ...details(1), statusCode: 200 });
  assert.deepEqual(r.find('request slow')[0].detail, {
    win: 'doc#1',
    method: 'POST',
    path: '/api/files/append?path=/Docs/a.doc&token=*',
    type: 'xhr',
    status: 200,
    ms: SLOW_MS + 10_300,
  });
  assert.equal(r.find('request failed').length, 0);
});

test('requests: an abort after a long wait with no answer IS written (a hang the page gave up on)', () => {
  const r = rig();
  const wc = new FakeContents(21);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  r.diag.requestStart(details(1));
  r.tick(SLOW_MS + 5000);
  r.diag.requestError({ ...details(1), error: 'net::ERR_ABORTED' });
  assert.equal(r.find('request failed')[0].detail.error, 'net::ERR_ABORTED');
  // …but not a stream that had its answer (a player closed, a socket ended).
  r.diag.requestStart(details(2));
  r.diag.requestResponse({ ...details(2), statusCode: 206 });
  r.tick(SLOW_MS * 4);
  r.diag.requestError({ ...details(2), error: 'net::ERR_ABORTED' });
  assert.equal(r.find('request failed').length, 1);
});

test('requests: an error status is written (404 aside); a websocket is never "waiting"', () => {
  const r = rig();
  const wc = new FakeContents(21);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  for (const [id, status] of [[1, 500], [2, 404], [3, 401], [4, 200]] as const) {
    r.diag.requestStart(details(id));
    r.diag.requestResponse({ ...details(id), statusCode: status });
    r.diag.requestDone({ ...details(id), statusCode: status });
  }
  assert.deepEqual(r.find('request http').map((e) => e.detail.status), [500, 401]);
  r.diag.requestStart(details(5, { resourceType: 'webSocket', url: `wss://files.example.com/api/coedit/ws?token=${COEDIT}` }));
  assert.equal(r.diag.inFlight, 0);
  r.tick(SLOW_MS * 2);
  r.diag.sweep();
  assert.equal(r.find('request waiting').length, 0);
  r.diag.requestError({ ...details(5, { resourceType: 'webSocket', url: `wss://files.example.com/api/coedit/ws?token=${COEDIT}` }), error: 'net::ERR_CONNECTION_RESET' });
  assert.equal(r.find('request failed')[0].detail.path, '/api/coedit/ws?token=*');
});

test('requests: a failing loop is one line a minute with its count', () => {
  const r = rig();
  const wc = new FakeContents(21);
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  for (let i = 0; i < 200; i++) {
    r.diag.requestStart(details(i));
    r.diag.requestError({ ...details(i), error: 'net::ERR_CONNECTION_REFUSED' });
    r.tick(100);
  }
  assert.equal(r.find('request failed').length, 1);
  r.tick(60_000);
  r.diag.requestStart(details(500));
  r.diag.requestError({ ...details(500), error: 'net::ERR_CONNECTION_REFUSED' });
  const f = r.find('request failed');
  assert.equal(f.length, 2);
  assert.equal(f[1].detail.repeated, 199);
  assert.ok(REQUEST_LIMIT >= 10);
});

test('a frame of the page whose process went away is written by the sweep', () => {
  const live = new Set([3100, 4400]);
  const r = rig({ live });
  const wc = new FakeContents(31);
  wc.subframes = [{ processId: 2, osProcessId: 4400, frameTreeNodeId: 5, url: `https://docs.example.org/editor?doc=${TICKET}`, framesInSubtree: [] }];
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  r.diag.sweep();
  assert.equal(r.find('frame gone').length, 0);
  live.delete(4400);
  r.diag.sweep();
  r.diag.sweep();
  r.diag.sweep();
  const g = r.find('frame gone');
  assert.equal(g.length, 1);
  assert.deepEqual(g[0].detail, { win: 'doc#1', frame: 'https://docs.example.org/editor?doc=*', pid: 4400 });
});

test("a frame Electron can no longer read (its renderer died) is gone at the sweep's second look", () => {
  const live = new Set([3100, 4400]);
  const r = rig({ live });
  const wc = new FakeContents(32);
  let crashed = false;
  const disposed = () => {
    throw new Error('Render frame was disposed before WebFrameMain could be accessed');
  };
  wc.subframes = [
    {
      frameTreeNodeId: 6,
      get detached() {
        return crashed;
      },
      get processId() {
        return crashed ? disposed() : 2;
      },
      get osProcessId() {
        return crashed ? disposed() : 4400;
      },
      get url() {
        return crashed ? disposed() : 'https://docs.example.org/web-apps/editor.html';
      },
      get framesInSubtree() {
        return [];
      },
    } as FrameLike,
  ];
  r.diag.track(wc, { kind: 'editor', name: 'a.doc', home: HOME });
  r.diag.sweep();
  crashed = true;
  live.delete(4400);
  r.diag.sweep();
  assert.equal(r.find('frame gone').length, 0);
  r.diag.sweep();
  assert.deepEqual(r.find('frame gone').map((e) => e.detail), [
    { win: 'doc#1', frame: 'https://docs.example.org/web-apps/editor.html', pid: 4400 },
  ]);
  assert.equal(r.find('diagnostics error (written once)').length, 0);
});

test('a diagnostic that throws never throws into the window; it says so once', () => {
  const r = rig();
  const wc = new FakeContents(41);
  wc.getOSProcessId = () => {
    throw new Error('Object has been destroyed');
  };
  r.diag.track(wc, { kind: 'viewer', name: 'a.docx', home: HOME });
  assert.doesNotThrow(() => wc.emit('did-finish-load'));
  assert.doesNotThrow(() => wc.emit('did-finish-load'));
  assert.equal(r.find('diagnostics error (written once)').length, 1);
});

test('⚠ the whole trail of a window carries no piece of any credential', () => {
  const live = new Set([3100, 4400]);
  const r = rig({ live });
  const wc = new FakeContents(51);
  wc.subframes = [{ processId: 2, osProcessId: 4400, frameTreeNodeId: 5, url: `https://docs.example.org/e/${COEDIT}?jwt=${JWT_TOKEN}`, framesInSubtree: [] }];
  r.diag.track(wc, { kind: 'editor', name: 'Rapor.docx', home: HOME });
  const urls = [
    `${HOME}/api/files/append?path=/Docs/Rapor.docx&token=${BEARER}`,
    `${HOME}/s/${SHARE}`,
    `${HOME}/z/${TICKET}?download=1`,
    `${HOME}/api/coedit/${COEDIT}/join?key=${API_KEY}`,
    `${HOME}/api/office/config?jwt=${JWT_TOKEN}#t=${BEARER}`,
    `${HOME}/api/public/s/${SHARE}/pin?password=${PASSPHRASE}`,
    `${HOME}/files/edit?path=/a.doc#access_token=${PASSPHRASE}`,
    `https://alice:${API_KEY}@docs.example.org/cache/${TICKET}.docx?md5=${COEDIT}&expires=1763480000`,
    `wss://files.example.com/api/coedit/ws?client=c1&token=${COEDIT}`,
  ];
  let id = 0;
  for (const url of urls) {
    for (const type of ['xhr', 'webSocket', 'subFrame']) {
      const d = { id: ++id, url, method: 'GET', resourceType: type, webContentsId: 51 };
      r.diag.requestStart(d);
      r.tick(SLOW_MS + 1);
      r.diag.sweep();
      r.diag.requestResponse({ ...d, statusCode: 503 });
      r.diag.requestDone({ ...d, statusCode: 503 });
      r.diag.requestStart({ ...d, id: ++id });
      r.diag.requestError({ ...d, id, error: 'net::ERR_CONNECTION_RESET' });
      r.tick(61_000);
    }
    wc.emit('did-fail-load', {}, -102, 'ERR_CONNECTION_REFUSED', url, false);
    wc.emit('console-message', {
      level: 'error',
      message: `Failed to fetch ${url} with Authorization: Bearer ${BEARER} {"token":"${BEARER}","password":"${PASSPHRASE}"} ${JWT_TOKEN} ticket=${TICKET} secret=${PASSPHRASE}`,
      lineNumber: 7,
      sourceId: url,
      frame: wc.subframes[0],
    });
    r.tick(61_000);
  }
  live.delete(4400);
  r.diag.sweep();
  r.diag.sweep();
  wc.emit('unresponsive');
  wc.emit('render-process-gone', {}, { reason: 'crashed', exitCode: 1 });
  wc.emit('destroyed');
  const all = r.lines.join('\n');
  assert.ok(r.lines.length > 40, `the trail was written (${r.lines.length} lines)`);
  for (const step of ['request waiting', 'request slow', 'request http', 'request failed', 'load failed', 'console error', 'frame gone']) {
    assert.ok(r.find(step).length > 0, step);
  }
  assertNoSecret(all);
  assert.doesNotMatch(all, /alice:/);
  assert.doesNotMatch(all, /Authorization: Bearer [^*]/);
});
