// What the test chain records of its build host (task #194): the kernel's
// pressure stall information, the run disk's write latency, each job
// container's memory and writes, the budget it takes from the host, and how
// the morning report reads them.
//
// ⚠ Why this exists. 0.53's three full runs on the build host were red in
// three different places, and the same code was green in another run: a
// minute when memory "full" stood at 0.5-0.7 (the web job's 3 GiB counted as
// 1, beside an Android emulator's 3.2 GiB), and a 15 s window when the disk
// completed 1.1 writes per second, each after 10 s. Each time the answer took
// a Prometheus query by hand (lessons #1222, #1232). The run keeps it now, per
// job, and the report says it beside the red job.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import {
  blockDevOf,
  describeOutside,
  diskDelta,
  ioStatBytes,
  loadRecord,
  outsideContainers,
  parseDiskstats,
  parseFlatKeyed,
  parsePressure,
  readCgroup,
} from '../../../scripts/chain/host.mjs';
import { STALL, composeReport, hostLine, nightRecord, resultFields, stallWords } from '../../../scripts/chain/nightly-lib.mjs';
import { hostBudget } from '../../../scripts/chain/schedule.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const CHAIN = path.join(REPO, 'scripts', 'chain');
const GiB = 2 ** 30;

describe('pressure stall information', () => {
  // /proc/pressure/memory on the build host, 2026-10-07 23:26 UTC.
  const text = 'some avg10=0.53 avg60=4.97 avg300=2.38 total=2308304600\nfull avg10=0.49 avg60=4.65 avg300=2.23 total=2181406821\n';

  it("reads the averages as fractions of the time, Prometheus' scale, and the total in seconds", () => {
    const p = parsePressure(text);
    expect(p?.full?.avg10).toBeCloseTo(0.0049, 6);
    expect(p?.full?.avg60).toBeCloseTo(0.0465, 6);
    expect(p?.some?.avg300).toBeCloseTo(0.0238, 6);
    expect(p?.full?.total).toBeCloseTo(2181.406821, 6);
  });

  it('is null where the kernel has none, and takes a "some"-only file (cpu on older kernels)', () => {
    expect(parsePressure('')).toBeNull();
    expect(parsePressure(null as unknown as string)).toBeNull();
    expect(parsePressure('some avg10=1.00 avg60=0.00 avg300=0.00 total=5')).toMatchObject({ some: { avg10: 0.01 }, full: null });
  });
});

describe('the run disk', () => {
  it("finds a device's numbers from a stat() dev number (Linux's encoding)", () => {
    expect(blockDevOf(252 * 256)).toEqual({ major: 252, minor: 0 }); // dm-0, the build host's root
    expect(blockDevOf(259 * 256 + 3)).toEqual({ major: 259, minor: 3 }); // nvme0n1p3
    expect(blockDevOf((8 << 8) | (272 & 0xff) | ((272 & ~0xff) << 12))).toEqual({ major: 8, minor: 272 });
  });

  // major minor name | reads merged sectors ms | writes merged sectors ms | in-flight io-ms weighted ...
  const line = (writes: number, writeMs: number, sectors: number, ioMs: number) =>
    `  252       0 dm-0 54106 0 1788680 99000 ${writes} 0 ${sectors} ${writeMs} 3 ${ioMs} 900000 0 0 0 0 0 0`;
  const other = '  259       0 nvme0n1 1 0 8 1 2 0 16 2 0 3 4 0 0 0 0 0 0';

  it('reads one device of /proc/diskstats by name or by number', () => {
    const text = `${other}\n${line(1000, 5000, 80000, 120000)}\n`;
    expect(parseDiskstats(text, { name: 'dm-0' })).toMatchObject({ name: 'dm-0', writes: 1000, writeMs: 5000, sectorsWritten: 80000, ioMs: 120000 });
    expect(parseDiskstats(text, { major: 252, minor: 0 })?.name).toBe('dm-0');
    expect(parseDiskstats(text, { name: 'sda' })).toBeNull();
  });

  it('measures the mean write of a window: the 10 s writes of the 15 s stall in lesson #1232', () => {
    const a = parseDiskstats(line(1000, 5000, 80000, 120000), { name: 'dm-0' });
    const b = parseDiskstats(line(1016, 5000 + 16 * 10256, 80000 + 2048, 135000), { name: 'dm-0' });
    const d = diskDelta(a, b, 15);
    expect(d?.writeMs).toBe(10256);
    expect(d?.util).toBe(1);
    expect(d?.writeMBps).toBeCloseTo((2048 * 512) / 2 ** 20 / 15, 6);
    // No write completed: no latency to say yet (the stalled writes bring theirs when they end).
    expect(diskDelta(a, a, 5)?.writeMs).toBeNull();
    expect(diskDelta(null, b, 5)).toBeNull();
  });
});

