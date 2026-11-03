/**
 * A filex of a spec's own, beside the shared hermetic one: its own data
 * directory, its own port, the same admin, and the environment the spec asks
 * for. For what the shared instance must not carry (the vault switched on,
 * a bot check on the sign-in) or what one instance can do only once (a
 * captcha test key's token is accepted once per server, then `replayed`).
 *
 * Boots E2E_FILEX_BIN; the caller skips without one.
 */
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

export interface ExtraInstance {
  base: string;
  /** The server's own log so far (for a red test's message). */
  log: () => string;
  stop: () => Promise<void>;
}

/** Starts a filex with `env` over the defaults, and waits for /healthz. */
export async function bootExtraInstance(
  bin: string,
  { label, env = {} }: { label: string; env?: Record<string, string> },
): Promise<ExtraInstance> {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), `filex-${label}-`));
  const dataDir = path.join(scratch, 'data');
  fs.mkdirSync(dataDir);
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const logFile = path.join(scratch, 'server.log');
  const logFd = fs.openSync(logFile, 'a');
  const child = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_DATA_DIR: dataDir,
      FILEX_PUBLIC_URL: base,
      FILEX_ADMIN_EMAIL: process.env.E2E_ADMIN_EMAIL ?? 'admin@local',
      FILEX_ADMIN_PASSWORD: process.env.E2E_ADMIN_PASSWORD ?? 'admin',
      FILEX_DEFAULT_LOCALE: 'en',
      ...env,
    },
    stdio: ['ignore', logFd, logFd],
    windowsHide: true,
  });
  let exited: string | null = null;
  child.on('exit', (code, signal) => {
    exited = signal ? `signal ${signal}` : `code ${code}`;
  });
  const log = () => {
    try {
      return fs.readFileSync(logFile, 'utf8');
    } catch {
      return '';
    }
  };
  const stop = async () => {
    if (exited === null) child.kill();
    for (let i = 0; i < 50 && exited === null; i++) await new Promise((r) => setTimeout(r, 100));
    try {
      fs.closeSync(logFd);
    } catch {
      /* closed already */
    }
    fs.rmSync(scratch, { recursive: true, force: true });
  };
  const until = Date.now() + 60_000;
  for (;;) {
    if (exited) {
      const tail = log().slice(-1500);
      await stop();
      throw new Error(`the ${label} instance exited during start-up (${exited})\n${tail}`);
    }
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > until) {
      await stop();
      throw new Error(`the ${label} instance never answered /healthz on ${base}`);
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  return { base, log, stop };
}
