// The stage the newer shot scripts are taken on: an instance of their own, the
// requests they seed it with, a browser pinned to English, one `shot()` call,
// and the documents a picture needs to look like somebody's real work.
//
// ⚠ One module rather than one copy per script. capture.mjs, sidenav.mjs and
// driveshell.mjs each grew their own boot/api/signIn; the scripts written for
// v0.43.0 (apps, signing, appearance, symlinks) share this one, so a change to
// how an instance is booted or how a session is pinned to English is made once.
//
// ⚠ Every instance gets its OWN port and its OWN data directory, and nothing
// from the caller's FILEX_* environment reaches it. A shot of somebody's live
// server is a shot of their data, and a stray FILEX_DATABASE_URL would point a
// throwaway run at a real database.

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { compareServedUI } from '../../scripts/check-embed.mjs';
import {
  ENGINES_IMAGE,
  containerLogs,
  dockerInfo,
  ensureImage,
  removeContainer,
  startContainer,
} from '../../scripts/lib/containers.mjs';
import { goBuild } from '../../scripts/lib/go-build.mjs';
import { RUN_MARKER } from '../../scripts/lib/procs.mjs';
import { APP_LOCATIONS, locateApp } from '../helpers/app-locations.mjs';
import { SHOTS_LDFLAGS, shotsDir } from './release.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO = resolve(HERE, '../..');

export const log = (...a) => console.log('•', ...a);
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The host the links in a picture are shown with. The instance is reached on
// loopback; a README picture reading `http://127.0.0.1:53817/s/…` teaches the
// reader nothing, so the PUBLIC url — what a link is built from — gets a
// presentable value (the same one capture.mjs uses).
export const PUBLIC_URL = 'https://files.example.com';

// ── the instance ──────────────────────────────────────────────────────────

function defaultBin() {
  for (const p of [join(REPO, 'bin/filex.exe'), join(REPO, 'bin/filex')]) {
    if (existsSync(p)) return p;
  }
  return null;
}

export function freePort() {
  return new Promise((resolvePort, reject) => {
    const srv = createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolvePort(port));
    });
  });
}

