/**
 * 01-harness — guards the test harness itself.
 *
 * `e2e/run.mjs` used to attach the filex server's stdout/stderr to a Node
 * pipe (`stdio: [...,'pipe','pipe']` + `child.stdout.pipe(writeStream)`) and
 * then run Playwright with `spawnSync`. Draining a pipe needs Node's event
 * loop; `spawnSync` blocks that loop for the whole suite. So nothing drained,
 * the 64 KiB OS pipe buffer filled, and filex — which logs one line per HTTP
 * request from inside the request path — blocked forever in write(2).
 *
 * Measured before the fix: the server answered 551 requests and then went
 * dead to *everything*, /healthz included, for the remaining 8 minutes of the
 * run. 20 specs failed with connection timeouts that looked exactly like a
 * product deadlock. It is the worst kind of red: the harness breaking the
 * thing it is supposed to be measuring.
 *
 * Two assertions, deliberately:
 *   1. the mechanism — a chatty child on a Node pipe really does stall while
 *      the loop is blocked, and really does not when handed a file
 *      descriptor. Without this, assertion 2 is just string-matching.
 *   2. run.mjs is on the safe side of that mechanism.
 */
import { test, expect } from '@playwright/test';
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const RUN_MJS = path.resolve(HERE, '../run.mjs');

/** Writes 256 KiB to stdout, then touches `sentinel` and exits. */
const CHATTY = (sentinel: string) =>
  `const l='x'.repeat(1023)+'\\n';` +
  `for(let i=0;i<256;i++)process.stdout.write(l);` +
  `process.stdout.write('',()=>{require('fs').writeFileSync(${JSON.stringify(sentinel)},'done');process.exit(0)});`;

/** Blocks this process's event loop for `ms`, the way `spawnSync` does. */
function blockEventLoop(ms: number) {
  spawnSync(process.execPath, ['-e', `Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,${ms})`]);
}

test.describe('Harness — the server must not log into a Node pipe', () => {
  test('a chatty child stalls on a pipe and survives on a file descriptor', async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-harness-'));

    // (a) piped: nothing drains while the loop is blocked → the child stalls.
    const pipedSentinel = path.join(dir, 'piped.done');
    const piped = spawn(process.execPath, ['-e', CHATTY(pipedSentinel)], {
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const pipedSink = fs.createWriteStream(path.join(dir, 'piped.log'));
    piped.stdout.pipe(pipedSink);
    blockEventLoop(3_000);
    const stalled = !fs.existsSync(pipedSentinel);
    piped.kill();
    pipedSink.destroy();

    // (b) same child, same blocked loop, but writing to a real fd.
    const fdSentinel = path.join(dir, 'fd.done');
    const fd = fs.openSync(path.join(dir, 'fd.log'), 'a');
    const direct = spawn(process.execPath, ['-e', CHATTY(fdSentinel)], {
      stdio: ['ignore', fd, fd],
    });
    blockEventLoop(3_000);
    const finished = fs.existsSync(fdSentinel);
    direct.kill();
    fs.closeSync(fd);
    await new Promise((r) => setTimeout(r, 100));
    fs.rmSync(dir, { recursive: true, force: true });

    expect(stalled, 'a piped child should stall while the event loop is blocked').toBe(true);
    expect(finished, 'a child writing to an fd should finish regardless of the loop').toBe(true);
  });

  test('run.mjs hands the server a file descriptor, not a pipe', async () => {
    const src = fs.readFileSync(RUN_MJS, 'utf8');
    const startServer = src
      .slice(src.indexOf('async function startServer'), src.indexOf('async function startS3'))
      // Drop comments — the fix is *documented* in terms of the broken call,
      // and a guard that trips over its own explanation is a bad guard.
      .split('\n')
      .filter((line) => !/^\s*(\/\/|\/\*|\*)/.test(line))
      .join('\n');

    expect(startServer, 'startServer must exist').toContain('spawn(binary');
    expect(startServer).toContain('fs.openSync(logFile');
    expect(startServer).toMatch(/stdio:\s*\['ignore',\s*logFd,\s*logFd\]/);
    expect(startServer, 'never pipe the server through Node').not.toMatch(/child\.(stdout|stderr)\.pipe\(/);
  });
});

/**
 * The other harness defect that made the suite dishonest: every storage
 * `seedLocalStorage` created resolved to the SAME directory, because it sent
 * `config: { root: mountPath, path: 'fileman' }` and the local driver reads
 * `config.path` first. `mountPath` was ignored, so `./fileman` under the
 * server's working dir held every spec's files at once.
 *
 * Nothing announced that. It surfaced as unrelated red: listings scoped to
 * "my storage" returned another spec's uploads, node-id lookups missed, and
 * the explorer never saw a single-storage deployment.
 */
test.describe('Harness — seeded storages must be isolated from each other', () => {
  const A = `iso-a-${Date.now()}`;
  const B = `iso-b-${Date.now()}`;

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, A);
    await dropStorageByName(request, B);
  });

  test('two seeded storages do not see each other\'s files', async ({ request }) => {
    const a = await seedLocalStorage(request, A, `/tmp/filex-${A}`);
    const b = await seedLocalStorage(request, B, `/tmp/filex-${B}`);
    expect(a.config?.path, 'each storage gets its own root').not.toBe(b.config?.path);

    const put = async (storage: string, name: string) => {
      const res = await request.post('/api/files/manager?action=upload', {
        multipart: {
          path: `${storage}://`,
          'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(name) },
        },
      });
      expect(res.ok(), `upload ${name} → ${res.status()}`).toBeTruthy();
    };
    await put(A, 'only-in-a.txt');
    await put(B, 'only-in-b.txt');

    const names = async (storage: string) => {
      const res = await request.get(
        `/api/files/manager?action=index&path=${encodeURIComponent(`${storage}://`)}`,
      );
      expect(res.ok()).toBeTruthy();
      const body = (await res.json()) as { files?: Array<{ basename: string }> };
      return (body.files ?? []).map((f) => f.basename);
    };

    expect(await names(A)).toContain('only-in-a.txt');
    expect(await names(A), `${A} must not see ${B}'s files`).not.toContain('only-in-b.txt');
    expect(await names(B)).toContain('only-in-b.txt');
    expect(await names(B), `${B} must not see ${A}'s files`).not.toContain('only-in-a.txt');
  });
});
