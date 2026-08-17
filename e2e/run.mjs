#!/usr/bin/env node
/**
 * e2e/run.mjs — the one entry point for filex's end-to-end suite.
 *
 * Two profiles, deliberately kept apart:
 *
 *   local        Hermetic. Starts a filex binary on a free port against a
 *                throwaway data dir with a deterministic admin, waits for
 *                /healthz, runs every spec except the deployment smoke, and
 *                tears the whole thing down again. This is the release gate.
 *
 *   deployment   Read-only smoke against a URL that is already live. Never
 *                run as part of a build check.
 *
 * Mixing the two is what made the old setup unable to answer the only
 * question that matters before a release — "is this build good?" — because a
 * local run could go red merely because production was slow. Keeping them
 * separate is the point of this file.
 *
 * Usage:
 *   node e2e/run.mjs local
 *   node e2e/run.mjs local --s3                 # + a MinIO container and an s3 storage
 *   node e2e/run.mjs local --binary ../bin/filex.exe --keep
 *   node e2e/run.mjs deployment --url https://fm.example.com
 *
 * Exit code is the suite's. A profile that cannot set up what it promised
 * fails loudly; it never quietly runs a smaller suite than you asked for.
 */

import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const E2E_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(E2E_DIR, '..');

const ADMIN_EMAIL = 'admin@local';
const ADMIN_PASSWORD = 'admin';

// ── argv ────────────────────────────────────────────────────────────────────

const argv = process.argv.slice(2);
const profile = argv[0];
const flag = (name) => argv.includes(`--${name}`);
const value = (name, fallback = undefined) => {
  const i = argv.indexOf(`--${name}`);
  return i >= 0 && argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[i + 1] : fallback;
};

if (profile !== 'local' && profile !== 'deployment') {
  console.error('usage: node e2e/run.mjs <local|deployment> [options]\n');
  console.error('  local       hermetic run against a binary this script starts');
  console.error('    --binary <path>   filex binary (default: bin/filex[.exe], or --build)');
  console.error('    --build           build the frontend + binary first');
  console.error('    --s3              also start MinIO and register an s3 storage');
  console.error('    --port <n>        server port (default: a free one)');
  console.error('    --keep            leave the server (and data dir) running afterwards');
  console.error('    --grep <pattern>  pass through to playwright');
  console.error('');
  console.error('  deployment  read-only smoke against a live URL');
  console.error('    --url <url>       required, e.g. https://fm.example.com');
  process.exit(2);
}

const cleanups = [];
let cleaning = false;
async function cleanup(why) {
  if (cleaning) return;
  cleaning = true;
  for (const fn of cleanups.reverse()) {
    try {
      await fn();
    } catch (err) {
      console.error(`[e2e] cleanup (${why}) failed: ${err.message}`);
    }
  }
}
for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, async () => {
    await cleanup(sig);
    process.exit(130);
  });
}

// ── helpers ─────────────────────────────────────────────────────────────────

const log = (msg) => console.log(`[e2e] ${msg}`);

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

async function waitFor(url, what, timeoutMs = 90_000) {
  const deadline = Date.now() + timeoutMs;
  let lastErr = 'no attempt made';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(3_000) });
      if (res.ok) return;
      lastErr = `HTTP ${res.status}`;
    } catch (err) {
      lastErr = err.message;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`${what} never became ready at ${url} (${timeoutMs}ms): ${lastErr}`);
}

function run(cmd, args, opts = {}) {
  const res = spawnSync(cmd, args, { stdio: 'inherit', shell: process.platform === 'win32', ...opts });
  if (res.error) throw res.error;
  if (res.status !== 0) throw new Error(`${cmd} ${args.join(' ')} exited ${res.status}`);
}

function tryRun(cmd, args) {
  const res = spawnSync(cmd, args, { stdio: 'ignore', shell: process.platform === 'win32' });
  return res.status === 0;
}

/** Every spec except the deployment smoke — that one is the other profile. */
function localSpecs() {
  return fs
    .readdirSync(path.join(E2E_DIR, 'tests'))
    .filter((f) => f.endsWith('.spec.ts') && !f.startsWith('90-deployment-smoke'))
    .sort();
}