describe("a job container's cgroup", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'chain-cgroup-'));
  afterAll(() => fs.rmSync(dir, { recursive: true, force: true }));

  it("counts a write once: on the run's device, not again on the disk under it", () => {
    const io = '259:0 rbytes=915898368 wbytes=3207852032 rios=54106 wios=456155 dbytes=0 dios=0\n252:0 rbytes=915898368 wbytes=3207852032 rios=54106 wios=449768 dbytes=0 dios=0\n';
    expect(ioStatBytes(io, '252:0')).toMatchObject({ dev: '252:0', wbytes: 3207852032 });
    expect(ioStatBytes('8:0 rbytes=1 wbytes=10\n8:16 rbytes=1 wbytes=99\n')).toMatchObject({ dev: '8:16', wbytes: 99 });
    expect(ioStatBytes('')).toBeNull();
  });

  it('reads the working set as cAdvisor does (memory.current less the inactive page cache), its peak, writes and stalls', () => {
    // The e2e-chromium container of the night of 2026-10-07, 14 min in.
    fs.writeFileSync(path.join(dir, 'memory.current'), `${2054 * 2 ** 20}\n`);
    fs.writeFileSync(path.join(dir, 'memory.stat'), `anon ${1475 * 2 ** 20}\nfile ${445 * 2 ** 20}\ninactive_file ${341 * 2 ** 20}\nactive_file ${103 * 2 ** 20}\n`);
    fs.writeFileSync(path.join(dir, 'memory.peak'), `${2577 * 2 ** 20}\n`);
    fs.writeFileSync(path.join(dir, 'io.stat'), '252:0 rbytes=1 wbytes=3207852032\n');
    fs.writeFileSync(path.join(dir, 'memory.pressure'), 'some avg10=0.00 avg60=0.00 avg300=0.00 total=487562\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=485192\n');
    fs.writeFileSync(path.join(dir, 'io.pressure'), 'some avg10=0.05 avg60=1.32 avg300=1.44 total=14795629\nfull avg10=0.04 avg60=1.14 avg300=1.14 total=12247220\n');
    const c = readCgroup(dir, '252:0');
    expect(c?.wsBytes).toBe((2054 - 341) * 2 ** 20);
    expect(c?.peakBytes).toBe(2577 * 2 ** 20);
    expect(c?.wbytes).toBe(3207852032);
    expect(c?.memFullS).toBeCloseTo(0.485192, 6);
    expect(c?.ioFullS).toBeCloseTo(12.24722, 6);
    expect(readCgroup(path.join(dir, 'gone'))).toBeNull();
    expect(readCgroup(null as unknown as string)).toBeNull();
    expect(parseFlatKeyed('anon 5\nbad\nfile x\n')).toEqual({ anon: 5 });
  });

  it('keeps what a job went through in GiB, rounded, and null where nothing was read', () => {
    expect(
      loadRecord({ hostMemFull: 0.6543, hostIoFull: 0.711, diskWriteMs: 10255.7, wsBytes: 3.03 * GiB, peakBytes: 3.6 * GiB, wbytes: 34.48 * GiB, memFullS: 12.346, ioFullS: null }),
    ).toEqual({
      host_mem_full: 0.65, host_io_full: 0.71, disk_write_ms: 10256, mem_gb: 3.03, mem_peak_gb: 3.6, written_gb: 34.48, stalled_mem_s: 12.35, stalled_io_s: null,
    });
    expect(loadRecord({ hostMemFull: null, hostIoFull: null, diskWriteMs: null })).toMatchObject({ host_mem_full: null, mem_gb: null, written_gb: null });
  });
});

