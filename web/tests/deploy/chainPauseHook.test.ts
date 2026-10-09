// The pause and resume hooks of the test chain (#194): what scripts/chain/run.sh
// runs around a run, so that what else the build host runs is out of the way
// while the chain measures and uses the host's memory.
//
// ⚠ Why this exists. The build host also runs an Android emulator that holds
// 3.2-3.8 GiB. Through 0.53 and 0.54 it was up during every run: a run took
// the 6.75-7.25 GiB it left (run.mjs measureHost) and was an hour longer, and
// on the night of 2026-10-09 the chain still pushed the host to memory
// pressure 0.37 and a disk write of 3.1 s. The owner decided (#194) that the
// emulator stops while the chain runs and starts again when it ends - however
// it ends. Stopping it by hand before a release was done twice and forgotten
// as often. The hooks are generic (a command from the settings file, nothing
// of the host in this repository), and these tests hold what makes them safe
// to leave unattended at night:
//   - the pause runs only once the lock is held (two chains must not pause
//     and resume the same thing over each other), and before run.mjs starts,
//     so the budget it measures is the host without what was paused;
//   - the resume runs once, and only after a pause that succeeded (a pause
//     that found nothing to stop must not start it);
//   - a pause that fails or hangs does not stop the chain;
//   - a run stopped by a signal still resumes;
//   - a hook does not inherit the lock's descriptor (a daemon it started
//     would hold the build host's lock forever).

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { hostFields, hostLine, pauseWords } from '../../../scripts/chain/nightly-lib.mjs';
import { hookLines, hookSettings, parseArgs, pauseOf } from '../../../scripts/chain/run.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const CHAIN = path.join(REPO, 'scripts', 'chain');
const RUN_SH = path.join(CHAIN, 'run.sh');
const read = (rel: string) => fs.readFileSync(path.join(CHAIN, rel), 'utf8');

const made: string[] = [];
afterAll(() => {
  for (const d of made) fs.rmSync(d, { recursive: true, force: true });
});

/** The process environment without any CHAIN_* setting of the machine running the tests. */
function cleanEnv(): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [k, v] of Object.entries(process.env)) if (v !== undefined && !k.startsWith('CHAIN_')) env[k] = v;
  return env;
}

describe('the settings of the hooks', () => {
  it('are two one-line commands and a time limit, none by default', () => {
    expect(hookSettings({})).toEqual({ pause: '', resume: '', timeoutS: 900 });
    expect(hookSettings({ CHAIN_PAUSE_CMD: ' /usr/local/lib/hooks/pause.sh ', CHAIN_RESUME_CMD: '/usr/local/lib/hooks/resume.sh', CHAIN_HOOK_TIMEOUT_S: '0' })).toEqual({
      pause: '/usr/local/lib/hooks/pause.sh',
      resume: '/usr/local/lib/hooks/resume.sh',
      timeoutS: 0,
    });
    expect(() => hookSettings({ CHAIN_PAUSE_CMD: 'a\nb' })).toThrow(/CHAIN_PAUSE_CMD must be one line/);
    expect(() => hookSettings({ CHAIN_RESUME_CMD: 'a\rb' })).toThrow(/CHAIN_RESUME_CMD must be one line/);
    expect(() => hookSettings({ CHAIN_HOOK_TIMEOUT_S: 'soon' })).toThrow(/CHAIN_HOOK_TIMEOUT_S=soon/);
    expect(() => hookSettings({ CHAIN_HOOK_TIMEOUT_S: '1.5' })).toThrow(/whole number of seconds/);
  });

  it('reach run.sh through --print-hooks: the limit, the pause, the resume, one per line', () => {
    expect(parseArgs(['--print-hooks'])).toMatchObject({ printHooks: true });
    expect(hookLines({ timeoutS: 900, pause: '/p.sh', resume: '' })).toBe('900\n/p.sh\n');
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'chain-hooks-print-'));
    made.push(dir);
    const envFile = path.join(dir, 'chain.env');
    fs.writeFileSync(envFile, 'CHAIN_ROOT=/var/lib/filex-chain\nCHAIN_PAUSE_CMD=/usr/local/lib/hooks/pause.sh --quiet\nCHAIN_RESUME_CMD=/usr/local/lib/hooks/resume.sh\n');
    const r = spawnSync(process.execPath, [path.join(CHAIN, 'run.mjs'), '--print-hooks', '--env', envFile], {
      cwd: REPO,
      encoding: 'utf8',
      env: cleanEnv(),
      windowsHide: true,
    });
    expect(r.status, r.stderr).toBe(0);
    expect(r.stdout.replace(/\r/g, '').split('\n')).toEqual(['900', '/usr/local/lib/hooks/pause.sh --quiet', '/usr/local/lib/hooks/resume.sh', '']);
    // run.sh asks for them like it asks for the lock, and passes the flag
    // through untouched when it is asked directly.
    const sh = read('run.sh');
    expect(sh).toMatch(/mapfile -t HOOKS < <\(node "\$DIR\/run\.mjs" --print-hooks "\$@"\)/);
    expect(sh).toMatch(/--plan\|--help\|-h\|--print-lock\|--print-hooks\) exec node/);
  });
});

