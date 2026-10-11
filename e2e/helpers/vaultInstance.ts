/**
 * A second filex with the vault on (`FILEX_E2E_VAULT=1`), its own data
 * directory and the same admin - for the specs that need level 3 (212 the
 * vault, 236 an app editing a vault's file). The vault is off by default and
 * the shared hermetic instance keeps the default (spec 172 counts two levels
 * there), so these boot their own from E2E_FILEX_BIN, like spec 173's escrow
 * instance.
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

/** A filex with the vault on, its own data directory, the same admin. */
export async function bootVaultInstance(bin: string): Promise<{ base: string; stop: () => Promise<void> }> {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-vault-'));
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
      FILEX_SECRET_KEY: 'e2e-vault-key-not-a-real-secret',
      FILEX_E2E_VAULT: '1',
    },
    stdio: ['ignore', logFd, logFd],
    windowsHide: true,
  });
  let exited: string | null = null;
  child.on('exit', (code, signal) => {
    exited = signal ? `signal ${signal}` : `code ${code}`;
  });
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
      const log = fs.readFileSync(logFile, 'utf8').slice(-1500);
      await stop();
      throw new Error(`the vault instance exited during start-up (${exited})\n${log}`);
    }
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > until) {
      await stop();
      throw new Error(`the vault instance never answered /healthz on ${base}`);
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  return { base, stop };
}
