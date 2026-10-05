// Installing from a store, and a paid app's license (0.52.0) - what an
// administrator meets when a store's Install button sends them to filex.
//
//   node e2e/shots/store.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/store/ (the release named in ./release.mjs):
//
//   store-trust-1440.png       the first link from a store: "This store is not
//                              trusted yet", its address and the fingerprints
//                              of the two keys it publishes, the box to tick
//   store-review-1440.png      the install review that opens from the link,
//                              marked "From store", the paid app's license key
//                              by its prefix
//   store-trusted-1440.png     Admin → Plugins → Apps, Trusted stores: who
//                              trusted the store and when, its fingerprints
//   store-license-1440.png     the app's page, its License section: valid,
//                              licensed to, seats, dates, the key's prefix
//   store-license-held-1440.png the same page after the store revoked the
//                              license: the app held (Unlicensed), the band
//                              every admin page carries while it lasts
//
// The store is a small server here that signs exactly as a store must
// (ed25519 over the lower-hex SHA-256 of the canonical JSON, as
// e2e/tests/202-store-install.spec.ts does), with keys made from fixed seeds
// so the fingerprints in the pictures do not change from one take to the
// next. The app is e2e/shots/board-app/ (apps.mjs's), sold here as a paid app
// and served from a stand-in for GitHub's raw host (FILEX_APP_GITHUB_RAW_BASE).
//
// ⚠ Where the store is. Plain http is taken for a store on this machine only
// (FILEX_PLUGIN_LOOPBACK_SOURCES), and the pictures would then name it
// `http://127.0.0.1:<port>` - true, and nothing like what a reader meets.
// SHOTS_STORE_HOST (e.g. store.example.com) serves the store over https on
// port 443 under that name instead, with a certificate from a CA made for
// this run and handed to filex alone (SSL_CERT_FILE): the name must resolve
// to this machine (an /etc/hosts line), port 443 must be free and openssl
// must be on the PATH - a container run as root has all three. The pictures
// in the repository are taken that way; without it the scene still runs and
// shows the loopback address.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs), SHOTS_STORE_HOST.

import { createHash, createPrivateKey, createPublicKey, sign } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lookup } from 'node:dns/promises';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTLSServer } from 'node:https';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { packBoardApp } from './board-app/pack.mjs';
import { PUBLIC_URL, bootInstance, client, dismissToasts, log, mustSay, newContext, shootWhole, shot, signIn, sleep } from './scene.mjs';

const SET = 'store';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const OWNER = 'example';
const REPO = 'filex-board';
const LICENSE_KEY = 'FXL-7Q4M-2KD9-H3XW';
const LICENSEE = 'Northwind Traders';

/* ── signing as a store must ─────────────────────────────────────────────── */

function canonical(v) {
  if (v === null || typeof v !== 'object') return JSON.stringify(v);
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']';
  return '{' + Object.keys(v).sort().map((k) => JSON.stringify(k) + ':' + canonical(v[k])).join(',') + '}';
}

const sha256hex = (b) => createHash('sha256').update(b).digest('hex');

/** An ed25519 key from a fixed seed: the same fingerprint every take. */
function seededKey(id, use) {
  const seed = createHash('sha256').update(`filex shots store key ${id}`).digest();
  const der = Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), seed]);
  const priv = createPrivateKey({ key: der, format: 'der', type: 'pkcs8' });
  const pub = createPublicKey(priv).export({ format: 'der', type: 'spki' }).subarray(-32);
  return { id, use, priv, pub };
}

function envelope(k, payload) {
  const sig = sign(null, Buffer.from(sha256hex(Buffer.from(canonical(payload), 'utf8')), 'utf8'), k.priv).toString('hex');
  return { payload, key_id: k.id, signature: sig };
}

const iso = (d) => d.toISOString().replace(/\.\d+Z$/, 'Z');

/* ── where the store listens ─────────────────────────────────────────────── */

/**
 * A CA and a certificate for `host`, made with openssl into `dir`: the CA is
 * handed to filex (SSL_CERT_FILE), the certificate to the store's server.
 */
function makeCertificate(dir, host) {
  const run = (...args) => execFileSync('openssl', args, { cwd: dir, stdio: ['ignore', 'pipe', 'pipe'] });
  writeFileSync(join(dir, 'san.ext'), `subjectAltName=DNS:${host}\nbasicConstraints=CA:FALSE\n`);
  run('req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes', '-days', '2',
    '-subj', '/CN=filex screenshots store CA', '-keyout', 'ca.key', '-out', 'ca.crt');
  run('req', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes',
    '-subj', `/CN=${host}`, '-keyout', 'store.key', '-out', 'store.csr');
  run('x509', '-req', '-in', 'store.csr', '-CA', 'ca.crt', '-CAkey', 'ca.key', '-CAcreateserial', '-days', '2',
    '-extfile', 'san.ext', '-out', 'store.crt');
  return { ca: join(dir, 'ca.crt'), key: readFileSync(join(dir, 'store.key')), cert: readFileSync(join(dir, 'store.crt')) };
}

