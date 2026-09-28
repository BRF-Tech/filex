// Does this filex binary (or image) actually work on this machine?
//
//   node smoke-cli.mjs --binary <filex[.exe]> [--expect-arch arm64] [--expect-version 0.48.1]
//   node smoke-cli.mjs --image <docker image>  [--expect-arch arm64] [--expect-version 0.48.1]
//
// Boots a throwaway server — the binary's own `filex serve`, or the image in a
// container — and drives it with the same binary's CLI:
//
//   --version · /healthz · `client login` · `client ls` (storages) · `mkdir` ·
//   `upload` (1 MiB of random bytes, and a file with a non-ASCII name) · `ls` ·
//   `download` + byte-for-byte compare · `mv` · `rm` · `ls` again.
//
// With --image, the CLI is the `filex` copied OUT of the image, so the image's
// own binary is what serves and what talks to it.
//
// ⚠ Why: a release built arm64 binaries and arm64 images for months and nothing
// ever RAN one — a cross-compiled binary that starts is a claim, not a fact
// (CGO off, modernc sqlite, but a dependency that panics on arm64 at init
// would still ship). This runs on a runner of the binary's own architecture,
// so it measures what a user on that machine gets.
//
// Exit 0 = every step passed. Anything else prints the step, what came back
// and the tail of the server's log.
import { spawn, spawnSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { archOf, normalizeArch } from './arch-of.mjs';

const EMAIL = 'smoke@example.com';
const PASSWORD = 'smoke-test-password-4471';
const STORAGE = 'smoke';

function args(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith('--')) throw new Error(`unexpected argument ${argv[i]}`);
    out[argv[i].slice(2)] = argv[++i];
  }
  return out;
}