async function waitForHealth(url, proc, deadlineMs = 45_000) {
  const until = Date.now() + deadlineMs;
  while (Date.now() < until) {
    if (proc && proc.exitCode !== null) throw new Error(`filex exited (${proc.exitCode}) before it answered /healthz`);
    try {
      const r = await fetch(`${url}/healthz`);
      if (r.ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(300);
  }
  throw new Error(`no healthy instance at ${url}`);
}

/**
 * Boots `filex serve` from FILEX_BIN (else bin/filex[.exe]) on a free loopback
 * port with a fresh data directory, and returns what a script needs to talk to
 * it. `env` adds to — never replaces — the variables every shot instance gets.
 *
 * `engines` names the conversion engines the scene's picture depends on
 * (`imagemagick`, `libreoffice`, … — the keys of the server's own probe,
 * backend/internal/wasmplugin/engines.go). When THIS host lacks any of them
 * the same build is run inside the full container image instead, which carries
 * them all — see bootInContainer. `SHOTS_ENGINES=host|container` forces one
 * side; the default, `auto`, asks the server rather than guessing.
 *
 * ⚠ Asked of the SERVER, not looked up here: the engines a picture shows are
 * the ones filex found, by its own table of binary names. A second copy of
 * that table in this file is a second answer that can disagree with the
 * screen.
 *
 * The answer carries `storageRoot(name)` — the path to give a local storage,
 * which inside a container is the CONTAINER's path — and `container` (its
 * name, or null on the host).
 *
 * ⚠ `filex serve` takes NO flags: the listen address and the data directory
 * come from the environment, and `--listen` exits with "unknown flag".
 */
export async function bootInstance({ name, admin, env = {}, engines = [] }) {
  const mode = engines.length ? String(process.env.SHOTS_ENGINES || 'auto').toLowerCase() : 'host';
  if (!['auto', 'host', 'container'].includes(mode)) {
    throw new Error(`SHOTS_ENGINES=${mode}: expected auto, host or container`);
  }
  if (mode !== 'container') {
    const inst = await bootOnHost({ name, admin, env });
    if (!engines.length) return inst;
    const missing = await missingEngines(inst.url, admin, engines);
    if (!missing.length) {
      log(`this host has every engine the picture shows (${engines.join(', ')})`);
      return inst;
    }
    await inst.stop();
    if (mode === 'host') {
      throw new Error(`SHOTS_ENGINES=host, and this host lacks ${missing.join(', ')} — the picture would say "Not installed on this server"`);
    }
    log(`this host lacks ${missing.join(', ')}: the picture would say "Not installed on this server" — running the same build inside the full image instead`);
  }
  const inst = await bootInContainer({ name, admin, env });
  const missing = await missingEngines(inst.url, admin, engines);
  if (missing.length) {
    await inst.stop();
    throw new Error(`${inst.image} lacks ${missing.join(', ')} too — the picture would say "Not installed on this server"`);
  }
  log(`the container has every engine the picture shows (${engines.join(', ')})`);
  return inst;
}

/** Which of `engines` the running instance reports it did NOT find. */
async function missingEngines(url, admin, engines) {
  const api = client(url);
  await api.login(admin.email, admin.password);
  const { runtime = {} } = await api.json('/api/admin/app-plugins');
  const found = runtime.engines ?? {};
  return engines.filter((e) => !found[e]);
}

/**
 * A LINUX build of this tree, for the container: the one `pnpm shots` handed
 * over (SHOTS_LINUX_BIN), FILEX_BIN itself on a Linux host of the right
 * architecture, else built now — never the image's own filex, which is the last
 * release rather than this tree.
 */
function linuxBinary(arch, dir) {
  if (process.env.SHOTS_LINUX_BIN) return process.env.SHOTS_LINUX_BIN;
  const goarch = arch === 'arm64' ? 'arm64' : 'amd64';
  const hostArch = process.arch === 'x64' ? 'amd64' : process.arch;
  if (process.env.FILEX_BIN && process.platform === 'linux' && hostArch === goarch) return process.env.FILEX_BIN;
  const out = join(dir, 'filex-linux');
  log(`building this tree for linux/${goarch}, for the container`);
  goBuild({ cwd: join(REPO, 'backend'), pkg: './cmd/filex', out, goos: 'linux', goarch, ldflags: SHOTS_LDFLAGS, log });
  return out;
}

/**
 * The same instance as bootOnHost, run inside the full image (ENGINES_IMAGE,
 * override with SHOTS_ENGINES_IMAGE) so the conversion engines are on its
 * PATH — without installing anything on this machine.
 *
 * ⚠ The data directory stays INSIDE the container. On Docker Desktop a bind
 * mount is a network filesystem to the Linux side, and SQLite's locking on one
 * is exactly the kind of fault that shows up as a flaky picture; nothing here
 * needs the files on the host anyway — a scene seeds its storage through the
 * upload API (uploadTree), as a person would.
 *
 * ⚠ Before a picture is taken the container's UI is compared byte for byte
 * with web/dist (scripts/check-embed.mjs → compareServedUI) — the same check
 * `pnpm shots` runs on the host binary, because a second binary is a second
 * chance to photograph an interface that no longer exists.
 */
async function bootInContainer({ name, admin, env }) {
  const docker = dockerInfo();
  if (!docker.ok) {
    throw new Error(
      `this picture needs conversion engines this host has not got, and Docker cannot run the full image instead (${docker.why}). ` +
        'Start Docker, or install the engines and set SHOTS_ENGINES=host.',
    );
  }
  const image = process.env.SHOTS_ENGINES_IMAGE || ENGINES_IMAGE;
  ensureImage(image, log);
  const port = Number(process.env.SHOTS_PORT) || (await freePort());
  const url = `http://127.0.0.1:${port}`;
  const work = mkdtempSync(join(tmpdir(), `filex-shots-${name}-container-`));
  const binary = linuxBinary(docker.arch, work);
  const cname = `filex-shots-${name}-${randomBytes(4).toString('hex')}`;
  let gone = false;
  const remove = () => {
    if (gone) return;
    gone = true;
    removeContainer(cname);
    rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 });
  };
  // A scene that throws exits through process.exit(1): this removes the
  // container on that path too, instead of leaving it holding the port.
  process.once('exit', () => {
    if (!process.env.SHOTS_KEEP) remove();
  });
  startContainer({
    name: cname,
    image,
    port,
    innerPort: 5212,
    marker: process.env[RUN_MARKER] || 'manual',
    binary,
    log,
    env: {
      FILEX_LISTEN: '0.0.0.0:5212',
      FILEX_DATA_DIR: '/shots/data',
      FILEX_ADMIN_EMAIL: admin.email,
      FILEX_ADMIN_PASSWORD: admin.password,
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_PUBLIC_URL: PUBLIC_URL,
      FILEX_SECRET_KEY: `${name}-shots-key-not-a-real-secret`,
      ...env,
    },
  });
  try {
    await waitForHealth(url, null, 90_000);
  } catch (err) {
    const logs = containerLogs(cname);
    remove();
    throw new Error(`${err.message}\n${logs}`);
  }
  const ui = await compareServedUI({ url, binary, log });
  if (!ui.ok) {
    remove();
    throw new Error(`the container's filex does NOT serve the UI in web/dist — refusing to photograph it.\n${ui.report}`);
  }
  return {
    url,
    image,
    container: cname,
    files: '/shots/files',
    storageRoot: (n) => `/shots/files/${n}`,
    async stop() {
      if (process.env.SHOTS_KEEP) {
        log(`container ${cname} left running at ${url} (SHOTS_KEEP=1)`);
        return;
      }
      remove();
    },
  };
}

