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
 * For running gates side by side (`all(…, { jobs })`, task #172):
 *   `lane`   gates of one lane run one at a time, in list order (two vitest
 *            runs in one tree, everything in the one WSL mirror of the Go
 *            module, the browser suites and their ports)
 *   `after`  names of gates that must have finished first; when one of them
 *            is red this gate is recorded red too, "not run", and never
 *            started (a name not in the list is ignored)
 * For the gate cache (see `cache` below):
 *   `inputs`      globs of the repository files the verdict depends on; a
 *                 gate without them is never cached (builds, images: they
 *                 make what later gates use)
 *   `cacheExtra`  (ctx) => what else it reads, outside this repository, as
 *                 a string; null means "could not tell", and the gate runs
 *
 * The runner never decides a gate may be skipped: a gate either ran and
 * passed, passed on exactly these inputs before (and says so), or the
 * release stops.
 */
export class Gates {
  /**
   * `cache`: { dir, read, write } or null. `dir` holds one JSON file per green
   * key (<git dir>/filex-release/gate-cache, beside the run state); `read`
   * lets a gate pass on a green entry for its key (`--no-cache` turns it
   * off), `write` records a gate that just passed (off in a dry run, which
   * writes nothing).
   */
  constructor({ logsDir, bash, repo, onRecord = () => {}, cache = null }) {
    this.logsDir = logsDir;
    this.bash = bash;
    this.repo = repo;
    this.onRecord = onRecord;
    this.cache = cache;
    this.results = [];
    this.tree = null;
    fs.mkdirSync(logsDir, { recursive: true });
  }

  logPath(stage, name) {
    const safe = `${stage}--${name}`.toLowerCase().replace(/[^a-z0-9.-]+/g, '-').replace(/-+/g, '-').slice(0, 90);
    return path.join(this.logsDir, `${safe}.log`);
  }

  /**
   * Runs every spec (all of them, so one red run shows every problem). With
   * `jobs` above 1, or when a spec names a `lane` or an `after`, independent
   * gates run side by side, at most `jobs` at a time.
   */
  async all(stage, specs, ctx, { jobs = 1 } = {}) {
    if (jobs <= 1 && !specs.some((s) => s.lane || s.after?.length)) {
      const out = [];
      for (const spec of specs) out.push(await this.one(stage, spec, ctx));
      return out.every((r) => r.ok);
    }
    const out = await this.#schedule(stage, specs, ctx, Math.max(1, jobs));
    return out.every((r) => r.ok);
  }

  /**
   * The side-by-side runner. A gate starts when a slot is free, every gate
   * it is `after` has finished green, and no earlier gate of its lane is
   * unfinished. Gates are taken in list order, so the list is also the
   * priority.
   */
  #schedule(stage, specs, ctx, jobs) {
    const names = new Set(specs.map((s) => s.name));
    const results = new Array(specs.length).fill(null);
    const started = new Array(specs.length).fill(false);
    const verdict = new Map();
    const holder = new Map();
    let running = 0;
    return new Promise((resolve) => {
      const finish = (i, rec) => {
        results[i] = rec;
        verdict.set(specs[i].name, rec.ok);
        running--;
        if (holder.get(specs[i].lane) === i) holder.delete(specs[i].lane);
        pump();
      };
      const launch = (i, spec) => {
        started[i] = true;
        running++;
        this.one(stage, spec, ctx).then(
          (rec) => finish(i, rec),
          (e) => finish(i, { stage, name: specs[i].name, ok: false, detail: `the gate itself crashed: ${e?.stack ?? e}` }),
        );
      };
      const notRun = (i, why) => launch(i, { name: specs[i].name, check: () => ({ ok: false, detail: `not run: ${why}` }) });
      const pump = () => {
        for (let i = 0; i < specs.length; i++) {
          if (started[i]) continue;
          const s = specs[i];
          const deps = (s.after ?? []).filter((n) => n !== s.name && names.has(n));
          const red = deps.find((n) => verdict.get(n) === false);
          if (red !== undefined) {
            notRun(i, `it needs "${red}", which is red`);
            continue;
          }
          if (running >= jobs) continue;
          if (deps.some((n) => !verdict.has(n))) continue;
          if (s.lane && (holder.has(s.lane) || specs.some((p, j) => j < i && p.lane === s.lane && !results[j]))) continue;
          if (s.lane) holder.set(s.lane, i);
          if (jobs > 1) console.log(`  ${dim('start ')}  ${dim(s.name)}`);
          launch(i, s);
        }
        if (results.every(Boolean)) {
          resolve(results);
          return;
        }
        // Nothing running and nothing able to start: what is left waits on
        // something that never finishes (`after` names make a cycle).
        if (running === 0) {
          for (let i = 0; i < specs.length; i++) if (!started[i]) notRun(i, 'what it waits for never finishes (its `after` names make a cycle)');
        }
      };
      pump();
    });
  }

  async one(stage, spec, ctx) {
    const log = this.logPath(stage, spec.name);
    const t0 = Date.now();
    let res;
    let key = null;
    let cached = null;
    if (this.cache && spec.inputs && !spec.check) {
      try {
        key = await this.#cacheKey(spec, ctx);
        if (key && this.cache.read) cached = await this.#cacheLookup(spec.name, key);
      } catch (e) {
        console.log(`  ${yellow('note  ')}  ${spec.name}: the gate cache could not be read (${e?.message ?? e}); the gate runs`);
        key = null;
        cached = null;
      }
    }
    if (cached) {
      const note = log.replace(/\.log$/, '.cached.log');
      res = { ok: true, detail: `from the cache: green on exactly these inputs at ${cached.at}${cached.head ? ` (${String(cached.head).slice(0, 10)})` : ''}` };
      fs.writeFileSync(note, `${res.detail}\nkey ${key}\nthe green run's log: ${cached.log ?? '-'}\n`);
      const rec = { stage, name: spec.name, ok: true, cached: true, detail: res.detail, log: slash(note), secs: 0, at: new Date().toISOString() };
      this.results.push(rec);
      this.onRecord(rec);
      console.log(`  ${green('ok    ')}  ${spec.name} ${dim('(cached)')}  ${dim(res.detail.slice(0, 110))}`);
      return rec;
    }
    try {
      res = await this.#execute(spec, ctx, log);
    } catch (e) {
      res = { ok: false, detail: `the gate itself crashed: ${e?.stack ?? e}` };
    }
    const secs = (Date.now() - t0) / 1000;
    if (!fs.existsSync(log)) fs.writeFileSync(log, `${res.detail ?? ''}\n`);
    const rec = { stage, name: spec.name, ok: !!res.ok, detail: res.detail ?? '', log: slash(log), secs, at: new Date().toISOString() };
    // Only for the HEAD the key was made from: a commit during the run makes
    // the key describe something else.
    if (rec.ok && key && this.cache?.write && revParse(this.repo, 'HEAD') === this.tree?.head) await this.#cacheStore(spec.name, key, { at: rec.at, head: this.tree?.head ?? null, secs, log: rec.log });
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

  // ── the gate cache (task #172; the rules: scripts/release/checks.mjs) ─────
  //
  // ⚠ A key is made only from what HEAD holds, so it is made only when the
  // working tree IS HEAD (untracked files included: vitest picks up a stray
  // test file). Ignored build output is not part of it - each gate that needs
  // a build either builds it itself or comes after the gate that does.

  /** This gate's key, or null when it cannot be told (the gate then runs). */
  async #cacheKey(spec, ctx) {
    const { gateCacheKey, inputEntries, parseLsTree } = await import('./checks.mjs');
    const head = revParse(this.repo, 'HEAD');
    if (!head) return null;
    const st = git(this.repo, 'status', '--porcelain', '--untracked-files=all');
    if (st.status !== 0 || st.stdout.trim()) return null;
    if (this.tree?.head !== head) {
      const ls = git(this.repo, 'ls-tree', '-r', '-z', '--full-tree', head);
      if (ls.status !== 0) return null;
      this.tree = { head, entries: parseLsTree(ls.stdout) };
    }
    const val = (v) => (typeof v === 'function' ? v(ctx) : v);
    const extra = spec.cacheExtra ? spec.cacheExtra(ctx) : undefined;
    if (extra === null) return null;
    const v = spec.vitest;
    const recipe = {
      sh: spec.sh ? val(spec.sh) : undefined,
      cmd: spec.cmd ? val(spec.cmd) : undefined,
      vitest: v ? { cwd: val(v.cwd), files: val(v.files), env: val(v.env), mustPass: val(v.mustPass) } : undefined,
      env: val(spec.env),
      cwd: val(spec.cwd),
      expect: spec.expect ? String(spec.expect) : undefined,
      extra,
    };
    const files = inputEntries(this.tree.entries, spec.inputs);
    const facts = { platform: process.platform, arch: process.arch, node: process.version };
    return gateCacheKey({ name: spec.name, recipe, files, facts });
  }

  #cacheFile(key) {
    return path.join(this.cache.dir, `${key}.json`);
  }

  /** The green entry for this key, or null; an unusable entry is said out loud. */
  async #cacheLookup(name, key) {
    const { cacheEntryHit } = await import('./checks.mjs');
    const file = this.#cacheFile(key);
    let text;
    try {
      text = fs.readFileSync(file, 'utf8');
    } catch {
      return null;
    }
    const v = cacheEntryHit(text, { key, name });
    if (v.hit) return v.entry;
    console.log(`  ${yellow('note  ')}  ${name}: its cache entry is unusable (${v.why}); the gate runs`);
    return null;
  }

  /** Records a green gate. A cache that cannot be written costs a run later, nothing now. */
  async #cacheStore(name, key, entry) {
    try {
      const { GATE_CACHE_SCHEMA } = await import('./checks.mjs');
      fs.mkdirSync(this.cache.dir, { recursive: true });
      const file = this.#cacheFile(key);
      const tmp = `${file}.${process.pid}.tmp`;
      fs.writeFileSync(tmp, `${JSON.stringify({ schema: GATE_CACHE_SCHEMA, name, key, ok: true, ...entry }, null, 2)}\n`);
      fs.renameSync(tmp, file);
    } catch (e) {
      console.log(`  ${yellow('note  ')}  ${name}: green, but the gate cache could not record it (${e?.message ?? e})`);
    }
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