describe('the budget a run takes from its host', () => {
  it('is CHAIN_MEM_GB, or what MemAvailable leaves above the reserve when that is less, in quarter GiB', () => {
    // The build host with its Android emulator up: 7.9 GiB available at the start of every 0.53 run.
    expect(hostBudget({ memGb: 8, availGb: 7.9, reserveGb: 1 })).toEqual({ gb: 6.75, cut: true });
    expect(hostBudget({ memGb: 8, availGb: 8.4, reserveGb: 1 })).toEqual({ gb: 7.25, cut: true });
    // With the emulator stopped.
    expect(hostBudget({ memGb: 8, availGb: 11.2, reserveGb: 1 })).toEqual({ gb: 8, cut: false });
    expect(hostBudget({ memGb: 8, availGb: 0.5, reserveGb: 1 })).toEqual({ gb: 0, cut: true });
  });

  it('leaves CHAIN_MEM_GB where nothing says what is available (--plan on another system)', () => {
    expect(hostBudget({ memGb: 8, availGb: Infinity, reserveGb: 1 })).toEqual({ gb: 8, cut: false });
    expect(hostBudget({ memGb: 8, availGb: NaN, reserveGb: 1 })).toEqual({ gb: 8, cut: false });
  });

  it("names what is not this chain's: another prefix's leftovers count, this prefix's do not", () => {
    const ps = ['aaa\tandroid16\t', 'bbb\tfxnightly-net\tfxnightly', 'ccc\tfxchain-pg\tfxchain', 'ddd\tgrafana\t', '', 'bad-line'].join('\n');
    expect(outsideContainers(ps, 'fxnightly')).toEqual([
      { id: 'aaa', name: 'android16' },
      { id: 'ccc', name: 'fxchain-pg' },
      { id: 'ddd', name: 'grafana' },
    ]);
    expect(describeOutside([{ name: 'grafana', gb: 0.31 }, { name: 'android16', gb: 3.14 }, { name: 'redis', gb: 0.01 }, { name: 'x', gb: NaN }])).toBe(
      'android16 3.1 GiB, grafana 0.3 GiB; 3 container(s), 3.5 GiB in all',
    );
  });

  it('is measured before the sidecars start, and the run.mjs log names it with the containers outside', () => {
    const run = fs.readFileSync(path.join(CHAIN, 'run.mjs'), 'utf8');
    const start = run.indexOf('this.removeLeftovers();');
    expect(start).toBeGreaterThan(0);
    const measure = run.indexOf('this.measureHost();', start);
    expect(measure).toBeGreaterThan(start);
    expect(measure).toBeLessThan(run.indexOf('await this.servicesUp();', start));
    expect(run).toMatch(/hostBudget\(\{ memGb: this\.cfg\.memGb, availGb: avail, reserveGb: this\.cfg\.reserveGb \}\)/);
    expect(run).toMatch(/this\.log\(`outside the chain: \$\{outside\}`\)/);
  });
});

