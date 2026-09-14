// Finding — and ending — every process a run started, including the ones
// nobody holds a handle to any more.
//
// ⚠⚠ Why a run cannot just trust its children to clean up. Every shot script
// kills the filex it booted in a `finally`, and that is not enough:
//
//   - on Windows, killing a process does NOT kill its children. filex starts
//     plugin processes and `thumb backfill` workers; Playwright starts a
//     browser that starts a dozen more. A script that dies — or is killed on a
//     timeout — leaves all of them running, parented to a PID that is gone.
//   - an agent in this repo reported a runaway script "killed" that went on
//     burning a CPU core for nine hours. Nobody listed the processes afterwards.
//
// So a run is identified by something every one of its processes carries, and
// the sweep is a measurement, not a hope:
//
//   Linux    an environment marker (SHOTS_RUN_ID=<id>). Every child inherits
//            it — node, filex, the browser and all its helpers — and
//            /proc/<pid>/environ is readable for our own user.
//   Windows  a process's environment cannot be read from outside, so the
//            marker is a DIRECTORY: the run's private temp dir. The binary is
//            copied into it and TEMP/TMP point into it, so every data dir and
//            every Playwright browser profile lives under it. A process is ours
//            when its executable path or command line names that directory —
//            or when its parent is ours and it was created after that parent
//            (the creation-time test stops a recycled PID adopting a stranger).
//
// ⚠ Both rules are deliberately narrow. The maintainer's installed desktop app
// is also called filex.exe, and review browsers stay open for hours on the same
// machine; neither names the run's directory and neither descends from it.

import { spawnSync } from 'node:child_process';
import { readdirSync, readFileSync, readlinkSync } from 'node:fs';
import path from 'node:path';

const IS_WIN = process.platform === 'win32';

/** The environment variable every process of a run inherits. */
export const RUN_MARKER = 'SHOTS_RUN_ID';
const norm = (s) => String(s ?? '').split('/').join('\\').toLowerCase();

/** Every process visible to this user: { pid, ppid, name, exe, cmd, created, env? }. */
export function listProcesses() {
  return IS_WIN ? listWindows() : listLinux();
}

function listWindows() {
  const ps =
    "$ErrorActionPreference='SilentlyContinue';" +
    'Get-CimInstance Win32_Process | ForEach-Object { [pscustomobject]@{' +
    'pid=$_.ProcessId; ppid=$_.ParentProcessId; name=$_.Name; exe=$_.ExecutablePath; ' +
    'cmd=$_.CommandLine; created=$(if ($_.CreationDate) { $_.CreationDate.ToFileTimeUtc() } else { 0 }) } } | ' +
    'ConvertTo-Json -Compress';
  const r = spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', ps], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    windowsHide: true,
  });
  if (r.status !== 0 || !r.stdout.trim()) {
    throw new Error(`could not list processes: ${r.stderr || `exit ${r.status}`}`);
  }
  const rows = JSON.parse(r.stdout);
  return (Array.isArray(rows) ? rows : [rows]).map((p) => ({
    pid: p.pid,
    ppid: p.ppid,
    name: p.name ?? '',
    exe: p.exe ?? '',
    cmd: p.cmd ?? '',
    created: Number(p.created) || 0,
  }));
}

function listLinux() {
  const out = [];
  for (const d of readdirSync('/proc')) {
    if (!/^\d+$/.test(d)) continue;
    try {
      const stat = readFileSync(`/proc/${d}/stat`, 'utf8');
      // `comm` is parenthesised and may itself contain spaces or parentheses.
      const rest = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
      // ⚠ A zombie has exited and runs nothing; it only waits for its parent to
      // reap it. Counting it makes a sweep run by that parent — synchronously,
      // so nothing gets reaped meanwhile — report its own child as a survivor.
      if (rest[0] === 'Z') continue;
      let exe = '';
      try {
        exe = readlinkSync(`/proc/${d}/exe`);
      } catch {
        /* kernel thread, or another user's process */
      }
      let env = '';
      try {
        env = readFileSync(`/proc/${d}/environ`, 'utf8');
      } catch {
        /* not ours to read — which also means not ours */
      }
      out.push({
        pid: Number(d),
        ppid: Number(rest[1]),
        name: stat.slice(stat.indexOf('(') + 1, stat.lastIndexOf(')')),
        exe,
        cmd: readFileSync(`/proc/${d}/cmdline`, 'utf8').split('\0').join(' ').trim(),
        // field 22, starttime — rest[0] is field 3
        created: Number(rest[19]) || 0,
        env,
      });
    } catch {
      /* exited while we looked */
    }
  }
  return out;
}

/**
 * The processes belonging to one run.
 *
 * @param {object} o
 * @param {string} o.dir     the run's private directory (the Windows marker)
 * @param {string} o.marker  the value of SHOTS_RUN_ID (the Linux marker)
 * @param {number[]} [o.roots]  PIDs this run spawned directly and may still hold
 * @param {object[]} [o.procs]  a listing to reuse
 */
export function runProcesses({ dir, marker, roots = [], procs = listProcesses() }) {
  // Never this process, and never its ancestors: the pnpm and the shell that
  // started the run name nothing, but a parent-walk must not reach them either.
  const self = new Set([process.pid]);
  const byPid = new Map(procs.map((p) => [p.pid, p]));
  for (let p = byPid.get(process.pid); p && !self.has(p.ppid); p = byPid.get(p.ppid)) self.add(p.ppid);

  const needle = norm(path.resolve(dir));
  const mark = `${RUN_MARKER}=${marker}`;
  const ours = new Map();
  for (const p of procs) {
    if (self.has(p.pid)) continue;
    const named = norm(p.exe).includes(needle) || norm(p.cmd).includes(needle);
    const marked = Boolean(marker && p.env && p.env.split('\0').includes(mark));
    if (named || marked || roots.includes(p.pid)) ours.set(p.pid, p);
  }
  for (let grew = true; grew; ) {
    grew = false;
    for (const p of procs) {
      if (ours.has(p.pid) || self.has(p.pid)) continue;
      const parent = ours.get(p.ppid);
      if (parent && p.created >= parent.created) {
        ours.set(p.pid, p);
        grew = true;
      }
    }
  }
  return [...ours.values()];
}

export function killProcess(pid) {
  if (IS_WIN) {
    spawnSync('taskkill', ['/PID', String(pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return;
  }
  try {
    process.kill(pid, 'SIGKILL');
  } catch {
    /* already gone */
  }
}

const pause = (ms) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);

/**
 * Kills every process of the run, then LISTS AGAIN and returns what is still
 * there. `survivors: []` is the only result that means "cleaned up".
 *
 * Synchronous on purpose: it has to work from an `exit` handler.
 */
export function sweepRun(opts) {
  const found = runProcesses(opts);
  if (found.length === 0) return { killed: [], survivors: [] };
  for (const p of found) killProcess(p.pid);
  const key = (p) => `${p.pid}:${p.created}`;
  const deadline = Date.now() + 15_000;
  let survivors;
  for (;;) {
    pause(300);
    const alive = new Set(listProcesses().map(key));
    survivors = found.filter((p) => alive.has(key(p)));
    if (survivors.length === 0 || Date.now() > deadline) break;
    for (const p of survivors) killProcess(p.pid);
  }
  return { killed: found, survivors };
}

export const describeProcess = (p) => `pid ${p.pid} ${p.name}  ${String(p.cmd || p.exe).slice(0, 180)}`;
