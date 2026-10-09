// Installing from a store, and a paid app's license (0.52.0) - what an
// administrator meets when a store's Install button sends them to filex.
//
//   node e2e/shots/store.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/store/ (the capture folder, ./release.mjs):
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
//   store-connected-1440.png   #162: Trusted stores with the store connected
//                              (its key's fingerprint) and the Store screen
//                              settings under it
//   store-screen-1440.png      #162: the App store page a person opens from
//                              the navigation panel - the catalog filex read
//                              and verified, one app installed, one to ask for
//   store-screen-request-1440.png #162: asking for an app, with a reason
//   store-request-review-1440.png #162: that request on Install requests, its
//                              review: from the store, "open the store review"
//   store-screen-storage-tab-1280.png #215: the store screen's Storage plugins
//                              tab - the server's note first, the Checks
//                              column (what the store's plugin validator
//                              proved), a plugin the store has no build of for
//                              this server in red, with nothing to ask for
//   store-screen-storage-tab-dark-tr-1280.png   the same, Turkish, dark
//   store-screen-storage-tab-390.png            the same on a 390-px phone
//   store-screen-storage-tab-dark-tr-390.png    the phone, Turkish, dark
//   storage-store-review-1280.png #215: a storage plugin's install link, its
//                              review - the server's notices (a program
//                              outside any sandbox, what the store's run
//                              measured, filex probing again, the store's
//                              signature), the build for this server and its
//                              SHA-256, the capabilities, the release notes
//   storage-store-review-dark-tr-1280.png       the same, Turkish, dark
//   storage-store-review-390.png                the same on a 390-px phone
//   storage-store-review-dark-tr-390.png        the phone, Turkish, dark
//
// The #215 pictures are MEASURED too, in a real browser, at 1280 and 390 px,
// light and dark, in English and Turkish (nothing scrolls sideways, no
// control on another, the review fits the window); a failed measurement
// throws. They are shown in docs/PLUGINS.md → Installing from a store and
// docs/APP-PLUGINS.md → The store screen (the `<!-- shot: … -->` lines there
// become the pictures once they are published: scripts/lib/shots-site.mjs →
// relinkText).
//
// ⚠⚠ The storage review needs the store over https. A storage plugin's link
// names its release feed and every build by an https address (appstore
// validateStorage), and the review READS the feed (plugin.ReadPinnedFeed,
// https only) - here from the store's own server, which serves the feed of
// the plugin it lists. So the four review pictures are taken only with
// SHOTS_STORE_HOST (the chain sets it): without it the link is refused
// before any review, and the scene says it left them out. The e2e suite
// cannot reach this review at all (e2e/tests/230-store-storage-plugins.spec.ts,
// lesson #1366): this scene, with its CA handed to filex, is the one place a
// browser opens it.
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

import { createHash, createPrivateKey, createPublicKey, randomUUID, sign } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lookup } from 'node:dns/promises';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { createServer as createTLSServer } from 'node:https';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { packBoardApp } from './board-app/pack.mjs';
import {
  PUBLIC_URL,
  bootInstance,
  client,
  dismissToasts,
  layoutProblems,
  log,
  mustSay,
  newContext,
  setLanguage,
  shootWhole,
  shot,
  signIn,
  sleep,
} from './scene.mjs';

const SET = 'store';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const OWNER = 'example';
const REPO = 'filex-board';
const LICENSE_KEY = 'FXL-7Q4M-2KD9-H3XW';
const LICENSEE = 'Northwind Traders';
/** #162: the store's one-time connection code, and the app its catalog offers beside the board. */
const CONNECT_CODE = 'fxc_' + 'S'.repeat(43);
const OTHER_APP = 'pdf-tools';

/* ── #215: the store's storage plugins ───────────────────────────────────── */

/** Every platform a store pins a storage plugin's build for (docs/PLUGINS.md). */
const STORAGE_PLATFORMS = ['darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64'];

