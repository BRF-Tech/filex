// End-to-end measurement of what a document window leaves in the log (#366).
//
// On 2026-11-18 an "Open with filex" window took no keystrokes for half an
// hour and filex-desktop.log said nothing about it. src/doc-diag.ts now
// writes [docwin] / [docreq] lines; its unit test drives it with fake windows.
// This drives the REAL ones, so a change in what Electron emits (the console
// event's shape changed in 35, the frames API in 30) cannot leave the log
// silent again while the unit test stays green.
//
// What it does, for real, in a viewer window of a signed-in account:
//   • a request whose connection is torn down  → [docreq] request failed
//   • a request answered 503                   → [docreq] request http
//   • a request held past FILEX_DIAG_SLOW_MS    → [docreq] request waiting,
//                                                 then request slow
//   • a burst of console errors                → [docwin] console error,
//                                                 rate limited, repeats counted
//   • a cross-site <iframe> whose renderer is killed → [docwin] frame gone
//   • the window's unresponsive / responsive events → how long it was stuck
//     (emitted on its real webContents: see the note where it is done)
//   • the page's renderer crashed              → [docwin] renderer gone
//   • the window closed                        → [docwin] held back, closed
// …and that the credential injector still works next to the new listeners
// (the page's own `fetch('/api/auth/me')` answers 200), and that no piece of
// the secrets put in those URLs and messages reaches the log.
//
// It starts everything it needs: the server binary you name, on a
// throwaway data dir, behind a small proxy of its own (the proxy is what tears
// down, holds and fails requests on cue), and the app on a throwaway profile
// with its windows kept off screen (--keep-windows-hidden).
//
// ⚠ No Playwright MCP and no OS input: Playwright talks to the test's own
// Electron, the operator keeps their mouse and keyboard, and no window of
// this run comes on screen.
//
//   FILEX_BIN=bin/filex.exe node desktop/scripts/docwin-e2e.mjs

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const BIN = process.env.FILEX_BIN;
if (!BIN || !fs.existsSync(BIN)) {
  console.error('set FILEX_BIN to a filex server binary (bin/filex.exe, built with `go build ./backend/cmd/filex`)');
  process.exit(2);
}

// Synthetic, made for this run; never a real credential. One looks like a
// token, one like words: the second is caught only by where it stands. The
// first is drawn at run time: a token-shaped literal in the source is what
// the public repository's secret scan (gitleaks) refuses, fake or not.
const TOKEN_LIKE = randomBytes(24).toString('base64url');
const WORD_LIKE = 'violet-otter-lantern-quartz';
const SLOW_MS = 2000;

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.unref();
    s.on('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });
}

const upstreamPort = await freePort();
const proxyPort = await freePort();
const PUBLIC = `http://127.0.0.1:${proxyPort}`;
// Another SITE for the iframe (with site isolation it gets a renderer of its
// own); the same proxy answers it.
const FRAME_URL = `http://localhost:${proxyPort}/__diag/frame`;

// ─── the server ───
const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-docwin-'));
const serverLog = fs.openSync(path.join(dataDir, 'server.log'), 'a');
const PASSWORD = `e2e-${Math.random().toString(36).slice(2)}-pass`;
const server = spawn(BIN, ['serve'], {
  env: {
    ...process.env,
    FILEX_DATA_DIR: dataDir,
    FILEX_LISTEN: `127.0.0.1:${upstreamPort}`,
    FILEX_PUBLIC_URL: PUBLIC,
    FILEX_ADMIN_EMAIL: 'admin@local',
    FILEX_ADMIN_PASSWORD: PASSWORD,
    FILEX_SECRET_KEY: 'docwin-e2e-secret-key-not-for-production',
  },
  stdio: ['ignore', serverLog, serverLog],
  // A console program: no window of its own on the operator's screen.
  windowsHide: true,
});