async function listen(server, port, host) {
  await new Promise((ok, fail) => {
    server.once('error', fail);
    server.listen(port, host, ok);
  });
  return server.address().port;
}

async function main() {
  const work = mkdtempSync(join(tmpdir(), 'filex-shots-store-'));
  const servers = [];
  let inst = null;
  let browser = null;
  try {
    /* The app, as its repository serves it: the board app, under its tag and its commit. */
    const board = packBoardApp(work);
    const manifestBytes = readFileSync(board.manifestPath);
    const uiBytes = readFileSync(board.uiZip);
    const app = board.manifest.name;
    const version = board.manifest.version;
    const commit = createHash('sha1').update(`${app}@${version}`).digest('hex');
    const gh = join(work, 'github');
    for (const ref of [`v${version}`, commit]) {
      const dir = join(gh, OWNER, REPO, ref);
      mkdirSync(dir, { recursive: true });
      writeFileSync(join(dir, 'filex-app.json'), manifestBytes);
      writeFileSync(join(dir, 'ui.zip'), uiBytes);
    }
    const github = createServer((req, res) => {
      const p = decodeURIComponent(new URL(req.url ?? '/', 'http://x').pathname);
      const file = join(gh, ...p.split('/').filter((s) => s && s !== '..'));
      try {
        const body = readFileSync(file);
        res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Length': body.length }).end(body);
      } catch {
        res.writeHead(404).end('not found');
      }
    });
    servers.push(github);
    const ghPort = await listen(github, 0, '127.0.0.1');

    /* The store. */
    const idx = seededKey('index-shots', 'index');
    const lic = seededKey('license-shots', 'license');
    const intents = new Map();
    const completions = [];
    let licenseResult = 'valid';
    const handler = (req, res) => {
      const url = req.url ?? '';
      const send = (code, body) => res.writeHead(code, { 'Content-Type': 'application/json' }).end(JSON.stringify(body));
      let raw = '';
      req.on('data', (c) => (raw += c));
      req.on('end', () => {
        if (req.method === 'GET' && url === '/v1/keys.json') {
          send(200, { keys: [idx, lic].map((k) => ({ id: k.id, use: k.use, ed25519: k.pub.toString('hex'), status: 'active' })) });
          return;
        }
        const done = url.match(/^\/v1\/install\/([^/]+)\/complete$/);
        if (req.method === 'POST' && done) {
          completions.push({ token: done[1], ...JSON.parse(raw || '{}') });
          res.writeHead(204).end();
          return;
        }
        const read = url.match(/^\/v1\/install\/([^/]+)$/);
        if (req.method === 'GET' && read) {
          const payload = intents.get(read[1]);
          if (!payload) return send(404, { error: 'not_found' });
          send(200, envelope(idx, payload));
          return;
        }
        if (req.method === 'POST' && url === '/v1/licenses/verify') {
          const b = JSON.parse(raw || '{}');
          const now = new Date();
          send(200, envelope(lic, {
            result: licenseResult, app: b.app, licensee: LICENSEE, seats: 25, seats_used: 1,
            valid_until: iso(new Date(now.getTime() + 365 * 86400_000)),
            updates_until: iso(new Date(now.getTime() + 365 * 86400_000)),
            instance_id: b.instance_id, checked_at: iso(now),
            next_check_by: iso(new Date(now.getTime() + 86400_000)),
            grace_until: iso(new Date(now.getTime() + 7 * 86400_000)),
          }));
          return;
        }
        send(404, { error: 'not_found' });
      });
    };
    const host = (process.env.SHOTS_STORE_HOST ?? '').trim().toLowerCase();
    let origin;
    const env = {
      FILEX_UPDATE_CHECK: '0',
      FILEX_PLUGIN_LOOPBACK_SOURCES: '1',
      FILEX_APP_GITHUB_RAW_BASE: `http://127.0.0.1:${ghPort}`,
    };
    if (host) {
      if (!/^[a-z0-9.-]+$/.test(host)) throw new Error(`SHOTS_STORE_HOST=${host}: a host name, nothing else`);
      const { address } = await lookup(host);
      if (!['127.0.0.1', '::1'].includes(address)) {
        throw new Error(`SHOTS_STORE_HOST=${host} resolves to ${address}; it must name this machine (an /etc/hosts line: 127.0.0.1 ${host})`);
      }
      const tls = makeCertificate(work, host);
      const store = createTLSServer({ key: tls.key, cert: tls.cert }, handler);
      servers.push(store);
      await listen(store, 443, '127.0.0.1');
      origin = `https://${host}`;
      // Go reads its roots from here alone when it is set: the store's CA,
      // for this filex only. Nothing else it talks to here is https.
      env.SSL_CERT_FILE = tls.ca;
    } else {
      const store = createServer(handler);
      servers.push(store);
      origin = `http://127.0.0.1:${await listen(store, 0, '127.0.0.1')}`;
    }
    log(`store at ${origin}, its repository at http://127.0.0.1:${ghPort}`);

    const link = (token) => {
      intents.set(token, {
        store: origin, token_id: `tid-${token}`, app, kind: 'app', version,
        repo: `${OWNER}/${REPO}`, ref: `v${version}`, commit, filex_origin: PUBLIC_URL,
        manifest_sha256: sha256hex(manifestBytes), ui_sha256: sha256hex(uiBytes),
        permissions: board.manifest.permissions, filex_range: '>=0.52.0', paid: true,
        license_key: LICENSE_KEY, expires_at: iso(new Date(Date.now() + 30 * 60_000)),
      });
      return `/admin/store-install#store=${encodeURIComponent(origin)}&intent=${token}`;
    };

    inst = await bootInstance({ name: SET, admin: ADMIN, env });
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    await admin.post('/api/notifications/read-all', {});

    browser = await chromium.launch();
    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // 1. The first link from a store nobody trusted yet.
    await page.goto(`${inst.url}${link('shots-install-0001')}`);
    const trust = page.getByTestId('store-install-trust');
    await trust.waitFor({ timeout: 20_000 });
    if ((await page.getByTestId('store-trust-fingerprint').count()) !== 2) throw new Error('the trust page does not show the two keys');
    await mustSay(trust, 'the trust page', ['This store is not trusted yet', origin, 'Install links', 'Licenses']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(page, SET, 'store-trust-1440.png');

    // 2. Trusted: the review from the store.
    await page.locator('input[name="store-trust-compared"]').check();
    await page.getByTestId('store-trust-approve').click();
    const wizard = page.getByTestId('app-plugin-wizard');
    await wizard.waitFor({ timeout: 30_000 });
    await page.getByTestId('app-plugin-from-store').waitFor({ timeout: 15_000 });
    await mustSay(page.getByTestId('app-plugin-from-store'), 'the review', [`From store ${origin}`]);
    await mustSay(page.getByTestId('app-plugin-store-license-prefix'), 'the license box', ['FXL-7Q…']);
    if ((await wizard.innerText()).includes(LICENSE_KEY)) throw new Error('the review shows the whole license key');
    await page.mouse.move(4, 4);
    await sleep(400);
    await shootWhole(page, page.locator('dialog[open] [role="dialog"]').last(), SET, 'store-review-1440.png', {
      restore: { width: 1440, height: 900 },
    });

    // Install it.
    await page.locator('input[name="app-plugin-understand"]').check();
    await page.getByTestId('app-plugin-install').click();
    await page.getByTestId('app-plugin-done').waitFor({ timeout: 60_000 });
    await page.getByTestId('app-plugin-done').getByRole('button').click();
    await page.getByTestId('store-install-done').waitFor({ timeout: 20_000 });
    for (let i = 0; i < 50 && !completions.some((c) => c.result === 'installed'); i++) await sleep(100);
    if (!completions.some((c) => c.result === 'installed')) throw new Error('the store was not told the link was installed');

    // 3. Admin → Plugins → Apps: the trusted stores under the apps.
    await page.goto(`${inst.url}/admin/plugins`);
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-plugins').waitFor({ timeout: 20_000 });
    const stores = page.getByTestId('app-stores');
    await stores.waitFor({ timeout: 20_000 });
    await page.getByTestId('app-store-row').first().waitFor({ timeout: 15_000 });
    await mustSay(stores, 'the trusted stores', [origin]);
    await dismissToasts(page);
    await stores.scrollIntoViewIfNeeded();
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(stores, SET, 'store-trusted-1440.png');

    // 4. The app's page: its license.
    await page.goto(`${inst.url}/admin/plugins/apps/${app}`);
    const license = page.getByTestId('app-plugin-license');
    await license.waitFor({ timeout: 20_000 });
    await mustSay(page.getByTestId('app-plugin-license-status'), 'the license status', ['valid']);
    await mustSay(license, 'the License section', [LICENSEE, 'FXL-7Q…']);
    await license.scrollIntoViewIfNeeded();
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(page, SET, 'store-license-1440.png');

    // 5. The store revokes it: Verify now holds the app, and every admin page says so.
    licenseResult = 'revoked';
    await page.getByTestId('app-plugin-license-verify').click();
    await page.getByTestId('app-plugin-license-held').waitFor({ timeout: 20_000 });
    await page.goto(`${inst.url}/admin/plugins`);
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-plugins').waitFor({ timeout: 20_000 });
    const band = page.getByTestId('app-license-held-band');
    await band.waitFor({ timeout: 20_000 });
    await mustSay(band, 'the band', ['oard']);
    await mustSay(page.locator('main'), 'the apps list', ['Unlicensed']);
    await dismissToasts(page);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(page, SET, 'store-license-held-1440.png');
    log('a store link: trusting the store, its review, the trusted stores, a license valid and held');
    await ctx.close();
  } finally {
    if (browser) await browser.close();
    if (inst) await inst.stop();
    for (const s of servers) s.close();
    rmSync(work, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