/**
 * The storage plugins the store lists. The first is the one whose install
 * link is reviewed; the last has builds for macOS alone, so a Linux or
 * Windows server reads "No build for this server" on its row.
 */
const STORAGE_PLUGINS = [
  {
    name: 's3-archive',
    label: { en: 'S3 Archive', tr: 'S3 Arşivi' },
    summary: { en: 'Moves old files to a cheaper S3 storage class', tr: 'Eski dosyaları daha ucuz bir S3 depolama sınıfına taşır' },
    version: '1.4.0',
    platforms: STORAGE_PLATFORMS,
    notes: 'Multipart uploads resume after a restart.\nListing a bucket of 100,000 objects is twice as fast.',
    conformance: { platform: 'linux/amd64', filex: '0.55.0', verified: true, passed: 67, failed: 0, skipped: 12, driver: 's3archive',
      capabilities: ['write', 'delete', 'range', 'move', 'copy', 'mkdir', 'multipart'] },
  },
  {
    name: 'sharepoint',
    label: { en: 'SharePoint Online', tr: 'SharePoint Online' },
    summary: { en: 'Document libraries of a Microsoft 365 tenant', tr: 'Bir Microsoft 365 kiracısının belge kitaplıkları' },
    version: '2.1.0',
    platforms: STORAGE_PLATFORMS,
    notes: '',
    conformance: { platform: 'linux/amd64', filex: '0.55.0', verified: true, passed: 61, failed: 0, skipped: 18, driver: 'sharepoint',
      capabilities: ['write', 'delete', 'range', 'move', 'mkdir', 'set_mtime'] },
  },
  {
    name: 'icloud-drive',
    label: { en: 'iCloud Drive', tr: 'iCloud Drive' },
    summary: { en: 'The iCloud Drive of the Mac filex runs on', tr: 'filex’in çalıştığı Mac’in iCloud Drive’ı' },
    version: '0.9.2',
    platforms: ['darwin/amd64', 'darwin/arm64'],
    notes: '',
    conformance: { platform: 'darwin/arm64', filex: '0.55.0', verified: true, passed: 58, failed: 0, skipped: 21, driver: 'icloud',
      capabilities: ['write', 'delete', 'move', 'mkdir'] },
  },
];

/** The release a storage plugin's builds hang off (an https address: never fetched by the review). */
const storageBuildUrl = (p, plat) =>
  `https://github.com/${OWNER}/filex-${p.name}/releases/download/v${p.version}/${p.name}-${plat.replace('/', '-')}${plat.startsWith('windows/') ? '.exe' : ''}`;

/** A build's pinned SHA-256: fixed, so the review shows the same one every take. */
const storageBuildSha = (p, plat) => sha256hex(Buffer.from(`${p.name}@${p.version}@${plat}`, 'utf8'));

/** The store's path of the release feed it reviewed (served by the store's own server here). */
const storageFeedPath = (p) => `/releases/${p.name}/v${p.version}/filex-storage.json`;

/**
 * The four looks every #215 picture is taken in, of the eight the scene
 * measures: English light and Turkish dark, at 1280 and 390 px.
 */
const LOOKS = [];
for (const scheme of ['light', 'dark']) {
  for (const locale of ['en', 'tr']) {
    for (const width of [1280, 390]) {
      const shoots = (scheme === 'light' && locale === 'en') || (scheme === 'dark' && locale === 'tr');
      LOOKS.push({ scheme, locale, width, height: width === 390 ? 844 : 900, suffix: shoots ? `${scheme === 'dark' ? 'dark-tr-' : ''}${width}` : '' });
    }
  }
}

/**
 * What of a dialog does not fit: the dialog past the window's sides, or a
 * piece of it past the dialog's own (a long SHA-256 or address that did not
 * break). layoutProblems reads the page under a dialog, not the dialog.
 */