describe('what a Firefox line writes to the disk', () => {
  it('keeps its HTTP cache in memory and no history database, as a Chromium context does', () => {
    // ⚠ The night of 2026-10-07: six minutes in, the one Firefox profile of
    // the run held an 815 MB cache2 and Firefox had written 2.2 GB; a 0.53
    // full run's Firefox line wrote 33-34 GB, half of the run's writes.
    const pw = fs.readFileSync(path.join(REPO, 'e2e', 'playwright.config.ts'), 'utf8');
    const firefox = /firefox: \{\s*launchOptions: \{\s*firefoxUserPrefs: \{([^}]*)\}/.exec(pw)?.[1] ?? '';
    expect(firefox).toMatch(/'browser\.cache\.disk\.enable': false/);
    expect(firefox).toMatch(/'places\.history\.enabled': false/);
    // The pointer prefs the desktop specs need stay.
    expect(firefox).toMatch(/'ui\.primaryPointerCapabilities': 6/);
  });
});

describe("WebKit's line and the panel's service worker", () => {
  // Task #199: Playwright's WebKit lost its network process while the
  // worker installed (158, 109, 139 on GitHub's full matrix). WebKit runs
  // without it; the spec that tests the worker lets it back in.
  it('blocks the worker for WebKit alone, and 164 lets it back in for the installable-app test', () => {
    const pw = fs.readFileSync(path.join(REPO, 'e2e', 'playwright.config.ts'), 'utf8');
    expect(pw).toMatch(/\n {2}webkit: \{\s*serviceWorkers: 'block',\s*\}/);
    expect(pw.match(/serviceWorkers: 'block'/g) ?? [], 'no other engine and no global block').toHaveLength(1);
    const sub = fs.readFileSync(path.join(REPO, 'e2e', 'tests', '164-sub-path.spec.ts'), 'utf8');
    expect(sub).toMatch(/test\.describe\('the installable app', \(\) => \{\s*test\.use\(\{ serviceWorkers: 'allow' \}\);/);
  });
});

describe('the run and the morning report', () => {
  const load = (over: Record<string, number | null>) => ({
    host_mem_full: 0.02, host_io_full: 0.1, disk_write_ms: 120, mem_gb: 1, mem_peak_gb: 1.2, written_gb: 0.5, stalled_mem_s: 0, stalled_io_s: 1, ...over,
  });

  it('marks a job the host stalled under: memory full 0.3 (lesson #1222), io full 0.5, a write of a second (lesson #1232)', () => {
    expect(STALL).toEqual({ memFull: 0.3, ioFull: 0.5, diskWriteMs: 1000 });
    expect(stallWords(load({}))).toBe('');
    expect(stallWords(null as unknown as Record<string, number>)).toBe('');
    expect(stallWords(load({ host_mem_full: 0.62, disk_write_ms: 10256 }))).toBe('memory full 0.62, disk writes 10256 ms');
    expect(stallWords(load({ host_io_full: 0.71 }))).toBe('io full 0.71');
  });

  it("says the host's budget and its worst minute in one line", () => {
    const h = {
      budget_gb: 6.75, configured_gb: 8, budget_cut: true, mem_available_start_gb: 7.9, reserve_gb: 1,
      outside: 'android16 3.1 GiB; 9 container(s), 5.2 GiB in all', disk: 'dm-0',
      mem_full_max: 0.08, io_full_max: 0.43, disk_write_ms_max: 812, stalled_secs: 0, pool_held_secs: 40, temp_wait_secs: 1516,
    };
    expect(hostLine(h)).toBe(
      'host: budget 6.75 of 8 GiB (MemAvailable 7.9 GiB at the start; outside the chain: android16 3.1 GiB; 9 container(s), 5.2 GiB in all); ' +
        'worst memory full 0.08, io full 0.43, disk writes 812 ms (dm-0); waited 25 min for the disk to cool',
    );
    expect(hostLine({ ...h, budget_cut: false, budget_gb: 8, temp_wait_secs: 0 })).toBe('host: budget 8 GiB; worst memory full 0.08, io full 0.43, disk writes 812 ms (dm-0)');
    expect(hostLine(undefined as unknown as typeof h)).toBeNull();
  });

  it('keeps each night\'s budget and worst pressure in its record: whether the chain still stalls its host, night by night', () => {
    const host = { budget_gb: 7, configured_gb: 8, budget_cut: true, mem_full_max: 0.05, io_full_max: 0.3, disk_write_ms_max: 400, stalled_secs: 0 };
    const f = resultFields({ finished: 'x', ok: true, stopped: false, secs: 1, wall: 'w', counts: {}, jobs: [], host });
    expect(f.host).toEqual({ budget_gb: 7, mem_full_max: 0.05, io_full_max: 0.3, disk_write_ms_max: 400 });
    expect(nightRecord({ night: 'n', decision: 'ran', host: f.host }).host).toEqual(f.host);
  });

  it('names the stall beside the red job it may explain, and the host line under the wall time', () => {
    const now = Date.parse('2026-10-08T04:00:00Z');
    const sha = 'c'.repeat(40);
    const r = composeReport({
      tonight: { night: '2026-10-08', at: '2026-10-07T23:00:07Z', phase: 'done', decision: 'ran', sha, run_id: 'nightly-x', result: '/r/result.json' },
      result: {
        finished: '2026-10-08T02:30:00Z', ok: false, stopped: false, secs: 12600, wall: '3h30m00s',
        counts: { passed: 1, failed: 2, skipped: 0, total: 3 },
        jobs: [
          { name: 'build', status: 'passed' },
          { name: 'e2e-chromium', status: 'failed', summary: 'e2e=1 1 failed', log: '/r/logs/e2e-chromium.log', load: load({ host_mem_full: 0.03, disk_write_ms: 10256 }) },
          { name: 'race-handlers-3', status: 'failed', summary: 'race=1 tests=210 failed=1 races=0', log: '/r/logs/race-handlers-3.log', load: load({}) },
        ],
        host: { budget_gb: 6.75, configured_gb: 8, budget_cut: true, mem_available_start_gb: 7.9, outside: 'android16 3.1 GiB', disk: 'dm-0', mem_full_max: 0.12, io_full_max: 0.71, disk_write_ms_max: 10256, temp_wait_secs: 0 },
      },
      history: [],
      nowMs: now,
      tz: 'Europe/Istanbul',
    });
    expect(r.message).toContain('host: budget 6.75 of 8 GiB (MemAvailable 7.9 GiB at the start; outside the chain: android16 3.1 GiB); worst memory full 0.12, io full 0.71, disk writes 10256 ms (dm-0)');
    const lines = r.message.split('\n');
    const red = lines.findIndex((l: string) => l.startsWith('RED e2e-chromium'));
    expect(lines[red + 2]).toBe("  the host stalled while it ran (disk writes 10256 ms): read a timeout there as the host's before the test's");
    // A red job the host did not stall under gets no such line.
    const race = lines.findIndex((l: string) => l.startsWith('RED race-handlers-3'));
    expect(lines[race + 2] ?? '').not.toMatch(/the host stalled/);
  });

  it('writes a LOAD line per job and a PRESSURE line at most once a minute, and keeps `host` in result.json', () => {
    const run = fs.readFileSync(path.join(CHAIN, 'run.mjs'), 'utf8');
    expect(run).toMatch(/if \(load\) this\.log\(loadLine\(job\.name, load\)\);/);
    expect(run).toMatch(/now - this\.lastPressureLog >= 60_000/);
    expect(run).toMatch(/host: this\.hostResult\(\),/);
    // The container's id, for its cgroup.
    expect(run).toMatch(/'run', '--rm', '--cidfile', this\.cidFile\(job\)/);
  });
});