// ─── the proxy: the server, plus four answers on cue ───
const held = new Set();
function onRequest(req, res) {
  const u = new URL(req.url, PUBLIC);
  if (u.pathname === '/__diag/reset') {
    req.socket.destroy();
    return;
  }
  if (u.pathname === '/__diag/hang') {
    const t = setTimeout(() => {
      held.delete(t);
      res.writeHead(200, { 'content-type': 'text/plain' });
      res.end('late');
    }, Number(u.searchParams.get('ms') ?? 9000));
    held.add(t);
    return;
  }
  if (u.pathname === '/__diag/status') {
    res.writeHead(Number(u.searchParams.get('answer') ?? 500), { 'content-type': 'text/plain' });
    res.end('on cue');
    return;
  }
  if (u.pathname === '/__diag/frame') {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end('<!doctype html><meta charset="utf-8"><title>frame</title><p>a frame of another site</p>');
    return;
  }
  const up = http.request(
    { host: '127.0.0.1', port: upstreamPort, method: req.method, path: req.url, headers: req.headers },
    (ur) => {
      // The page gets an <iframe> of another site below: its CSP would
      // (rightly) refuse it. This proxy is a test rig, not a server.
      const headers = { ...ur.headers };
      delete headers['content-security-policy'];
      delete headers['x-frame-options'];
      res.writeHead(ur.statusCode ?? 502, headers);
      ur.pipe(res);
    },
  );
  up.on('error', () => res.destroy());
  req.pipe(up);
}
function onUpgrade(req, socket, head) {
  const up = net.connect(upstreamPort, '127.0.0.1', () => {
    const lines = [`${req.method} ${req.url} HTTP/1.1`];
    for (let i = 0; i < req.rawHeaders.length; i += 2) lines.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}`);
    up.write(lines.join('\r\n') + '\r\n\r\n');
    if (head?.length) up.write(head);
    up.pipe(socket);
    socket.pipe(up);
  });
  up.on('error', () => socket.destroy());
  socket.on('error', () => up.destroy());
}
// Loopback only, both families: `localhost` may be tried as ::1 first.
const proxies = [];
for (const host of ['127.0.0.1', '::1']) {
  const p = http.createServer(onRequest);
  p.on('upgrade', onUpgrade);
  await new Promise((resolve) => {
    p.once('error', () => resolve());
    p.listen(proxyPort, host, () => resolve());
  });
  proxies.push(p);
}

async function waitHealthy() {
  for (let i = 0; i < 120; i++) {
    const ok = await fetch(`${PUBLIC}/healthz`).then((r) => r.ok, () => false);
    if (ok) return true;
    await new Promise((r) => setTimeout(r, 500));
  }
  return false;
}

let app;
let profile;
async function cleanup() {
  for (const t of held) clearTimeout(t);
  await app?.close().catch(() => {});
  for (const p of proxies) p.close();
  server.kill();
}

try {
  if (!(await waitHealthy())) throw new Error(`the server did not come up (log: ${path.join(dataDir, 'server.log')})`);

  // The harness reads these when it loads.
  process.env.FILEX_SERVER = PUBLIC;
  process.env.FILEX_EMAIL = 'admin@local';
  process.env.FILEX_PASSWORD = PASSWORD;
  const { arrived, check, launchApp, signIn, sleep, failures } = await import('./lib/harness.mjs');

  ({ app, profile } = await launchApp({
    env: { FILEX_DIAG_SLOW_MS: String(SLOW_MS) },
    args: ['--keep-windows-hidden'],
  }));
  const { win } = await signIn(app);
  const accountId = await win.evaluate(async () => (await window.filexApp.getState()).accounts[0].id);

  const NAME = 'Rapor Özeti.docx';
  const opened = app.waitForEvent('window', { timeout: 30_000 });
  await win.evaluate(({ id, name }) => window.filexApp.openDoc(id, `docs/${name}`), { id: accountId, name: NAME });
  const doc = await arrived(await opened, /\/files\/edit/);
  check('a document window opened on the account', /\/files\/edit/.test(doc.url()), doc.url());

  /** Runs `fn` on the document window's webContents in the main process. */
  const onDoc = (fn, arg) =>
    app.evaluate(
      ({ BrowserWindow }, { src, arg: a, name }) => {
        const w = BrowserWindow.getAllWindows().find((x) => x.getTitle() === name);
        if (!w) return 'no window';
        return new Function('w', 'a', src)(w, a);
      },
      { src: fn, arg, name: NAME },
    );

  // The injector still puts the account's bearer on the page's requests.
  const me = await doc.evaluate(() => fetch('/api/auth/me').then((r) => r.status, (e) => String(e)));
  check('the credential injector still works beside the new listeners', me === 200, String(me));

  await doc.evaluate((s) => fetch(`/__diag/reset?token=${s}`).then(() => 'answered', (e) => String(e)), TOKEN_LIKE);
  await doc.evaluate((s) => fetch(`/__diag/status?answer=503&share_password=${s}`).then((r) => r.status), WORD_LIKE);
  const late = await doc.evaluate(
    ({ s, ms }) => fetch(`/__diag/hang?ms=${ms}&ticket=${s}`).then((r) => r.text()),
    { s: TOKEN_LIKE, ms: SLOW_MS + 7000 },
  );
  check('the held request was answered in the end', late === 'late', late);

  await doc.evaluate(
    ({ t, w }) => {
      for (let i = 0; i < 40; i++) console.error(`save failed ${location.origin}/api/x?token=${t} Authorization: Bearer ${w}`);
      for (let i = 0; i < 20; i++) console.error(`distinct error ${i}`);
    },
    { t: TOKEN_LIKE, w: WORD_LIKE },
  );

  // A frame of another site, then its renderer killed.
  const framed = await doc.evaluate(
    (src) =>
      new Promise((resolve) => {
        const f = document.createElement('iframe');
        f.src = src;
        f.onload = () => resolve(true);
        document.body.appendChild(f);
        setTimeout(() => resolve(false), 10_000);
      }),
    FRAME_URL,
  );
  check('a cross-site frame loaded in the page', framed === true);
  await sleep(6500);
  const framePid = await onDoc(`
    const main = w.webContents.mainFrame;
    const f = main.framesInSubtree.find((x) => x !== main && x.processId !== main.processId);
    return f ? f.osProcessId : 0;
  `);
  check('the frame runs in a renderer of its own', typeof framePid === 'number' && framePid > 0, String(framePid));
  if (framePid > 0) {
    try {
      process.kill(framePid);
    } catch (e) {
      check('the frame renderer could be stopped', false, String(e));
    }
  }
  // Two looks of the sweep (every 5 s): a frame that cannot be read is
  // written only when it still cannot at the next one.
  await sleep(11_500);

  // Unresponsive / responsive. ⚠ Chromium's hang monitor does not fire here:
  // measured, a page whose main thread was kept busy for 40 s while
  // synthetic mouse, wheel and key input arrived (sendInputEvent) never got
  // `unresponsive` - in a window that is never shown nor focused, which this
  // suite must not change (the operator's screen and keyboard). So the
  // events are emitted on the window's REAL webContents in the main
  // process: what is measured is that the app listens on the right object
  // and writes how long it lasted; that Chromium emits them is Electron's
  // documented contract (test/doc-diag.test.ts drives the timing).
  await onDoc('w.webContents.emit("unresponsive"); return "ok";');
  await sleep(1200);
  await onDoc('w.webContents.emit("responsive"); return "ok";');

  await onDoc('w.webContents.forcefullyCrashRenderer(); return "ok";');
  await sleep(1500);
  await onDoc('w.destroy(); return "ok";');
  await sleep(1000);
  await app.close();
  app = null;

  const logFile = path.join(profile, 'logs', 'filex-desktop.log');
  const text = fs.existsSync(logFile) ? fs.readFileSync(logFile, 'utf8') : '';
  const lines = text.split('\n').filter((l) => /\[doc(win|req)\]/.test(l));
  const line = (re) => lines.find((l) => re.test(l));
  const report = (name, re) => check(name, !!line(re), line(re) ?? `(none of ${lines.length} doc lines)`);

  report('opened, with kind and name', /\[docwin\] opened \{"win":"doc#1","kind":"viewer","name":"Rapor Özeti\.docx"/);
  report('a torn-down request', /\[docreq\] request failed .*"path":"\/__diag\/reset\?token=\*".*"error":"net::ERR_/);
  report('a 503', /\[docreq\] request http .*"path":"\/__diag\/status\?answer=503&share_password=\*".*"status":503/);
  report('a request still waiting', /\[docreq\] request waiting .*"path":"\/__diag\/hang\?ms=\d+&ticket=\*"/);
  report('the late answer', /\[docreq\] request slow .*"path":"\/__diag\/hang\?ms=\d+&ticket=\*".*"status":200/);
  report('a console error, masked', /\[docwin\] console error .*"msg":"save failed \/api\/x\?token=\* Authorization: Bearer \*"/);
  report('repeats counted', /\[docwin\] console error .*"msg":"distinct error 0".*"repeated":39/);
  const consoleLines = lines.filter((l) => /\[docwin\] console error/.test(l) && /save failed|distinct error/.test(l));
  check('console errors rate limited (at most 10 a minute)', consoleLines.length <= 10, String(consoleLines.length));
  report('the frame gone', /\[docwin\] frame gone .*"frame":"http:\/\/localhost:\d+\/__diag\/frame"/);
  report('unresponsive', /\[docwin\] unresponsive \{"win":"doc#1"/);
  report('responsive, with how long', /\[docwin\] responsive \{"win":"doc#1","ms":(1[2-9]\d\d|[2-9]\d{3})\}/);
  report('the renderer gone', /\[docwin\] renderer gone \{"win":"doc#1","reason":"(killed|crashed)"/);
  report('what was held back', /\[docwin\] held back \{"win":"doc#1","console":\{.*"dropped":\d+/);
  report('closed', /\[docwin\] closed \{"win":"doc#1","ms":\d+/);

  const leaks = [TOKEN_LIKE, WORD_LIKE].flatMap((s) =>
    Array.from({ length: s.length - 7 }, (_, i) => s.slice(i, i + 8)).filter((piece) => text.includes(piece)),
  );
  check('no piece of a secret in the log', leaks.length === 0, leaks.length ? `${leaks.length} pieces` : '');
  check('no bearer value in the log', !/Bearer [^*\s"]/.test(text));

  console.log('\nthe doc lines (secrets were synthetic and are masked):');
  for (const l of lines) console.log('  ' + l);
  if (failures()) console.log(`\nserver log: ${path.join(dataDir, 'server.log')}`);
} catch (e) {
  console.error(e);
  process.exitCode = 1;
} finally {
  await cleanup();
}
const { finish } = await import('./lib/harness.mjs');
if (process.exitCode) process.exit(process.exitCode);
finish();