async function dialogProblems(dialog) {
  return dialog.evaluate((el) => {
    const out = [];
    const view = window.innerWidth;
    const box = el.getBoundingClientRect();
    if (box.left < -0.5 || box.right > view + 0.5) out.push(`the dialog sticks out: ${Math.round(box.left)}..${Math.round(box.right)} of ${view}`);
    for (const c of el.querySelectorAll('p, li, dd, dt, h2, h3, button, input, span')) {
      const cs = getComputedStyle(c);
      if (cs.visibility === 'hidden' || cs.opacity === '0') continue;
      for (const r of c.getClientRects()) {
        if (r.width > 0 && (r.left < box.left - 0.5 || r.right > box.right + 0.5)) {
          out.push(`${c.tagName.toLowerCase()} "${(c.textContent || '').trim().slice(0, 40)}" sticks out of the dialog: ${Math.round(r.left)}..${Math.round(r.right)} of ${Math.round(box.left)}..${Math.round(box.right)}`);
        }
      }
    }
    if (el.scrollWidth > el.clientWidth + 1) out.push(`the dialog scrolls sideways (${el.scrollWidth} > ${el.clientWidth})`);
    return out;
  });
}

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
    /* #162: the catalog (a signed index) and an icon, set once the origin is known. */
    let indexBytes = Buffer.from('');
    let indexSig = '';
    const media = new Map();
    /* #215: the release feeds of the storage plugins it lists, by path, as the store reviewed them. */
    const feeds = new Map();
    const connected = { key: '', id: randomUUID() };
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
        if (req.method === 'GET' && url === '/v1/index.json') {
          res.writeHead(200, { 'Content-Type': 'application/json' }).end(indexBytes);
          return;
        }
        if (req.method === 'GET' && url === '/v1/index.json.sig') {
          res.writeHead(200, { 'Content-Type': 'text/plain' }).end(indexSig + '\n');
          return;
        }
        const icon = url.match(/^\/v1\/media\/([0-9a-f]{64}\.png)$/);
        if (req.method === 'GET' && icon) {
          const body = media.get(icon[1]);
          if (!body) return send(404, { error: 'not_found' });
          res.writeHead(200, { 'Content-Type': 'image/png' }).end(body);
          return;
        }
        if (req.method === 'GET' && feeds.has(url)) {
          res.writeHead(200, { 'Content-Type': 'application/json' }).end(feeds.get(url));
          return;
        }
        if (req.method === 'POST' && url === '/v1/instances/connect') {
          const b = JSON.parse(raw || '{}');
          if (b.code !== CONNECT_CODE) return send(404, { error: 'not_found' });
          if (b.filex_origin !== PUBLIC_URL) return send(409, { error: 'wrong_instance' });
          connected.key = b.public_key;
          send(200, envelope(idx, {
            store: origin, instance_id: connected.id, filex_origin: PUBLIC_URL,
            key_fingerprint: sha256hex(Buffer.from(b.public_key, 'hex')), connected_at: iso(new Date()),
          }));
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

    /* #162: the store's signed index - the board app (installed below) and one
       to ask for, with an icon - signed over its bytes as served. */
    const iconBytes = readFileSync(new URL('../../web/public/icons/icon-192.png', import.meta.url));
    const iconName = `${sha256hex(iconBytes)}.png`;
    media.set(iconName, iconBytes);
    const indexApp = (name, label, summary, perms, withIcon) => ({
      name, kind: 'app', publisher: 'example', repo: `${OWNER}/${name}`, categories: [],
      label: { en: label, tr: label }, summary: { en: summary, tr: summary },
      ...(withIcon ? { icon: { url: `${origin}/v1/media/${iconName}`, sha256: iconName.slice(0, 64) } } : {}),
      screenshots: [], latest: version, revoked: null,
      versions: [{
        version, ref: `v${version}`, commit, filex: '>=0.52.0', published_at: '2026-10-01',
        manifest: { url: 'https://example.invalid/m.json', sha256: sha256hex(manifestBytes), sig: '00' },
        wasm: null, ui: { url: 'https://example.invalid/ui.zip', sha256: sha256hex(uiBytes), sig: '00' },
        permissions: perms, languages: [], engines: [], security: false, yanked: null,
      }],
    });
    /* #215: its storage plugins - every build pinned by SHA-256 and signed with
       the store's artifact key, the feed it reviewed pinned too, and what its
       plugin validator measured. */
    const artifact = seededKey('artifact-shots', 'artifact');
    const storageBuilds = (p) => Object.fromEntries(p.platforms.map((plat) => {
      const sha = storageBuildSha(p, plat);
      const sig = sign(null, Buffer.from(sha, 'utf8'), artifact.priv).toString('hex');
      return [plat, { url: storageBuildUrl(p, plat), sha256: sha, size: 18_874_368, sig }];
    }));
    const feedSha = new Map();
    for (const p of STORAGE_PLUGINS) {
      const feed = {
        name: p.name, version: p.version, filex: '>=0.54.0', notes: p.notes,
        binaries: Object.fromEntries(p.platforms.map((plat) => [plat, { url: storageBuildUrl(p, plat), sha256: storageBuildSha(p, plat) }])),
      };
      const bytes = Buffer.from(JSON.stringify(feed, null, 2), 'utf8');
      feeds.set(storageFeedPath(p), bytes);
      feedSha.set(p.name, sha256hex(bytes));
    }
    const indexStorage = (p) => ({
      name: p.name, kind: 'storage', publisher: 'example', repo: `${OWNER}/filex-${p.name}`, categories: [],
      label: p.label, summary: p.summary, screenshots: [], latest: p.version, revoked: null,
      versions: [{
        version: p.version, filex: '>=0.54.0', published_at: '2026-09-28', manifest: { sha256: feedSha.get(p.name) },
        permissions: [], yanked: null,
        binaries: Object.fromEntries(p.platforms.map((plat) => [plat, { sha256: storageBuildSha(p, plat) }])),
        conformance: p.conformance,
      }],
    });
    const indexDoc = {
      schema: 1, serial: 12, generated_at: iso(new Date()), expires_at: iso(new Date(Date.now() + 30 * 86400_000)),
      keys: [], publishers: [{ id: 'example', name: 'Example Apps', github: 'example', verified: true, official: false }],
      apps: [
        indexApp(app, 'Board', 'A kanban board in a file', board.manifest.permissions, false),
        indexApp(OTHER_APP, 'PDF tools', 'Merge, split and rotate PDFs', ['files:read', 'files:write'], true),
        ...STORAGE_PLUGINS.map(indexStorage),
      ],
    };
    indexBytes = Buffer.from(JSON.stringify(indexDoc, null, 2) + '\n', 'utf8');
    indexSig = sign(null, Buffer.from(sha256hex(indexBytes), 'utf8'), idx.priv).toString('hex');

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
    /** #215: a signed install link for a storage plugin - its feed, every build, the store's run. */
    const storageLink = (token, p) => {
      intents.set(token, {
        store: origin, token_id: `tid-${token}`, app: p.name, kind: 'storage', version: p.version,
        repo: `${OWNER}/filex-${p.name}`, ref: `v${p.version}`, commit: createHash('sha1').update(`${p.name}@${p.version}`).digest('hex'),
        filex_origin: PUBLIC_URL, manifest_sha256: feedSha.get(p.name), feed_url: `${origin}${storageFeedPath(p)}`,
        binaries: storageBuilds(p), conformance: p.conformance, permissions: [],
        filex_range: '>=0.54.0', paid: false, expires_at: iso(new Date(Date.now() + 30 * 60_000)),
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

    // 6. #162: connect this filex to the store with its one-time code, and show
    //    the store screen to everyone.
    await admin.json('/api/admin/app-plugins/store-view', {
      method: 'PUT',
      body: JSON.stringify({ settings: { enabled: true, stores: [origin], audience: 'everyone', roles: [], groups: [] } }),
    });
    await page.goto(`${inst.url}/admin/plugins`);
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-stores').waitFor({ timeout: 20_000 });
    await page.getByTestId('app-store-connect-open').first().click();
    await page.locator('input[name="app-store-connect-code"]').fill(CONNECT_CODE);
    await page.getByTestId('app-store-connect').click();
    await page.getByTestId('app-store-connection-detail').waitFor({ timeout: 20_000 });
    if (!connected.key) throw new Error('the store was not sent a key');
    await mustSay(page.getByTestId('app-stores'), 'the connected store', ['Connected']);
    await page.getByTestId('app-store-view').waitFor({ timeout: 20_000 });
    await dismissToasts(page);
    await page.getByTestId('app-stores').scrollIntoViewIfNeeded();
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(page, SET, 'store-connected-1440.png');

    // 7. The App store page, as a person opens it from the navigation panel.
    await page.goto(`${inst.url}/drive/app-store`);
    const screen = page.getByTestId('store-screen');
    await screen.waitFor({ timeout: 20_000 });
    await page.getByTestId(`store-app-${OTHER_APP}`).waitFor({ timeout: 20_000 });
    await mustSay(screen, 'the store screen', ['App store', 'PDF tools', 'Board', 'Installed']);
    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'store-screen-1440.png');

    // 8. Asking for an app.
    await page.getByTestId(`store-app-actions-${OTHER_APP}`).click();
    await page.locator(`.fe-ctx [data-testid="store-app-actions-${OTHER_APP}-request"]`).last().click();
    await page.locator('textarea[name="store-request-reason"]').fill('The finance team merges the monthly statements into one PDF.');
    await page.mouse.move(4, 4);
    await sleep(400);
    await shootWhole(page, page.locator('dialog[open] [role="dialog"]').last(), SET, 'store-screen-request-1440.png', {
      restore: { width: 1440, height: 900 },
    });
    await page.getByTestId('store-request-send').click();
    await page.getByTestId('store-request-status-1').waitFor({ timeout: 20_000 }).catch(() => undefined);

    // 9. The request on Install requests: from the store, approved through its review.
    await page.goto(`${inst.url}/admin/plugins`);
    const reqs = page.getByTestId('plugin-requests');
    await reqs.waitFor({ timeout: 20_000 });
    await page.locator('[data-testid^="plugin-request-actions-"]').first().click();
    await page.locator('.fe-ctx [data-testid$="-review"]').last().click();
    const review = page.getByTestId('plugin-request-review');
    await review.waitFor({ timeout: 20_000 });
    await mustSay(review, 'the store request', ['PDF tools', origin, 'open the store review']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shootWhole(page, page.locator('dialog[open] [role="dialog"]').last(), SET, 'store-request-review-1440.png', {
      restore: { width: 1440, height: 900 },
    });
    log('a store link: trusting the store, its review, the trusted stores, a license valid and held, the store screen');
    await ctx.close();

    // 10. #215: the store screen's Storage plugins tab, in every look - the
    //     server's note, the Checks column, the plugin with no build here.
    const [archive, , mac] = STORAGE_PLUGINS;
    // A filex on a Mac (FILEX_BIN on this machine) runs a build of the
    // macOS-only plugin: its row is then not the one without a build.
    const macHere = process.platform === 'darwin' && !inst.container;
    const problems = [];
    for (const look of LOOKS) {
      const vctx = await newContext(browser, { scheme: look.scheme, width: look.width, height: look.height });
      const vpage = await vctx.newPage();
      await signIn(vpage, inst.url, ADMIN);
      await setLanguage(admin, vpage, look.locale);
      await vpage.goto(`${inst.url}/drive/app-store`);
      await vpage.waitForFunction((l) => document.documentElement.lang === l, look.locale, { timeout: 20_000 });
      await vpage.getByTestId('store-screen').waitFor({ timeout: 20_000 });
      await vpage.getByTestId('store-screen-kind-storage').click();
      await vpage.getByTestId('store-screen-storage-note').waitFor({ timeout: 20_000 });
      for (const p of STORAGE_PLUGINS) await vpage.getByTestId(`store-app-${p.name}`).waitFor({ timeout: 20_000 });
      if ((await vpage.getByTestId(`store-app-${board.manifest.name}`).count()) !== 0) throw new Error('the Storage tab lists an app');
      await mustSay(vpage.getByTestId(`store-app-checks-${archive.name}`), 'the Checks column', [String(archive.conformance.passed)]);
      const red = (await vpage.getByTestId(`store-app-checks-${mac.name}`).getAttribute('class')) ?? '';
      if (!macHere && !red.includes('text-rose-600')) throw new Error(`the row of ${mac.name}, which has no build for this server, is not drawn as such`);
      for (const x of await layoutProblems(vpage)) problems.push(`Storage tab, ${look.scheme} ${look.locale} ${look.width}px: ${x}`);
      if (look.suffix) {
        await dismissToasts(vpage);
        await vpage.mouse.move(2, 2);
        await sleep(500);
        await shot(vpage, SET, `store-screen-storage-tab-${look.suffix}.png`);
      }
      await setLanguage(admin, vpage, 'en');
      await vctx.close();
    }
    log('the Storage plugins tab fits at 1280 and 390 px, light and dark, in English and Turkish');

    // 11. #215: a storage plugin's install link and its review - only from a
    //     store served over https (SHOTS_STORE_HOST): the link names its feed
    //     and builds by https addresses, and the review reads the feed.
    if (!origin.startsWith('https://')) {
      log('left out: the storage plugin review (storage-store-review-*.png) needs the store over https - set SHOTS_STORE_HOST (the chain does)');
    } else {
      let n = 0;
      for (const look of LOOKS) {
        const vctx = await newContext(browser, { scheme: look.scheme, width: look.width, height: look.height });
        const vpage = await vctx.newPage();
        await signIn(vpage, inst.url, ADMIN);
        await setLanguage(admin, vpage, look.locale);
        n += 1;
        await vpage.goto(`${inst.url}${storageLink(`shots-storage-${String(n).padStart(4, '0')}`, archive)}`);
        const review = vpage.getByTestId('storage-store-review');
        await review.waitFor({ timeout: 30_000 });
        await vpage.waitForFunction((l) => document.documentElement.lang === l, look.locale, { timeout: 20_000 });
        await vpage.getByTestId('storage-store-notice-warning').first().waitFor({ timeout: 10_000 });
        if ((await vpage.getByTestId('storage-store-notice-info').count()) < 3) {
          throw new Error(`the review says too little: ${(await review.innerText()).slice(0, 600)}`);
        }
        const sha = (await vpage.getByTestId('storage-store-sha256').innerText()).trim();
        if (!archive.platforms.some((plat) => storageBuildSha(archive, plat) === sha)) {
          throw new Error(`the review shows the build's SHA-256 as ${sha}, which the link does not pin`);
        }
        await vpage.getByTestId('storage-store-capabilities').waitFor({ timeout: 10_000 });
        await vpage.getByTestId('storage-store-notes').waitFor({ timeout: 10_000 });
        if (await vpage.getByTestId('storage-store-install').isDisabled()) throw new Error('the review refuses the install');
        const dialog = vpage.locator('dialog[open] [role="dialog"]').last();
        for (const x of await dialogProblems(dialog)) problems.push(`review, ${look.scheme} ${look.locale} ${look.width}px: ${x}`);
        if (look.suffix) {
          await vpage.mouse.move(2, 2);
          await sleep(500);
          await shootWhole(vpage, dialog, SET, `storage-store-review-${look.suffix}.png`, { restore: { width: look.width, height: look.height } });
        }
        // Closed without installing: the store is told the link was cancelled.
        await vpage.getByTestId('storage-store-cancel').click();
        await vpage.getByTestId('store-install-cancelled').waitFor({ timeout: 20_000 });
        await setLanguage(admin, vpage, 'en');
        await vctx.close();
      }
      log('the storage plugin review: its notices, the build and its SHA-256, at 1280 and 390 px, light and dark, in English and Turkish');
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
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
