// Does the app actually update itself?
//
// "latest.yml is on the server and returns 200" proves the FEED, not the
// updater. This drives the packaged app against a local feed that advertises a
// newer version and watches it go available → downloading → ready. Nothing is
// installed and the real feed is untouched: the copy under test has its
// `app-update.yml` rewritten to point at 127.0.0.1.
//
// Run (needs a packaged app):
//   FILEX_APP_BINARY=…\filex.exe FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/update-e2e.mjs

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { check, finish, launchApp, signIn, sleep } from './lib/harness.mjs';

// ── how the update is APPLIED (source guards — no app needed) ────────
//
// ⚠⚠ `quitAndInstall()` defaults to `isSilent = false`, which runs the NSIS
// installer with its full wizard. The app then looks like it threw a setup
// screen at somebody who never asked to install anything — which is exactly
// what happened. Silence has to be opted into at every call site, and you
// cannot see it from outside without actually installing, so it is asserted
// against the source.
{
  const src = fs.readFileSync(path.resolve('src/main.ts'), 'utf8');
  // Matched with the receiver on purpose: the prose above the code says
  // "quitAndInstall()" while explaining the trap, and a guard that fails on its
  // own explanation is a guard somebody deletes.
  const calls = [...src.matchAll(/autoUpdater\.quitAndInstall\(([^)]*)\)/g)].map((m) => m[1].trim());
  check('the update installs silently, never through the wizard',
    calls.length > 0 && calls.every((a) => /^true\s*,\s*true\s*$/.test(a)),
    calls.length ? calls.map((a) => `quitAndInstall(${a})`).join(' | ') : 'no quitAndInstall call found');
  check('…and comes back in the tray afterwards',
    /const UPDATED_FLAG = '--updated'/.test(src) && /includes\(UPDATED_FLAG\)/.test(src),
    'the installer relaunches us with --updated; a window appearing then is the disruption this design avoids');
}

const BIN = process.env.FILEX_APP_BINARY;
if (!BIN) {
  console.log('FILEX_APP_BINARY yok — bu süit kurulu uygulamayı sürer.');
  process.exit(2);
}

// The installer we already built is the payload; only the version in the feed
// is a lie, so the download and its checksum are real.
const pkg = path.resolve('release/filex-desktop-x64.exe');
check('a built installer is present to serve', fs.existsSync(pkg), pkg);
const bytes = fs.readFileSync(pkg);
const sha512 = createHash('sha512').update(bytes).digest('base64');
const FAKE = '9.9.9';

const feed = [
  `version: ${FAKE}`,
  'files:',
  '  - url: filex-desktop-x64.exe',
  `    sha512: ${sha512}`,
  `    size: ${bytes.length}`,
  'path: filex-desktop-x64.exe',
  `sha512: ${sha512}`,
  `releaseDate: '${new Date(0).toISOString()}'`,
  '',
].join('\n');

const server = http.createServer((req, res) => {
  if (req.url.startsWith('/latest.yml')) {
    res.writeHead(200, { 'Content-Type': 'text/yaml' });
    res.end(feed);
    return;
  }
  if (req.url.startsWith('/filex-desktop-x64.exe')) {
    res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Length': bytes.length });
    res.end(bytes);
    return;
  }
  res.writeHead(404).end();
});
await new Promise((r) => server.listen(8899, '127.0.0.1', r));

// ⚠ The BUILD OUTPUT (release/win-unpacked), never the operator's install:
// this rewrites the feed URL, and doing that to a real install would leave
// their app pointed at a dead local server. Copying 300 MB out first was tried
// and made the launch time out on a machine with live antivirus.
const appDir = path.dirname(BIN);
check('the suite is driving a build output, not an install',
  /win-unpacked/i.test(appDir), appDir);
const cfg = path.join(appDir, 'resources', 'app-update.yml');
check('the packaged app carries an update config', fs.existsSync(cfg),
  fs.existsSync(cfg) ? '' : 'no app-update.yml — electron-builder `publish` was not configured');
const cfgBackup = fs.existsSync(cfg) ? fs.readFileSync(cfg, 'utf8') : null;
if (cfgBackup !== null) {
  fs.writeFileSync(
    cfg,
    ['provider: generic', 'url: http://127.0.0.1:8899/', 'updaterCacheDirName: filex-updater-e2e', ''].join('\n'),
  );
}
const restore = () => {
  if (cfgBackup !== null) fs.writeFileSync(cfg, cfgBackup);
};
process.on('exit', restore);

const { app } = await launchApp({
  env: { FILEX_NO_UPDATE: '' }, // the rig disables updates; this suite is the exception
});
const { win } = await signIn(app, { label: 'filex desktop — update e2e' });

const state = async () => (await win.evaluate(() => window.filexApp.getState())).update ?? {};
await win.evaluate(() => window.filexApp.checkUpdate());

let seen = new Set();
let final = null;
for (let i = 0; i < 120; i++) {
  const u = await state();
  if (u.status) seen.add(u.status);
  if (u.status === 'ready' || u.status === 'error') { final = u; break; }
  await sleep(1000);
}
const u = final ?? (await state());

check('the app noticed the newer version', seen.has('available') || seen.has('downloading') || u.status === 'ready',
  `görülen durumlar: ${[...seen].join(' → ') || '—'}`);
check('…downloaded it', u.status === 'ready',
  u.status === 'error' ? `hata: ${u.error}` : `durum=${u.status} %${u.percent ?? '?'}`);
check('…and reports the version it staged', u.version === FAKE, String(u.version));

await app.close();
server.close();
restore();
finish();