describe('what run.mjs makes of the pause', () => {
  it('reads what run.sh says the pause did, and nothing when no pause ran', () => {
    expect(pauseOf({ CHAIN_PAUSE_STATUS: 'paused', CHAIN_PAUSE_EXIT: '0', CHAIN_PAUSE_SECS: '14' })).toEqual({ status: 'paused', exit: 0, secs: 14 });
    expect(pauseOf({ CHAIN_PAUSE_STATUS: 'failed', CHAIN_PAUSE_EXIT: '7', CHAIN_PAUSE_SECS: '3' })).toEqual({ status: 'failed', exit: 7, secs: 3 });
    expect(pauseOf({ CHAIN_PAUSE_STATUS: 'timeout', CHAIN_PAUSE_EXIT: '124' })).toEqual({ status: 'timeout', exit: 124, secs: null });
    expect(pauseOf({ CHAIN_PAUSE_STATUS: '', CHAIN_PAUSE_EXIT: '0', CHAIN_PAUSE_SECS: '0' })).toBeNull();
    expect(pauseOf({})).toBeNull();
  });

  it("logs it before the budget is measured and keeps it in result.json's host", () => {
    const runMjs = read('run.mjs');
    expect(runMjs).toMatch(/measureHost\(\) \{\n\s+if \(this\.pause\) this\.log\(pauseWords\(this\.pause\)\);/);
    expect(runMjs).toMatch(/this\.pause = pauseOf\(process\.env\);/);
    expect(runMjs).toMatch(/pause: this\.pause,/);
    // measureHost is the run's first look at the host: before the images, the
    // sidecars and every job.
    const run = runMjs.slice(runMjs.indexOf('  async run() {'));
    expect(run.indexOf('this.measureHost();')).toBeGreaterThan(0);
    expect(run.indexOf('this.measureHost();')).toBeLessThan(run.indexOf('this.ensureImages();'));
  });

  it('says it in the morning report, and the night keeps whether its host was paused', () => {
    const h = {
      budget_gb: 8, configured_gb: 8, budget_cut: false, mem_available_start_gb: 11.2, reserve_gb: 1, outside: null, disk: 'dm-0',
      mem_full_max: 0.04, io_full_max: 0.2, disk_write_ms_max: 640, stalled_secs: 0, pool_held_secs: 0, temp_wait_secs: 0,
    };
    expect(hostLine(h)).toBe('host: budget 8 GiB; worst memory full 0.04, io full 0.20, disk writes 640 ms (dm-0)');
    expect(hostLine({ ...h, pause: { status: 'paused', exit: 0, secs: 14 } })).toBe(
      'host: budget 8 GiB; worst memory full 0.04, io full 0.20, disk writes 640 ms (dm-0); pause hook done after 14 s, before the budget was measured',
    );
    expect(hostLine({ ...h, pause: { status: 'failed', exit: 7, secs: 3 } })).toBe(
      'host: budget 8 GiB; worst memory full 0.04, io full 0.20, disk writes 640 ms (dm-0); ' +
        'pause hook failed (exit 7) after 3 s: the run went on beside what it was to pause, and nothing was resumed',
    );
    expect(pauseWords({ status: 'timeout', exit: 124, secs: 900 })).toBe(
      'pause hook timed out (CHAIN_HOOK_TIMEOUT_S) after 900 s: the run went on beside what it was to pause, and nothing was resumed',
    );
    expect(pauseWords(null)).toBe('');
    expect(hostFields({ ...h, pause: { status: 'paused', exit: 0, secs: 14 } })).toMatchObject({ pause: 'paused' });
    expect(hostFields(h)).not.toHaveProperty('pause');
  });
});

describe('every run takes the hooks', () => {
  it('the nightly run starts its chain through run.sh, so the settings file it passes brings the hooks', () => {
    const nightly = read('nightly.mjs');
    expect(nightly).toMatch(/const argv = \[path\.join\(cfg\.src, 'scripts', 'chain', 'run\.sh'\), '--profile', profile/);
    expect(nightly).toMatch(/if \(envFile\) argv\.push\('--env', envFile\);/);
  });

  it('run.sh pauses only once it holds the lock, and starts run.mjs only after the pause', () => {
    const sh = read('run.sh');
    const lock = sh.indexOf('HELD="$LOCK"');
    const pause = sh.indexOf('if hook pause "$PAUSE_CMD"; then');
    const chain = sh.indexOf('node "$DIR/run.mjs" "$@" &');
    expect(lock).toBeGreaterThan(0);
    expect(pause).toBeGreaterThan(lock);
    expect(chain).toBeGreaterThan(pause);
    // The traps that bring the resume come after the lock: a run waiting for
    // the lock still stops at once.
    expect(sh.indexOf('trap resume_once EXIT')).toBeGreaterThan(lock);
    expect(sh.indexOf('trap resume_once EXIT')).toBeLessThan(pause);
  });

  it('the settings examples and the contributing guide name them', () => {
    const example = read('chain.env.example');
    expect(example).toMatch(/^# CHAIN_PAUSE_CMD=/m);
    expect(example).toMatch(/^# CHAIN_RESUME_CMD=/m);
    expect(example).toMatch(/^# CHAIN_HOOK_TIMEOUT_S=900$/m);
    expect(read('nightly.env.example')).toMatch(/^# CHAIN_PAUSE_CMD=/m);
    const guide = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(guide).toContain('**What else the host runs.**');
    expect(guide).toContain('`CHAIN_RESUME_CMD`');
    // A stop of the nightly unit leaves the resume time to boot what it starts.
    expect(read(path.join('systemd', 'filex-nightly.service'))).toMatch(/^TimeoutStopSec=10min$/m);
  });
});

// run.sh itself, on Linux (flock, setsid and /proc): a fake `docker` on PATH
// makes run.mjs stop at its first docker call with exit 2 ("docker does not
// answer"), so a whole run is a second or two and touches nothing.
describe.skipIf(process.platform !== 'linux')('run.sh around a run', () => {
  type Rig = { dir: string; log: string; envFile: string; env: Record<string, string> };

  function rig(opts: { lock?: 'file' | 'none'; hooks?: boolean; timeoutS?: number; env?: Record<string, string> } = {}): Rig {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'chain-hooks-'));
    made.push(dir);
    const bin = path.join(dir, 'bin');
    fs.mkdirSync(bin);
    const log = path.join(dir, 'hooks.log');
    const lockFile = path.join(dir, 'lock');
    fs.writeFileSync(
      path.join(bin, 'docker'),
      [
        '#!/bin/sh',
        'echo "docker $1" >> "$FAKE_LOG"',
        'if [ -n "${FAKE_DOCKER_SLEEP:-}" ]; then',
        '  echo $$ > "$FAKE_PIDFILE"',
        '  exec sleep "$FAKE_DOCKER_SLEEP" </dev/null >/dev/null 2>&1',
        'fi',
        'exit 1',
        '',
      ].join('\n'),
      { mode: 0o755 },
    );
    fs.writeFileSync(
      path.join(dir, 'pause.sh'),
      [
        'if flock -n "$FAKE_LOCK" true; then held=free; else held=held; fi',
        'fd9=closed',
        'if [ -e "/proc/$$/fd/9" ]; then fd9=open; fi',
        'echo "pause lock=$held fd9=$fd9" >> "$FAKE_LOG"',
        'if [ -n "${FAKE_PAUSE_SLEEP:-}" ]; then sleep "$FAKE_PAUSE_SLEEP"; fi',
        'exit "${FAKE_PAUSE_EXIT:-0}"',
        '',
      ].join('\n'),
    );
    fs.writeFileSync(path.join(dir, 'resume.sh'), ['echo "resume exit=${CHAIN_EXIT:-unset}" >> "$FAKE_LOG"', ''].join('\n'));
    const lines = [`CHAIN_ROOT=${path.join(dir, 'root')}`, `CHAIN_LOCK=${opts.lock === 'none' ? 'none' : lockFile}`];
    if (opts.hooks !== false) {
      lines.push(`CHAIN_PAUSE_CMD=sh ${path.join(dir, 'pause.sh')}`, `CHAIN_RESUME_CMD=sh ${path.join(dir, 'resume.sh')}`);
    }
    if (opts.timeoutS !== undefined) lines.push(`CHAIN_HOOK_TIMEOUT_S=${opts.timeoutS}`);
    const envFile = path.join(dir, 'chain.env');
    fs.writeFileSync(envFile, `${lines.join('\n')}\n`);
    const env = {
      ...cleanEnv(),
      PATH: [bin, path.dirname(process.execPath), process.env.PATH ?? ''].join(':'),
      FAKE_LOG: log,
      FAKE_LOCK: lockFile,
      FAKE_PIDFILE: path.join(dir, 'docker.pid'),
      ...(opts.env ?? {}),
    };
    return { dir, log, envFile, env };
  }

  const logOf = (r: Rig) => (fs.existsSync(r.log) ? fs.readFileSync(r.log, 'utf8').trim().split('\n').filter(Boolean) : []);

  function runSh(r: Rig) {
    return spawnSync('bash', [RUN_SH, '--env', r.envFile], { cwd: REPO, encoding: 'utf8', env: r.env, timeout: 90_000, windowsHide: true });
  }

  it('pauses once the lock is held, runs the chain, then resumes once with its exit code', () => {
    const r = rig();
    const out = runSh(r);
    const said = `${out.stdout}\n${out.stderr}`;
    expect(out.status, said).toBe(2);
    expect(logOf(r)).toEqual(['pause lock=held fd9=closed', 'docker version', 'resume exit=2']);
    expect(out.stdout).toMatch(/pause hook: done in \d+s/);
    expect(out.stdout).toMatch(/resume hook: done in \d+s/);
  }, 90_000);

  it('runs the hooks on a run without a lock too', () => {
    const r = rig({ lock: 'none' });
    const out = runSh(r);
    expect(out.status, `${out.stdout}\n${out.stderr}`).toBe(2);
    expect(logOf(r)).toEqual(['pause lock=free fd9=closed', 'docker version', 'resume exit=2']);
  }, 90_000);

  it('goes on when the pause fails, logs it, and resumes nothing', () => {
    const r = rig({ env: { FAKE_PAUSE_EXIT: '7' } });
    const out = runSh(r);
    expect(out.status, `${out.stdout}\n${out.stderr}`).toBe(2);
    expect(logOf(r)).toEqual(['pause lock=held fd9=closed', 'docker version']);
    expect(out.stdout).toMatch(/pause hook: failed \(exit 7\)/);
    expect(out.stdout).toContain('the run goes on without the pause, and nothing will be resumed');
  }, 90_000);

  it('cuts a pause that hangs at CHAIN_HOOK_TIMEOUT_S, with what it started, and goes on', () => {
    const r = rig({ timeoutS: 2, env: { FAKE_PAUSE_SLEEP: '60' } });
    const t0 = Date.now();
    const out = runSh(r);
    expect(out.status, `${out.stdout}\n${out.stderr}`).toBe(2);
    expect(Date.now() - t0).toBeLessThan(45_000);
    expect(logOf(r)).toEqual(['pause lock=held fd9=closed', 'docker version']);
    expect(out.stdout).toMatch(/pause hook: timed out after \d+s \(CHAIN_HOOK_TIMEOUT_S=2\)/);
  }, 90_000);

  it('runs as before when no hook is set', () => {
    const r = rig({ hooks: false });
    const out = runSh(r);
    expect(out.status, `${out.stdout}\n${out.stderr}`).toBe(2);
    expect(logOf(r)).toEqual(['docker version']);
    expect(out.stdout).not.toMatch(/hook/);
  }, 90_000);

  it("resumes once when the run is stopped, and keeps the stop's exit code", async () => {
    const r = rig({ env: { FAKE_DOCKER_SLEEP: '60' } });
    const child = spawn('bash', [RUN_SH, '--env', r.envFile], { cwd: REPO, env: r.env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
    let said = '';
    child.stdout.on('data', (d) => {
      said += String(d);
    });
    child.stderr.on('data', (d) => {
      said += String(d);
    });
    const closed = new Promise<number | null>((resolve) => child.on('close', (code) => resolve(code)));
    try {
      const until = Date.now() + 60_000;
      while (!logOf(r).includes('docker version') && Date.now() < until) await new Promise((res) => setTimeout(res, 100));
      expect(logOf(r), said).toContain('docker version');
      child.kill('SIGTERM');
      const code = await closed;
      expect(code, said).toBe(143);
      expect(logOf(r)).toEqual(['pause lock=held fd9=closed', 'docker version', 'resume exit=143']);
    } finally {
      const pidFile = r.env.FAKE_PIDFILE;
      if (fs.existsSync(pidFile)) {
        try {
          process.kill(Number(fs.readFileSync(pidFile, 'utf8').trim()), 'SIGKILL');
        } catch {
          /* gone already */
        }
      }
      if (child.exitCode === null) child.kill('SIGKILL');
    }
  }, 90_000);
});