/**
 * Run Playwright from THIS repo's install, never `npx`.
 *
 * ⚠ `npx playwright test` looks fine and is not. When the workspace install is
 * incomplete, npx quietly downloads its own copy — measured here: the project
 * pins 1.59.1 and npx fetched **1.62.1**, then failed to resolve
 * `@playwright/test` because the config imports the local one. A test runner
 * that silently swaps its own version is a suite you cannot reason about, and
 * the failure it produces points at the config rather than at the install.
 *
 * So: resolve the binary, and if it is not there, say what to run. A loud
 * failure beats a run on an unknown version.
 */
function playwright(specs, env) {
  const bin = path.join(
    E2E_DIR,
    'node_modules',
    '.bin',
    process.platform === 'win32' ? 'playwright.cmd' : 'playwright',
  );
  if (!fs.existsSync(bin)) {
    throw new Error(
      `Playwright is not installed in ${E2E_DIR}.\n` +
        `Run:  cd ${REPO} && pnpm install --force\n` +
        `(a plain \`pnpm install\` reports "Already up to date" when the store ` +
        `entry is missing — it validates the lockfile, not the files on disk).`,
    );
  }
  const args = ['test', ...specs, '--reporter=list'];
  const grep = value('grep');
  if (grep) args.push('--grep', grep);

  // Windows needs a shell to run playwright.cmd, and a shell re-parses the
  // argument list. `--grep "a|b"` then loses its quotes and the `|` becomes a
  // pipe: measured here as `'locale' is not recognized as an internal or
  // external command`, which reads like a broken install rather than a quoting
  // bug. Quote anything that is not plainly safe.
  const useShell = process.platform === 'win32';
  const shellSafe = (a) => (/^[\w.:/\\=-]+$/.test(a) ? a : `"${a.replace(/(["\\])/g, '\\$1')}"`);
  const res = spawnSync(bin, useShell ? args.map(shellSafe) : args, {
    cwd: E2E_DIR,
    stdio: 'inherit',
    shell: useShell,
    env: { ...process.env, ...env },
  });
  return res.status ?? 1;
}

// ── local profile ───────────────────────────────────────────────────────────

function resolveBinary() {
  const explicit = value('binary');
  if (explicit) {
    const p = path.resolve(process.cwd(), explicit);
    if (!fs.existsSync(p)) throw new Error(`--binary ${p} does not exist`);
    return p;
  }
  const name = process.platform === 'win32' ? 'filex.exe' : 'filex';
  const p = path.join(REPO, 'bin', name);
  if (!fs.existsSync(p)) {
    throw new Error(
      `no binary at ${p}. Build one with --build, or point at yours with --binary <path>.`,
    );
  }
  return p;
}

