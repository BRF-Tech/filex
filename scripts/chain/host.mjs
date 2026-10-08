// What the build host goes through while the chain runs (task #194): the
// kernel's pressure stall information (PSI), the disk the run writes to, and
// each job container's own memory, writes and stalls (cgroup v2).
//
// ⚠ Lessons #1222 and #1232: a timeout in the chain is first a question about
// the host. Memory "full" over 0.3, or a disk whose writes take seconds,
// slows everything that runs at that minute 20-60 times, and a test that
// holds itself to a few seconds of wall-clock time goes red without a bug.
// run.mjs reads these every few seconds and keeps them in the run
// (result.json `host`, and `load` per job), so the answer is in the run, not
// in a Prometheus query after the fact.
//
// The parsers are pure (web/tests/deploy/chainHost.test.ts). The readers
// return null where a file is not there (not Linux, cgroup v1, no PSI): run.mjs
// then records nothing for it.

import fs from 'node:fs';
import path from 'node:path';

function readOr(file) {
  try {
    return fs.readFileSync(file, 'utf8');
  } catch {
    return null;
  }
}

/**
 * A /proc/pressure/<resource> file (or a cgroup's <resource>.pressure):
 * { some, full }, each { avg10, avg60, avg300, total }. The averages are
 * fractions of the time (the file's percent / 100, the scale of
 * Prometheus' irate(node_pressure_*_seconds_total)); total is in seconds.
 */
export function parsePressure(text) {
  if (!text) return null;
  const out = {};
  for (const line of text.split('\n')) {
    const m = /^(some|full)\s+(.*)$/.exec(line.trim());
    if (!m) continue;
    const f = Object.fromEntries(
      m[2]
        .split(/\s+/)
        .map((kv) => kv.split('='))
        .filter((kv) => kv.length === 2),
    );
    const n = (k, scale) => (f[k] === undefined || !Number.isFinite(Number(f[k])) ? null : Number(f[k]) / scale);
    out[m[1]] = { avg10: n('avg10', 100), avg60: n('avg60', 100), avg300: n('avg300', 100), total: n('total', 1e6) };
  }
  return out.some || out.full ? { some: out.some ?? null, full: out.full ?? null } : null;
}

/** The host's memory and IO pressure now: { memFull, ioFull } (avg10 "full", fractions), or null without PSI. */
export function readPressure(root = '/proc/pressure') {
  const mem = parsePressure(readOr(path.join(root, 'memory')));
  const io = parsePressure(readOr(path.join(root, 'io')));
  if (!mem && !io) return null;
  return { memFull: mem?.full?.avg10 ?? 0, ioFull: io?.full?.avg10 ?? 0 };
}

/**
 * The block device of a stat() `dev` number (Linux's encoding): { major, minor }.
 * A filesystem without one (tmpfs, overlay) has major 0, and no line in /proc/diskstats.
 */
export function blockDevOf(dev) {
  const d = Number(dev);
  const hi = Math.floor(d / 2 ** 32);
  const lo = d % 2 ** 32;
  const major = ((Math.floor(lo / 256) & 0xfff) | ((hi & 0xfffff000) >>> 0)) >>> 0;
  const minor = ((lo & 0xff) | ((Math.floor(lo / 4096) & 0xfff00) >>> 0)) >>> 0;
  return { major, minor };
}

/**
 * One device of /proc/diskstats, by name or by major/minor: its counters
 * (writes completed, milliseconds spent writing, sectors written, requests in
 * flight, milliseconds busy), or null.
 */
export function parseDiskstats(text, want) {
  for (const line of String(text ?? '').split('\n')) {
    const p = line.trim().split(/\s+/);
    if (p.length < 14) continue;
    const [major, minor, name] = [Number(p[0]), Number(p[1]), p[2]];
    if (want.name ? name !== want.name : major !== want.major || minor !== want.minor) continue;
    const f = p.slice(3).map(Number);
    return { name, major, minor, writes: f[4], sectorsWritten: f[6], writeMs: f[7], inFlight: f[8], ioMs: f[9] };
  }
  return null;
}

/**
 * Between two readings of a device `secs` apart: the mean time a write took
 * (ms; null when none completed), the share of the time the device was busy,
 * and MiB/s written. A device that stopped (lesson #1232: 1.1 writes/s, every
 * write 10 s) shows when its writes complete: the milliseconds they waited
 * come in with them.
 */
export function diskDelta(a, b, secs) {
  if (!a || !b || !(secs > 0)) return null;
  const writes = b.writes - a.writes;
  return {
    writeMs: writes > 0 ? (b.writeMs - a.writeMs) / writes : null,
    util: Math.max(0, Math.min(1, (b.ioMs - a.ioMs) / (secs * 1000))),
    writeMBps: ((b.sectorsWritten - a.sectorsWritten) * 512) / 1048576 / secs,
  };
}

/** The device a directory is on, as /proc/diskstats names it (or `name` when given): { name, major, minor } or null. */
export function diskOf(dir, { name = '', diskstats = '/proc/diskstats' } = {}) {
  const text = readOr(diskstats);
  if (!text) return null;
  if (name) return parseDiskstats(text, { name });
  let st;
  try {
    st = fs.statSync(dir);
  } catch {
    return null;
  }
  const dev = blockDevOf(st.dev);
  return dev.major === 0 ? null : parseDiskstats(text, dev);
}

