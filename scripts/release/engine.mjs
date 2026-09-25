// The machinery under `pnpm release`: running a gate, writing down what it
// said, and remembering where a release got to between two invocations.
//
// ⚠⚠ A gate is judged by ITS OWN exit code and nothing else. Every command
// writes to its own log file and is never piped through `tail` or `grep`: a
// pipeline answers with its last command's status, and that is how a red
// Playwright suite was once reported as "all gates green" (2026-09-19) and how
// a failed plugin build carried on with the previous .wasm (2026-09-21).

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const IS_WIN = process.platform === 'win32';

// ── processes ───────────────────────────────────────────────────────────────

/** Runs a program to completion and hands back what it said. Never throws. */
export function run(bin, args, { cwd, env, input } = {}) {
  const r = spawnSync(bin, args, {
    cwd,
    env: env ? { ...process.env, ...env } : process.env,
    encoding: 'utf8',
    input,
    maxBuffer: 256 * 1024 * 1024,
    windowsHide: true,
  });
  return {
    status: typeof r.status === 'number' ? r.status : 127,
    stdout: r.stdout ?? '',
    stderr: r.stderr ?? (r.error ? String(r.error.message) : ''),
  };
}

/** git -C dir …, never throwing. */
export const git = (dir, ...args) => run('git', ['-C', dir, ...args]);

/** git -C dir …, throwing with git's own words when it fails. */
export function gitOut(dir, ...args) {
  const r = git(dir, ...args);
  if (r.status !== 0) throw new Error(`git ${args.join(' ')} failed in ${dir}: ${(r.stderr || r.stdout).trim()}`);
  return r.stdout.replace(/\s+$/, '');
}

/** A commit id, or null when the name does not resolve. */
export function revParse(dir, name) {
  const r = git(dir, 'rev-parse', '-q', '--verify', name);
  return r.status === 0 ? r.stdout.trim() : null;
}

/** `git ls-remote [options] <remote>` as a Map of ref → id, or an error. */
export function lsRemote(dir, remote, options = []) {
  const r = git(dir, 'ls-remote', ...options, remote);
  if (r.status !== 0) return { error: (r.stderr || r.stdout).trim() || `exit ${r.status}` };
  const map = new Map();
  for (const line of r.stdout.split('\n')) {
    const [id, ref] = line.trim().split(/\s+/);
    if (id && ref) map.set(ref, id);
  }
  return { map };
}

/** Forward slashes: the one path spelling bash, git and node all accept. */
export const slash = (p) => String(p).split('\\').join('/');

/** Single-quotes a word for bash. */
export const shq = (s) => `'${String(s).split("'").join(`'"'"'`)}'`;

/**
 * The bash that runs a gate's shell command.
 *
 * ⚠ On Windows, `bash` on PATH is very often C:\Windows\System32\bash.exe —
 * that is WSL, a different machine with a different git, which cannot read a
 * worktree's `.git` file (it names a Windows path). scripts/export-public.sh
 * run that way deleted every tracked file of its target before it failed
 * (2026-09-24, lesson #450). So this looks for Git for Windows' own bash, next
 * to the git that is already on PATH, and never falls back to whatever `bash`
 * happens to resolve to.
 */
export function findBash() {
  if (!IS_WIN) return 'bash';
  const execPath = run('git', ['--exec-path']).stdout.trim();
  const roots = [];
  if (execPath) roots.push(path.resolve(execPath, '..', '..', '..'));
  roots.push('C:/Program Files/Git');
  for (const root of roots) {
    for (const rel of ['bin/bash.exe', 'usr/bin/bash.exe']) {
      const p = path.join(root, rel);
      if (fs.existsSync(p)) return p;
    }
  }
  return null;
}

/**
 * [bin, args] that run `docker <args>` the way the maintainer's shell does.
 *
 * ⚠ On the maintainer's Windows machine `docker` is not an executable: it is
 * a bash script (and a .cmd twin) that forwards to docker-ce inside WSL
 * (Docker Desktop was removed on 2026-08-23). node cannot spawn either
 * directly — it answers ENOENT — so on Windows docker goes through Git Bash,
 * with the arguments as positional parameters so nothing is re-quoted.
 * Because the daemon may not see Windows paths, release gates never bind-mount
 * a host directory: they pipe what a container needs through stdin.
 */
export function dockerArgv(args, bash = findBash()) {
  if (!IS_WIN) return ['docker', args];
  return [bash ?? 'bash', ['-c', 'exec docker "$@"', 'docker', ...args]];
}