function build() {
  log('building packages, web, embed assets and the binary…');
  run('pnpm', ['-r', '--filter', './packages/*', 'build'], { cwd: REPO });
  run('pnpm', ['--filter', './web', 'build'], { cwd: REPO });
  run('node', ['scripts/sync-embed.mjs'], { cwd: REPO });
  const out = path.join(REPO, 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex');
  fs.mkdirSync(path.dirname(out), { recursive: true });
  run('go', ['build', '-o', out, './cmd/filex'], { cwd: path.join(REPO, 'backend') });
  return out;
}

async function startServer(binary) {
  const port = Number(value('port')) || (await freePort());
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-'));
  // 127.0.0.1, never "localhost": on Windows localhost resolves to ::1 first,
  // and a server bound to 127.0.0.1 answers that with ECONNREFUSED. The health
  // probe and Playwright both hit this, and it looks exactly like a server that
  // failed to start.
  const baseURL = `http://127.0.0.1:${port}`;

  log(`starting ${path.basename(binary)} on ${baseURL}`);
  log(`data dir ${dataDir}`);

  const logFile = path.join(dataDir, 'server.log');

  // ⚠ The server's stdout/stderr go STRAIGHT to a file descriptor — never
  // through a Node pipe.
  //
  // The obvious version of this (`stdio: ['ignore','pipe','pipe']` plus
  // `child.stdout.pipe(createWriteStream(...))`) wedges the server it just
  // started, and it took a full red suite to notice. Draining a pipe needs
  // Node's event loop, but we run Playwright with `spawnSync`, which blocks
  // that loop for the entire suite. So nothing drains: the OS pipe buffer
  // (64 KiB on Windows) fills up, and filex — which logs one line per HTTP
  // request from inside the request path — blocks forever in write(2). Every
  // handler stops, /healthz included.
  //
  // Measured: the server answered 551 requests and then went dead to
  // everything for the rest of the run, which reads exactly like a product
  // deadlock. Handing the child a real fd takes Node out of the path.
  const logFd = fs.openSync(logFile, 'a');

  const child = spawn(binary, ['serve'], {
    env: {
      ...process.env,
      FILEX_DATA_DIR: dataDir,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_PUBLIC_URL: baseURL,
      FILEX_ADMIN_EMAIL: ADMIN_EMAIL,
      FILEX_ADMIN_PASSWORD: ADMIN_PASSWORD,
      // The prepared-copy cache normally starts at 64 MiB. 86-slow-storage-cache
      // needs a file over the threshold; moving the threshold costs 3 MiB per
      // case instead of 200. It changes nothing for the other specs — a storage
      // still has to be marked (or measured) slow before anything is prepared.
      FILEX_CACHE_MIN_SIZE: '1048576',
      // ⚠ Required for S3 access keys. Unlike an API token, an S3 secret has
      // to be recoverable — SigV4 sends a derived HMAC, not the secret — so it
      // is sealed at rest and issuing refuses outright without a key. Without
      // this the connections panel answers "no secret key configured", which is
      // the honest message and also exactly what 25-connections would measure.
      FILEX_SECRET_KEY: 'e2e-secret-key-not-for-production',
      // The SFTP endpoint is off by default (it opens a TCP port of its own),
      // but the connections panel is measured with it ON — a suite that ran
      // against a disabled endpoint would be measuring the "ask your operator"
      // state and calling it a pass.
      FILEX_SFTP: '1',
      FILEX_SFTP_ADDR: '127.0.0.1:0',
      // FTPS too, for the same reason: the connections panel is measured with
      // the endpoint ON, or the suite is measuring the "ask your operator"
      // state and calling it a pass.
      FILEX_FTPS: '1',
      FILEX_FTPS_ADDR: '127.0.0.1:0',
      // NFS too. ⚠ It is off by default in the product because NFSv3 has no
      // transport security; the suite turns it on because the panel is what is
      // being measured, and it binds to loopback only.
      FILEX_NFS: '1',
      FILEX_NFS_ADDR: '127.0.0.1:0',
    },
    stdio: ['ignore', logFd, logFd],
  });

  let exited = null;
  child.on('exit', (code, signal) => {
    exited = signal ? `signal ${signal}` : `code ${code}`;
  });

  cleanups.push(async () => {
    if (flag('keep')) {
      log(`--keep: server still on ${baseURL}, data dir ${dataDir}`);
      return;
    }
    child.kill();
    await new Promise((r) => setTimeout(r, 300));
    try {
      fs.closeSync(logFd);
    } catch {
      /* already gone */
    }
    fs.rmSync(dataDir, { recursive: true, force: true });
  });

  try {
    await waitFor(`${baseURL}/healthz`, 'filex');
  } catch (err) {
    const tail = fs.existsSync(logFile) ? fs.readFileSync(logFile, 'utf8').slice(-2000) : '';
    throw new Error(`${err.message}${exited ? ` (process exited: ${exited})` : ''}\n${tail}`);
  }
  log('server is up');
  return { baseURL, dataDir };
}

/**
 * MinIO in Docker, plus an s3 storage registered through the admin API.
 * The bucket is made by creating a directory inside the container — MinIO
 * treats a top-level directory of its data dir as a bucket, so this needs no
 * extra client and no SigV4 signing here.
 */
async function startS3(baseURL) {
  if (!tryRun('docker', ['version'])) {
    throw new Error('--s3 needs Docker, and `docker version` failed. Not skipping silently.');
  }
  const image = 'minio/minio:latest';
  if (!tryRun('docker', ['image', 'inspect', image])) {
    log(`pulling ${image}…`);
    run('docker', ['pull', image]);
  }

  const port = await freePort();
  const name = `filex-e2e-minio-${port}`;
  const access = 'filexe2e';
  const secret = 'filexe2esecret';
  const bucket = 'filex-e2e';

  run('docker', [
    'run', '-d', '--rm', '--name', name,
    '-p', `${port}:9000`,
    '-e', `MINIO_ROOT_USER=${access}`,
    '-e', `MINIO_ROOT_PASSWORD=${secret}`,
    image, 'server', '/data',
  ]);
  cleanups.push(() => {
    if (flag('keep')) {
      log(`--keep: MinIO still on http://127.0.0.1:${port} (container ${name})`);
      return;
    }
    tryRun('docker', ['rm', '-f', name]);
  });

  await waitFor(`http://127.0.0.1:${port}/minio/health/live`, 'MinIO');
  run('docker', ['exec', name, 'mkdir', '-p', `/data/${bucket}`]);
  log(`MinIO up on http://127.0.0.1:${port}, bucket ${bucket}`);

  // Register the storage the same way a human would: through the admin API.
  const login = await fetch(`${baseURL}/api/auth/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
  });
  if (!login.ok) throw new Error(`admin login failed: ${login.status} ${await login.text()}`);
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');

  const storageName = 's3-e2e';
  const create = await fetch(`${baseURL}/api/admin/storages`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', cookie },
    body: JSON.stringify({
      name: storageName,
      driver: 's3',
      mount_path: '/',
      enabled: true,
      config: {
        endpoint: `http://127.0.0.1:${port}`,
        region: 'us-east-1',
        bucket,
        prefix: 'e2e',
        access_key: access,
        secret_key: secret,
        path_style: true,
      },
    }),
  });
  if (!create.ok) {
    throw new Error(`registering the s3 storage failed: ${create.status} ${await create.text()}`);
  }
  log(`s3 storage "${storageName}" registered`);
  return { storageName, endpoint: `http://127.0.0.1:${port}`, bucket };
}