async function bootOnHost({ name, admin, env }) {
  const bin = process.env.FILEX_BIN ?? defaultBin();
  if (!bin) throw new Error('no filex binary — run `pnpm run build:all` or set FILEX_BIN');
  // SHOTS_PORT is what `pnpm shots` hands each script: a port it just proved
  // free. Run by hand, a script finds its own.
  const port = Number(process.env.SHOTS_PORT) || (await freePort());
  const url = `http://127.0.0.1:${port}`;
  const data = mkdtempSync(join(tmpdir(), `filex-shots-${name}-`));
  const inherited = Object.fromEntries(Object.entries(process.env).filter(([k]) => !/^FILEX_/i.test(k)));
  log(`booting ${bin} on :${port} (${name})`);
  const proc = spawn(bin, ['serve'], {
    env: {
      ...inherited,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_DATA_DIR: join(data, 'data'),
      FILEX_ADMIN_EMAIL: admin.email,
      FILEX_ADMIN_PASSWORD: admin.password,
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_PUBLIC_URL: PUBLIC_URL,
      // Seals share PINs, app settings and the signing authority; without it
      // those features answer "unavailable", which is not what a picture of
      // them should show.
      FILEX_SECRET_KEY: `${name}-shots-key-not-a-real-secret`,
      ...env,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  proc.stdout.on('data', (d) => process.env.SHOTS_VERBOSE && process.stdout.write(d));
  proc.stderr.on('data', (d) => process.env.SHOTS_VERBOSE && process.stderr.write(d));
  await waitForHealth(url, proc);
  const files = join(data, 'files');
  mkdirSync(files, { recursive: true });
  return {
    url,
    /** A directory the scene may use as a storage root (inside the run's data). */
    files,
    storageRoot: (n) => join(files, n),
    container: null,
    async stop() {
      if (process.env.SHOTS_KEEP) {
        log(`instance left running at ${url} (SHOTS_KEEP=1)`);
        return;
      }
      proc.kill();
      for (let i = 0; i < 50 && proc.exitCode === null; i++) await sleep(100);
      rmSync(data, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 });
    },
  };
}

// ── the API, as a signed-in person ────────────────────────────────────────

/**
 * A client bound to one instance and one person. `login()` keeps the bearer
 * token; every call after it is made as that person.
 */
export function client(url) {
  let token = null;
  async function call(path, init = {}) {
    const isForm = init.body instanceof FormData;
    return fetch(`${url}${path}`, {
      ...init,
      headers: {
        ...(init.body && !isForm ? { 'Content-Type': 'application/json' } : {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(init.headers ?? {}),
      },
    });
  }
  async function json(path, init = {}) {
    const res = await call(path, init);
    const text = await res.text();
    let body;
    try {
      body = text ? JSON.parse(text) : {};
    } catch {
      body = text;
    }
    if (!res.ok) throw new Error(`${init.method ?? 'GET'} ${path}: ${res.status} ${text.slice(0, 400)}`);
    return body;
  }
  return {
    call,
    json,
    post: (path, body) => json(path, { method: 'POST', body: JSON.stringify(body ?? {}) }),
    patch: (path, body) => json(path, { method: 'PATCH', body: JSON.stringify(body ?? {}) }),
    async login(email, password) {
      token = null;
      const body = await json('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) });
      token = body.token;
      return token;
    },
  };
}

/**
 * A local storage rooted at `root`, visible under `name`.
 *
 * ⚠ `onHost: false` when `root` is a path inside a container
 * (`inst.storageRoot()` of a container instance): creating it here would make
 * `C:\shots\files\…` on the workstation. The local driver creates a missing
 * root itself.
 */
export async function addLocalStorage(admin, name, root, { onHost = true } = {}) {
  if (onHost) mkdirSync(root, { recursive: true });
  return admin.post('/api/admin/storages', {
    name,
    driver: 'local',
    mount_path: root,
    // ⚠ `config.path` is what the local driver reads (e2e/helpers/seed.ts).
    config: { path: root },
    sync_mode: 'ondemand',
    sync_interval_s: 0,
    enabled: true,
    read_only: false,
  });
}

/**
 * Copies a local directory tree INTO a storage through filex's own upload,
 * folder by folder. ⚠ Through filex rather than onto the disk: thumbnails are
 * rendered by the upload pipeline, so files dropped straight into a storage
 * root show as generic icons until somebody runs `filex thumb backfill`.
 */
export async function uploadTree(api, dest, localDir) {
  const entries = readdirSync(localDir, { withFileTypes: true });
  const files = entries.filter((e) => e.isFile());
  if (files.length) {
    const form = new FormData();
    form.append('path', dest);
    for (const f of files) form.append('file[]', new Blob([readFileSync(join(localDir, f.name))]), f.name);
    const res = await api.call('/api/files/manager?action=upload', { method: 'POST', body: form });
    if (!res.ok) throw new Error(`upload into ${dest}: ${res.status} ${(await res.text()).slice(0, 300)}`);
  }
  for (const d of entries.filter((e) => e.isDirectory())) {
    await api.post('/api/files/manager?action=newfolder', { path: dest, name: d.name });
    await uploadTree(api, `${dest.replace(/\/$/, '')}/${d.name}`, join(localDir, d.name));
  }
}

/** Waits until at least `min` files of a folder have their thumbnails. */
export async function waitForThumbs(api, folder, min, { timeoutMs = 60_000 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const body = await api.json(`/api/files/manager?action=index&path=${encodeURIComponent(folder)}`);
    const ready = (body.files ?? []).filter((f) => f.type === 'file' && f.thumb_url).length;
    if (ready >= min) return;
    await sleep(500);
  }
  throw new Error(`the thumbnails of ${folder} were not ready within ${timeoutMs / 1000}s — the picture would show icons`);
}

/** Lists a folder once so its rows exist in the catalogue before a browser asks. */
export async function index(api, path) {
  return api.json(`/api/files/manager?action=index&path=${encodeURIComponent(path)}`);
}

// ── the apps ──────────────────────────────────────────────────────────────

/**
 * Finds an app's build where the e2e suite finds it — one table for both,
 * `e2e/helpers/app-locations.mjs` — and FAILS when it is not there: a missing
 * build must not become a skipped picture, because a skipped picture keeps the
 * old one in the README.
 *
 * ⚠ An explicit FILEX_<APP>_APP_DIR is authoritative there; `pnpm shots`
 * passes these two variables through to the scripts and no other FILEX_*
 * (scripts/shots.mjs → scriptEnv).
 */
export function findApp(name) {
  const found = locateApp(name);
  if (!found.present) {
    const spot = APP_LOCATIONS[name];
    throw new Error(
      `the ${name} app is not built: ${found.how}. Build ${spot?.repo ?? name}, ` +
        `or point ${spot?.env ?? 'its FILEX_*_APP_DIR'} at a directory holding plugin.wasm + filex-app.json.`,
    );
  }
  return { name, wasm: found.wasm, manifestPath: found.manifestPath, manifest: found.manifest };
}

/**
 * Installs an app through the admin API with exactly the manifest's
 * permissions granted — the same request the wizard's "Install" sends after
 * the review. A script that photographs the wizard itself drives the wizard;
 * every other script needs the app present and uses this.
 */
export async function installApp(admin, app) {
  const fd = new FormData();
  fd.append('grant', JSON.stringify({ permissions: app.manifest.permissions }));
  fd.append('wasm', new Blob([readFileSync(app.wasm)]), 'plugin.wasm');
  fd.append('manifest', new Blob([readFileSync(app.manifestPath)]), 'filex-app.json');
  const res = await admin.call('/api/admin/app-plugins', { method: 'POST', body: fd });
  if (!res.ok) throw new Error(`install ${app.name}: ${res.status} ${(await res.text()).slice(0, 400)}`);
  return res.json();
}

// ── the browser ───────────────────────────────────────────────────────────

/**
 * A browser context pinned to English three ways over — the browser locale,
 * the stored preference and (on the instance) the server default. Getting a
 * half-Turkish dialog into the repo took one of those being unset.
 */
export async function newContext(browser, { scheme = 'light', width = 1440, height = 900 } = {}) {
  const ctx = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 2,
    locale: 'en-US',
    colorScheme: scheme,
    /* ⚠⚠ NO SERVICE WORKER. The admin UI is a PWA: once its worker is
       registered it answers from its own cache, and a picture then shows the
       bundle of an EARLIER build — the pack agent's first look at Connections
       had the Storages tab that this release removed, which reads exactly
       like a regression (v0.43.0). A screenshot must be of the build in this
       tree, so the worker is blocked outright rather than reloaded around. */
    serviceWorkers: 'block',
  });
  await ctx.addInitScript(() => {
    try {
      localStorage.setItem('filex.locale', 'en');
      // ⚠ The exact key the explorer checks (FileExplorer.vue → TOUR_LS_KEY):
      // the tour's backdrop swallows every click a scene makes.
      localStorage.setItem('filex.tourDone', '1');
      // The desktop-app promo banner parks itself across the bottom otherwise.
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    } catch {
      /* storage blocked — the defaults will show */
    }
  });
  return ctx;
}

/**
 * Signs in through the form and waits for a LANDING route: an administrator
 * lands on /admin/home, anybody else on /drive/…. ⚠ The login page's own URL
 * already contains "/admin/", so waiting for that resolves instantly and every
 * later step runs signed out.
 */
export async function signIn(page, url, { email, password }) {
  await page.goto(`${url}/admin/login`);
  await page.fill('#email', email);
  await page.fill('#password', password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/(admin\/(home|dashboard|explore)|drive\/)/, { timeout: 20_000 });
}

/**
 * Writes one picture into this release's folder for `set` (docs/screenshots/
 * <release>/<set>/<file>). A Page is shot as the viewport; a Locator as that
 * element, after it is visible.
 *
 * `SHOTS_DRY_RUN=1` walks a scene all the way to each picture — every wait,
 * every guard before it — and writes nothing: the target must still be
 * visible, and its first words are logged instead, so a rehearsal proves what
 * the picture WOULD show without replacing a picture nobody has approved.
 * (Used 2026-09-21 to rehearse the converter scene in the container before
 * the owner had signed off the release's test.)
 */
export async function shot(target, set, file) {
  if (process.env.SHOTS_DRY_RUN) {
    const el = typeof target.waitFor === 'function' ? target : target.locator('body');
    await el.waitFor({ state: 'visible', timeout: 15_000 });
    const text = (await el.innerText()).replace(/\s+/g, ' ').trim();
    log(`dry run — would write ${set}/${file}: "${text.slice(0, 600)}${text.length > 600 ? '…' : ''}"`);
    return;
  }
  const out = process.env.SHOTS_OUT ?? shotsDir(set);
  mkdirSync(out, { recursive: true });
  await target.screenshot({ path: join(out, file) });
  log(`wrote ${set}/${file}`);
}

// ── a document worth signing ──────────────────────────────────────────────

const pdfEscape = (s) => s.replace(/[\\()]/g, (c) => `\\${c}`);

/**
 * A one-page A4 PDF that reads like a real agreement — a heading, parties,
 * numbered clauses and a signature block — written by hand so the scenes do
 * not depend on a PDF producer being installed. `lines` are `[size, text]`
 * pairs (size 0 = a blank line); bold for size >= 14.
 */
export function documentPDF(lines) {
  let y = 780;
  let stream = '';
  for (const [size, text] of lines) {
    if (!size) {
      y -= 12;
      continue;
    }
    const font = size >= 14 ? 'F2' : 'F1';
    stream += `BT /${font} ${size} Tf 72 ${y} Td (${pdfEscape(text)}) Tj ET\n`;
    y -= Math.round(size * 1.6);
  }
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R /F2 5 0 R >> >> /Contents 6 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>',
    `<< /Length ${Buffer.byteLength(stream, 'latin1')} >>\nstream\n${stream}endstream`,
  ];
  let body = '%PDF-1.4\n';
  const offsets = [];
  objects.forEach((o, i) => {
    offsets.push(Buffer.byteLength(body, 'latin1'));
    body += `${i + 1} 0 obj\n${o}\nendobj\n`;
  });
  const xref = Buffer.byteLength(body, 'latin1');
  body += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const off of offsets) body += `${String(off).padStart(10, '0')} 00000 n \n`;
  body += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(body, 'latin1');
}

/** The agreement the signing scenes send round. */
export const AGREEMENT = [
  [20, 'Service Agreement'],
  [10, 'Agreement no. 2026-114 · Effective 1 October 2026'],
  [0],
  [11, 'This agreement is made between Northwind Studio Ltd. ("the Client") and'],
  [11, 'Contoso Design Co. ("the Provider").'],
  [0],
  [13, '1. Services'],
  [11, 'The Provider will design and deliver the brand identity described in Annex A,'],
  [11, 'in three milestones, each reviewed and accepted by the Client in writing.'],
  [0],
  [13, '2. Fees and payment'],
  [11, 'The Client will pay EUR 18,400 in three equal instalments, each due within'],
  [11, '30 days of the milestone it covers being accepted.'],
  [0],
  [13, '3. Confidentiality'],
  [11, 'Each party keeps the other party\'s confidential information private during'],
  [11, 'this agreement and for two years after it ends.'],
  [0],
  [13, '4. Term'],
  [11, 'This agreement runs until the final milestone is accepted, and either party'],
  [11, 'may end it with 30 days\' written notice.'],
  [0],
  [0],
  [13, 'Signatures'],
  [0],
  [11, 'For the Client                                   For the Provider'],
  [0],
  [0],
  [0],
  [11, '______________________________          ______________________________'],
  [10, 'Name, date                                                 Name, date'],
];