const freePort = () =>
  new Promise((resolve, reject) => {
    const s = net.createServer();
    s.once('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const sha = (buf) => crypto.createHash('sha256').update(buf).digest('hex');

let step = 0;
const results = [];
function ok(name, detail = '') {
  step++;
  results.push({ name, ok: true });
  console.log(`  ok ${String(step).padStart(2)}  ${name}${detail ? `  (${detail})` : ''}`);
}
class SmokeError extends Error {}
function fail(name, detail) {
  step++;
  results.push({ name, ok: false });
  console.log(`FAIL ${String(step).padStart(2)}  ${name}`);
  throw new SmokeError(`${name}: ${detail}`);
}

async function main() {
  const a = args(process.argv.slice(2));
  if (!a.binary === !a.image) throw new Error('give exactly one of --binary <file> or --image <docker image>');
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-smoke-'));
  const home = path.join(work, 'home');
  fs.mkdirSync(home, { recursive: true });
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  let bin = a.binary ? path.resolve(a.binary) : null;
  let serverLog = () => '';
  let stop = async () => {};

  try {
    // ── the binary ─────────────────────────────────────────────────────
    if (a.image) {
      const cid = spawnSync('docker', ['create', a.image], { encoding: 'utf8' });
      if (cid.status !== 0) fail('copy the CLI out of the image', cid.stderr);
      bin = path.join(work, process.platform === 'win32' ? 'filex.exe' : 'filex');
      const cp = spawnSync('docker', ['cp', `${cid.stdout.trim()}:/usr/local/bin/filex`, bin], { encoding: 'utf8' });
      spawnSync('docker', ['rm', cid.stdout.trim()]);
      if (cp.status !== 0) fail('copy the CLI out of the image', cp.stderr);
      fs.chmodSync(bin, 0o755);
    }
    if (!fs.existsSync(bin)) fail('the binary exists', bin);
    const arch = archOf(bin);
    if (a['expect-arch'] && arch !== normalizeArch(a['expect-arch'])) fail(`the binary is ${a['expect-arch']}`, `it is ${arch}`);
    ok('binary architecture', arch);

    const env = { ...process.env, HOME: home, USERPROFILE: home, FILEX_URL: '', FILEX_TOKEN: '' };
    const cli = (argv, input) => {
      const r = spawnSync(bin, argv, { env, input, encoding: 'utf8', timeout: 120_000 });
      return { code: r.status, out: `${r.stdout ?? ''}`, err: `${r.stderr ?? ''}${r.error ? String(r.error) : ''}` };
    };

    const v = cli(['--version']);
    if (v.code !== 0) fail('filex --version', `${v.code}: ${v.err}`);
    if (a['expect-version'] && !v.out.includes(a['expect-version'])) fail(`--version names ${a['expect-version']}`, v.out.trim());
    ok('filex --version', v.out.trim());

    // ── the server ─────────────────────────────────────────────────────
    const seed = {
      FILEX_ADMIN_EMAIL: EMAIL,
      FILEX_ADMIN_PASSWORD: PASSWORD,
      FILEX_DEFAULT_STORAGE_DRIVER: 'local',
      FILEX_DEFAULT_STORAGE_NAME: STORAGE,
      FILEX_PUBLIC_URL: url,
    };
    if (a.image) {
      const name = `filex-smoke-${port}`;
      const run = spawnSync('docker', [
        'run', '-d', '--name', name, '-p', `127.0.0.1:${port}:5212`,
        ...Object.entries({ ...seed, FILEX_DEFAULT_STORAGE_PATH: '/tmp' }).flatMap(([k, val]) => ['-e', `${k}=${val}`]),
        a.image,
      ], { encoding: 'utf8' });
      if (run.status !== 0) fail('docker run', run.stderr);
      serverLog = () => spawnSync('docker', ['logs', '--tail', '60', name], { encoding: 'utf8' }).stdout ?? '';
      stop = async () => void spawnSync('docker', ['rm', '-f', name]);
      ok('container started', a.image);
    } else {
      const files = path.join(work, 'files');
      fs.mkdirSync(files, { recursive: true });
      const logFile = path.join(work, 'server.log');
      const fd = fs.openSync(logFile, 'a');
      const child = spawn(bin, ['serve'], {
        env: { ...env, ...seed, FILEX_LISTEN: `127.0.0.1:${port}`, FILEX_DATA_DIR: path.join(work, 'data'), FILEX_DEFAULT_STORAGE_PATH: files },
        stdio: ['ignore', fd, fd],
      });
      serverLog = () => fs.readFileSync(logFile, 'utf8').split('\n').slice(-60).join('\n');
      stop = () =>
        new Promise((resolve) => {
          if (child.exitCode !== null) return resolve();
          child.once('exit', resolve);
          child.kill();
          setTimeout(resolve, 5000);
        });
      ok('filex serve started', `pid ${child.pid}`);
    }

    let healthy = false;
    for (let i = 0; i < 180 && !healthy; i++) {
      try {
        const r = await fetch(`${url}/healthz`);
        healthy = r.ok;
      } catch {
        /* not listening yet */
      }
      if (!healthy) await sleep(500);
    }
    if (!healthy) fail('/healthz answers 200', `nothing within 90 s on ${url}`);
    ok('/healthz answers 200', url);

    // ── the CLI against it ─────────────────────────────────────────────
    const login = cli(['client', 'login', '--url', url, '--email', EMAIL], `${PASSWORD}\n`);
    if (login.code !== 0) fail('client login', `${login.code}: ${login.err}${login.out}`);
    ok('client login');

    const storages = cli(['client', 'ls']);
    const names = storages.out.split(/\r?\n/).map((l) => l.trim().replace(/:\/\/$/, '')).filter(Boolean);
    if (storages.code !== 0 || !names.length) fail('client ls lists the storages', `${storages.code}: ${storages.err}${storages.out}`);
    const adapter = names.includes(STORAGE) ? STORAGE : names[0];
    ok('client ls lists the storages', names.join(', '));
    const remote = (p) => `${adapter}://${p}`;

    const mk = cli(['client', 'mkdir', remote('smoke-dir')]);
    if (mk.code !== 0) fail('client mkdir', `${mk.code}: ${mk.err}`);
    ok('client mkdir');

    const payloads = [
      { name: 'random.bin', bytes: crypto.randomBytes(1024 * 1024) },
      { name: 'Türkçe ağaç ✓.txt', bytes: Buffer.from('filex smoke — ğüşıöç\n'.repeat(64), 'utf8') },
    ];
    for (const p of payloads) {
      const local = path.join(work, p.name);
      fs.writeFileSync(local, p.bytes);
      const up = cli(['client', 'upload', local, remote(`smoke-dir/${p.name}`)]);
      if (up.code !== 0) fail(`client upload ${p.name}`, `${up.code}: ${up.err}${up.out}`);
      ok(`client upload ${p.name}`, `${p.bytes.length} bytes`);
    }

    const ls = cli(['client', 'ls', remote('smoke-dir'), '--json']);
    for (const p of payloads) if (ls.code !== 0 || !ls.out.includes(JSON.stringify(p.name).slice(1, -1))) fail(`client ls shows ${p.name}`, `${ls.code}: ${ls.err}${ls.out.slice(0, 400)}`);
    ok('client ls shows both files');

    for (const p of payloads) {
      const target = path.join(work, `down-${crypto.randomUUID()}`);
      const down = cli(['client', 'download', remote(`smoke-dir/${p.name}`), target]);
      if (down.code !== 0) fail(`client download ${p.name}`, `${down.code}: ${down.err}${down.out}`);
      const got = fs.statSync(target).isDirectory() ? fs.readFileSync(path.join(target, p.name)) : fs.readFileSync(target);
      if (sha(got) !== sha(p.bytes)) fail(`${p.name} comes back byte for byte`, `sent ${p.bytes.length} bytes, got ${got.length}`);
      ok(`client download ${p.name}, byte for byte`);
    }

    const mv = cli(['client', 'mv', remote('smoke-dir/random.bin'), remote('smoke-dir/renamed.bin')]);
    if (mv.code !== 0) fail('client mv', `${mv.code}: ${mv.err}`);
    ok('client mv');

    const rm = cli(['client', 'rm', remote('smoke-dir/renamed.bin'), remote(`smoke-dir/${payloads[1].name}`)]);
    if (rm.code !== 0) fail('client rm', `${rm.code}: ${rm.err}`);
    const after = cli(['client', 'ls', remote('smoke-dir'), '--json']);
    if (after.code !== 0 || after.out.includes('renamed.bin') || after.out.includes('random.bin')) fail('the folder is empty after rm', after.out.slice(0, 400));
    ok('client rm, and the folder is empty after');

    console.log(`\nsmoke: ${results.length} steps passed (${arch}, ${a.image ?? path.basename(bin)})`);
    return 0;
  } catch (e) {
    if (!(e instanceof SmokeError)) throw e;
    console.error(`\n${e.message}\n\n── server log (tail) ──\n${serverLog()}`);
    return 1;
  } finally {
    await stop();
    fs.rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 500 });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then(
    (code) => process.exit(code),
    (e) => {
      console.error(String(e?.stack ?? e));
      process.exit(2);
    },
  );
}