/** Runs docker to completion (see dockerArgv). Never throws. */
export function docker(args, opts = {}) {
  const [bin, argv] = dockerArgv(args);
  return run(bin, argv, { ...opts, env: { MSYS_NO_PATHCONV: '1', MSYS2_ARG_CONV_EXCL: '*', ...(opts.env ?? {}) } });
}

// ── output ──────────────────────────────────────────────────────────────────

const tty = process.stdout.isTTY && !process.env.NO_COLOR;
const paint = (code) => (s) => (tty ? `\x1b[${code}m${s}\x1b[0m` : s);
export const red = paint('31');
export const green = paint('32');
export const yellow = paint('33');
export const bold = paint('1');
export const dim = paint('2');

export function banner(title, note = '') {
  const line = `── ${title} `.padEnd(72, '─');
  console.log(`\n${bold(line)}${note ? ` ${note}` : ''}`);
}

const lastLines = (s, n) => String(s ?? '').replace(/\s+$/, '').split(/\r?\n/).slice(-n);

// ── gates ───────────────────────────────────────────────────────────────────

/**
 * Runs gates and keeps the ledger.
 *
 * A gate spec is one of:
 *   { name, check: async (ctx) => ({ ok, detail }) }     judged in-process
 *   { name, sh: 'command' | (ctx) => 'command' }          bash -c, in the repo
 *   { name, cmd: [bin, ...args] | (ctx) => [...] }        a program, no shell
 *   { name, vitest: { cwd, files, env, mustPass } }       see vitestSpec()
 * plus optional `cwd`, `env` (object or (ctx) => object) and `expect`
 * (a RegExp the command's output must match — for a runner that can exit 0
 * having run nothing).
 *
 * The runner never decides a gate may be skipped: a gate either ran and
 * passed, or the release stops.
 */
export class Gates {
  constructor({ logsDir, bash, repo, onRecord = () => {} }) {
    this.logsDir = logsDir;
    this.bash = bash;
    this.repo = repo;
    this.onRecord = onRecord;
    this.results = [];
    fs.mkdirSync(logsDir, { recursive: true });
  }

  logPath(stage, name) {
    const safe = `${stage}--${name}`.toLowerCase().replace(/[^a-z0-9.-]+/g, '-').replace(/-+/g, '-').slice(0, 90);
    return path.join(this.logsDir, `${safe}.log`);
  }

  /** Runs every spec (all of them, so one red run shows every problem). */
  async all(stage, specs, ctx) {
    const out = [];
    for (const spec of specs) out.push(await this.one(stage, spec, ctx));
    return out.every((r) => r.ok);
  }

  async one(stage, spec, ctx) {
    const log = this.logPath(stage, spec.name);
    const t0 = Date.now();
    let res;
    try {
      res = await this.#execute(spec, ctx, log);
    } catch (e) {
      res = { ok: false, detail: `the gate itself crashed: ${e?.stack ?? e}` };
    }
    const secs = (Date.now() - t0) / 1000;
    if (!fs.existsSync(log)) fs.writeFileSync(log, `${res.detail ?? ''}\n`);
    const rec = { stage, name: spec.name, ok: !!res.ok, detail: res.detail ?? '', log: slash(log), secs, at: new Date().toISOString() };
    this.results.push(rec);
    this.onRecord(rec);
    const time = dim(`(${secs < 10 ? secs.toFixed(1) : Math.round(secs)}s)`);
    if (rec.ok) {
      const d = rec.detail ? `  ${dim(String(rec.detail).split('\n')[0].slice(0, 110))}` : '';
      console.log(`  ${green('ok    ')}  ${spec.name} ${time}${d}`);
    } else {
      console.log(`  ${red('FAILED')}  ${bold(spec.name)} ${time}`);
      const body = rec.detail ? String(rec.detail).split('\n') : lastLines(fs.readFileSync(log, 'utf8'), 14);
      for (const l of body.slice(0, 40)) console.log(`          ${l}`);
      console.log(`          ${dim(`log: ${rec.log}`)}`);
    }
    return rec;
  }