/** The device's counters now. */
export function readDisk(disk, diskstats = '/proc/diskstats') {
  return disk ? parseDiskstats(readOr(diskstats), { name: disk.name }) : null;
}

/** A flat-keyed cgroup file (memory.stat): { key: number }. */
export function parseFlatKeyed(text) {
  const out = {};
  for (const line of String(text ?? '').split('\n')) {
    const [k, v] = line.trim().split(/\s+/);
    if (k && v !== undefined && Number.isFinite(Number(v))) out[k] = Number(v);
  }
  return out;
}

/**
 * A cgroup's io.stat: the bytes it read and wrote on `dev` ("major:minor")
 * when that line is there, else on the device it wrote most to. Not their
 * sum: a write through device-mapper is counted on dm-N and again on the disk
 * under it.
 */
export function ioStatBytes(text, dev = '') {
  let best = null;
  for (const line of String(text ?? '').split('\n')) {
    const p = line.trim().split(/\s+/);
    if (!p[0] || !p[0].includes(':')) continue;
    const kv = Object.fromEntries(p.slice(1).map((x) => x.split('=')).map(([k, v]) => [k, Number(v)]));
    const row = { dev: p[0], rbytes: kv.rbytes ?? 0, wbytes: kv.wbytes ?? 0 };
    if (dev && row.dev === dev) return row;
    if (!best || row.wbytes > best.wbytes) best = row;
  }
  return best;
}

/** A Docker container's cgroup v2 directory, by its full id (systemd driver, then cgroupfs), or null. */
export function cgroupDirOf(id, root = '/sys/fs/cgroup') {
  if (!id) return null;
  for (const d of [path.join(root, 'system.slice', `docker-${id}.scope`), path.join(root, 'docker', id)]) {
    if (fs.existsSync(path.join(d, 'memory.current'))) return d;
  }
  return null;
}

/**
 * A container's cgroup now: its working set (memory.current less the
 * inactive page cache - what cAdvisor reports, what plan.mjs WEIGHTS are),
 * memory.peak (page cache included; kernels from 5.19), the bytes it wrote,
 * and the seconds its tasks were all stalled on memory and on IO. null when
 * the cgroup is gone.
 */
export function readCgroup(dir, dev = '') {
  const currentText = dir ? readOr(path.join(dir, 'memory.current')) : null;
  const current = Number(currentText);
  if (currentText === null || !Number.isFinite(current)) return null;
  const stat = parseFlatKeyed(readOr(path.join(dir, 'memory.stat')));
  const peak = Number(readOr(path.join(dir, 'memory.peak')));
  const io = ioStatBytes(readOr(path.join(dir, 'io.stat')), dev);
  const mem = parsePressure(readOr(path.join(dir, 'memory.pressure')));
  const iop = parsePressure(readOr(path.join(dir, 'io.pressure')));
  return {
    wsBytes: Math.max(0, current - (stat.inactive_file ?? 0)),
    peakBytes: Number.isFinite(peak) && peak > 0 ? peak : null,
    wbytes: io ? io.wbytes : null,
    memFullS: mem?.full?.total ?? null,
    ioFullS: iop?.full?.total ?? null,
  };
}

/**
 * The containers that are not this chain's, from
 * `docker ps --no-trunc --format '{{.ID}}\t{{.Names}}\t{{.Label "filex-chain"}}'`:
 * [{ id, name }].
 */
export function outsideContainers(text, prefix) {
  return String(text ?? '')
    .split('\n')
    .map((l) => l.replace(/\r$/, '').split('\t'))
    .filter(([id, name]) => id && name)
    .filter(([, , label]) => (label ?? '') !== prefix)
    .map(([id, name]) => ({ id, name }));
}

/** "android16 3.1 GiB, brf-mono 0.6 GiB, ..." - the heaviest `max` of [{ name, gb }] over `minGb`, and how much all of them hold. */
export function describeOutside(list, { max = 5, minGb = 0.25 } = {}) {
  const known = list.filter((c) => Number.isFinite(c.gb));
  const total = known.reduce((s, c) => s + c.gb, 0);
  const shown = [...known].sort((a, b) => b.gb - a.gb).filter((c) => c.gb >= minGb).slice(0, max);
  const names = shown.map((c) => `${c.name} ${c.gb.toFixed(1)} GiB`).join(', ');
  return `${names || 'nothing over ' + minGb + ' GiB'}; ${known.length} container(s), ${total.toFixed(1)} GiB in all`;
}

/**
 * What a job went through, for its record (result.json `load`): the highest
 * host memory and IO "full" and the slowest disk write while it ran, the most
 * its container held, and what it wrote. `l` is run.mjs's running tally.
 */
export function loadRecord(l) {
  const gb = (b) => (Number.isFinite(b) ? Math.round((b / 2 ** 30) * 100) / 100 : null);
  const r2 = (n) => (Number.isFinite(n) ? Math.round(n * 100) / 100 : null);
  return {
    host_mem_full: r2(l.hostMemFull),
    host_io_full: r2(l.hostIoFull),
    disk_write_ms: Number.isFinite(l.diskWriteMs) ? Math.round(l.diskWriteMs) : null,
    mem_gb: gb(l.wsBytes),
    mem_peak_gb: gb(l.peakBytes),
    written_gb: gb(l.wbytes),
    stalled_mem_s: r2(l.memFullS),
    stalled_io_s: r2(l.ioFullS),
  };
}