// ── main ────────────────────────────────────────────────────────────────────

async function main() {
  if (profile === 'deployment') {
    const url = value('url');
    if (!url) throw new Error('deployment profile needs --url (e.g. --url https://fm.example.com)');
    log(`read-only smoke against ${url}`);
    return playwright(['tests/90-deployment-smoke.spec.ts'], { E2E_BASE_URL: url });
  }

  const binary = flag('build') ? build() : resolveBinary();
  const { baseURL, dataDir } = await startServer(binary);
  const storageRootDir = path.join(dataDir, 'storages');
  fs.mkdirSync(storageRootDir, { recursive: true });

  const env = {
    E2E_BASE_URL: baseURL,
    E2E_ADMIN_EMAIL: ADMIN_EMAIL,
    E2E_ADMIN_PASSWORD: ADMIN_PASSWORD,
    // Every storage a spec seeds is rooted under the throwaway data dir, so a
    // run cannot inherit the previous run's files. Specs write POSIX-looking
    // mounts (`/tmp/filex-e2e-files`) and several of them are FIXED names, so
    // without this they resolved to the same directory on every run: a delete
    // would hit a trash sidecar left behind an hour earlier and fail. 211 such
    // directories had piled up in the OS temp dir before this landed.
    E2E_STORAGE_ROOT: storageRootDir,
  };
  if (flag('s3')) {
    const s3 = await startS3(baseURL);
    env.E2E_S3_STORAGE = s3.storageName;
  }

  const specs = localSpecs().map((f) => `tests/${f}`);
  log(`running ${specs.length} spec files`);
  return playwright(specs, env);
}

let code = 1;
try {
  code = await main();
} catch (err) {
  console.error(`[e2e] ${err.message}`);
  code = 1;
} finally {
  await cleanup('exit');
}
process.exit(code);