  async #execute(spec, ctx, log) {
    const val = (v) => (typeof v === 'function' ? v(ctx) : v);
    if (spec.check) {
      const r = await spec.check(ctx);
      fs.writeFileSync(log, `${r.detail ?? ''}\n${r.log ?? ''}`);
      return r;
    }
    const env = { ...(val(spec.env) ?? {}) };
    const cwdRaw = val(spec.cwd);
    const cwd = cwdRaw ? path.resolve(this.repo, cwdRaw) : this.repo;
    let bin;
    let args;
    let after = null;
    if (spec.vitest) {
      const v = spec.vitest;
      const json = log.replace(/\.log$/, '.json');
      fs.rmSync(json, { force: true });
      const files = val(v.files) ?? [];
      bin = this.bash;
      args = ['-c', `npx vitest run ${files.map(shq).join(' ')} --reporter=default --reporter=json --outputFile.json=${shq(slash(json))}`];
      Object.assign(env, val(v.env) ?? {});
      const vcwd = path.resolve(this.repo, val(v.cwd) ?? '.');
      after = async (status) => {
        const { vitestVerdict } = await import('./checks.mjs');
        let report = null;
        try {
          report = JSON.parse(fs.readFileSync(json, 'utf8'));
        } catch {
          /* no report: judged below */
        }
        const verdict = vitestVerdict(report, { mustPass: val(v.mustPass) ?? [] });
        const ok = status === 0 && verdict.ok;
        const detail = ok
          ? `${verdict.passed} passed, including every test the gate names`
          : [...(status !== 0 ? [`vitest exited ${status}`] : []), ...verdict.problems].join('\n');
        return { ok, detail };
      };
      return this.#spawnToLog(bin, args, vcwd, env, log, after, spec);
    }
    if (spec.sh) {
      if (!this.bash) return { ok: false, detail: 'no bash to run this gate with (Git for Windows\' bin/bash.exe was not found) — install Git for Windows' };
      bin = this.bash;
      args = ['-c', val(spec.sh)];
    } else if (spec.cmd) {
      [bin, ...args] = val(spec.cmd);
    } else {
      return { ok: false, detail: 'gate has neither check, sh, cmd nor vitest' };
    }
    return this.#spawnToLog(bin, args, cwd, env, log, after, spec);
  }

  #spawnToLog(bin, args, cwd, env, log, after, spec) {
    return new Promise((resolve) => {
      const fd = fs.openSync(log, 'w');
      fs.writeSync(fd, `$ ${[bin, ...args].join(' ')}\n# cwd ${slash(cwd)}\n\n`);
      const child = spawn(bin, args, {
        cwd,
        env: { ...process.env, ...env },
        stdio: ['ignore', fd, fd],
        windowsHide: true,
      });
      const done = async (status, error) => {
        fs.closeSync(fd);
        if (error) return resolve({ ok: false, detail: `could not start ${bin}: ${error.message}` });
        if (after) return resolve(await after(status));
        if (status !== 0) return resolve({ ok: false, detail: '' });
        if (spec.expect) {
          const text = fs.readFileSync(log, 'utf8');
          if (!spec.expect.test(text)) {
            return resolve({ ok: false, detail: `exited 0, but its output never said ${spec.expect} — it may have run nothing` });
          }
        }
        return resolve({ ok: true, detail: '' });
      };
      let settled = false;
      child.on('error', (e) => {
        if (!settled) {
          settled = true;
          done(127, e);
        }
      });
      child.on('close', (code) => {
        if (!settled) {
          settled = true;
          done(typeof code === 'number' ? code : 1, null);
        }
      });
    });
  }
}

// ── state ───────────────────────────────────────────────────────────────────
//
// ⚠ The state lives inside the git directory, never in the working tree: the
// first gate of a release is "the tree is clean", and a tool that dirties the
// tree it is gating fails its own gate (the docs build did exactly that until
// 2026-09-06). A worktree has its own git directory, so two worktrees cannot
// share one release's state by accident.

export function stateFile(gitDir, tag) {
  return path.join(gitDir, 'filex-release', `${tag}.json`);
}

export function loadState(file) {
  if (!fs.existsSync(file)) return null;
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

export function saveState(file, state) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const tmp = `${file}.tmp`;
  fs.writeFileSync(tmp, `${JSON.stringify(state, null, 2)}\n`);
  fs.renameSync(tmp, file);
}

/**
 * One release run at a time per worktree. Two concurrent runs would both see
 * "not stamped yet" and both write a release commit.
 */
export function takeLock(file) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  try {
    fs.writeFileSync(file, String(process.pid), { flag: 'wx' });
  } catch {
    const pid = Number(fs.readFileSync(file, 'utf8').trim());
    let alive = false;
    if (pid > 0) {
      try {
        process.kill(pid, 0);
        alive = true;
      } catch {
        alive = false;
      }
    }
    if (alive && pid !== process.pid) return { held: pid };
    fs.writeFileSync(file, String(process.pid));
  }
  return { release: () => fs.rmSync(file, { force: true }) };
}
