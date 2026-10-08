// Which jobs of the chain may start now, and what a whole run would look like.
//
// Pure functions, no process and no Docker: scripts/chain/run.mjs asks
// `pickJobs` on every tick, and `simulate` replays a plan with expected
// durations through the very same decisions (run.mjs --plan, and
// web/tests/deploy/chainSchedule.test.ts, which holds the budget and the
// three-hour bound against the measured durations).
//
// The model:
//   - The browser round (the "track") runs one job at a time, in list order,
//     from the moment the build is green. While it runs it holds a fixed
//     share of the memory budget (its biggest job plus the Document Server).
//   - Every other job is in the "pool". A pool job starts when its needs have
//     passed, it fits the memory left over, and fewer than `maxJobs` pool jobs
//     are running. A job that does not fit RESERVES its weight, so the
//     smaller jobs behind it cannot keep taking the memory it is waiting for;
//     a job that cannot fit beside the round at all waits for the round's
//     share instead (pickJobs).
//   - The database sidecars hold their share until the last job that uses
//     them is settled.
//   - The budget itself is the host's: run.mjs takes CHAIN_MEM_GB, or less
//     when the host has less available at the start (hostBudget).

/** Memory left for pool jobs, in GiB. */
export function poolBudget({ memGb, trackActive, trackGb, dbUp, dbGb }) {
  return memGb - (trackActive ? trackGb : 0) - (dbUp ? dbGb : 0);
}

/**
 * The memory budget of a run, in GiB: `memGb` (CHAIN_MEM_GB), or less when
 * the host has less to give when the run starts - its MemAvailable before the
 * chain has started anything (`availGb`), less the `reserveGb` it keeps free
 * (CHAIN_MEM_RESERVE_GB), in quarter-GiB steps. `availGb` not finite (no
 * /proc/meminfo: `--plan` on another system) leaves `memGb`.
 *
 * ⚠ Task #194: CHAIN_MEM_GB=8 on a 14.8 GiB host whose Android emulator and
 * services left 7.9-8.4 GiB available: the chain was allowed every byte the
 * host had, the page cache its compilers and browsers read from included,
 * and the host stalled whenever the jobs reached their weights together. A
 * smaller budget makes a run longer (fewer -race jobs beside a browser), never
 * redder.
 */
export function hostBudget({ memGb, availGb, reserveGb = 0 }) {
  if (!Number.isFinite(availGb)) return { gb: memGb, cut: false };
  const room = Math.floor(Math.max(0, availGb - reserveGb) * 4) / 4;
  return room < memGb ? { gb: room, cut: true } : { gb: memGb, cut: false };
}

/** What the pool will have once the browser round ends: pickJobs' `ceiling`. */
export function poolCeiling({ memGb, dbUp, dbGb }) {
  return poolBudget({ memGb, trackActive: false, trackGb: 0, dbUp, dbGb });
}

/**
 * Split pending pool jobs into those whose needs have passed (`ready`, in
 * priority order) and those whose needs can no longer pass (`skip`).
 * `status` maps a job name to pending | running | passed | failed | skipped.
 */
export function readyJobs(pool, status) {
  const ready = [];
  const skip = [];
  for (const job of pool) {
    if (status[job.name] !== 'pending') continue;
    const needs = job.needs.map((n) => status[n]);
    if (needs.some((s) => s === 'failed' || s === 'skipped')) skip.push(job);
    else if (needs.every((s) => s === 'passed')) ready.push(job);
  }
  ready.sort((a, b) => a.prio - b.prio);
  return { ready, skip };
}

/**
 * The jobs to start now, in order. `running` is the pool jobs already
 * running; `budget` is what the pool has now (poolBudget) and `ceiling` what
 * it has once the browser round has let go of its share (poolBudget with the
 * round inactive; default: `budget`). The round always ends on its own; the
 * databases end only after their jobs, so the ceiling still holds their share.
 *
 *   - A job that fits starts.
 *   - A job that would fit once running pool jobs end RESERVES its weight:
 *     the lighter jobs behind it cannot keep taking that memory.
 *   - A job heavier than what the pool has now waits, while the round runs,
 *     until it fits (the databases let go) or the round ends, reserving
 *     nothing: a reservation would hold every job behind it for that long.
 *     ⚠ Before #194 such a job started "alone": the web gates (3 GiB) beside
 *     the round's 5 and the databases' 1 held 9 GiB of an 8 GiB budget - the
 *     opening minutes, when the host stalled in the 0.53 runs.
 *   - When the round holds nothing (budget = ceiling), a job heavier than the
 *     pool has starts when nothing else in the pool runs, and the pool drains
 *     for it: refusing it would stop the chain for good.
 */
export function pickJobs({ ready, running, budget, maxJobs, ceiling = budget }) {
  const EPS = 1e-9;
  let free = budget - running.reduce((sum, j) => sum + j.weight, 0);
  let slots = maxJobs - running.length;
  const start = [];
  for (const job of ready) {
    if (slots <= 0) break;
    if (job.weight > budget + EPS) {
      // More than the pool has now. While the round holds its share, wait
      // for it to end, reserving nothing.
      if (budget < ceiling - EPS) continue;
      // The pool has all it will get and the job is heavier still: it runs
      // alone, and the pool drains for it.
      if (running.length === 0 && start.length === 0) {
        start.push(job);
        slots -= 1;
      }
      free -= job.weight;
      if (free <= 0) break;
      continue;
    }
    if (free <= 0) break;
    if (job.weight <= free + EPS) {
      start.push(job);
      free -= job.weight;
      slots -= 1;
    } else {
      free -= job.weight;
    }
  }
  return start;
}

/**
 * Replay a plan through `pickJobs` with expected durations (minutes, from
 * `minutes(job)`), every job passing. Returns the wall time, when each job
 * ran, and the most memory the pool and the track held at once.
 */
export function simulate(plan, { memGb, trackGb, dbGb, poolMax, minutes, dsStart = 2 }) {
  const status = Object.fromEntries(plan.pool.map((j) => [j.name, 'pending']));
  const dbJobs = plan.pool.filter((j) => j.db).map((j) => j.name);
  const timeline = [];
  let t = 0;
  let running = [];
  let trackIndex = -1;
  let trackJob = null;
  let trackDone = plan.track.length === 0;
  let dsUp = false;
  let peak = 0;
  let maxRunning = 0;
  const settled = (n) => ['passed', 'failed', 'skipped'].includes(status[n]);

  for (let guard = 0; guard < 100000; guard += 1) {
    const buildPassed = plan.pool.filter((j) => j.kind === 'build').every((j) => status[j.name] === 'passed');
    if (buildPassed && !trackJob && !trackDone) {
      trackIndex += 1;
      if (trackIndex >= plan.track.length) {
        trackDone = true;
      } else {
        const job = plan.track[trackIndex];
        let start = t;
        if (job.ds && !dsUp) {
          dsUp = true;
          start += dsStart;
        }
        trackJob = { job, start, end: start + minutes(job) };
        timeline.push({ name: job.name, lane: 'track', start, end: trackJob.end });
      }
    }
    const trackActive = buildPassed && !trackDone;
    const dbUp = dbJobs.some((n) => !settled(n));
    const budget = poolBudget({ memGb, trackActive, trackGb, dbUp, dbGb });
    const ceiling = poolCeiling({ memGb, dbUp, dbGb });
    const { ready } = readyJobs(plan.pool, status);
    for (const job of pickJobs({ ready, running: running.map((r) => r.job), budget, ceiling, maxJobs: poolMax })) {
      status[job.name] = 'running';
      const r = { job, start: t, end: t + minutes(job) };
      running.push(r);
      timeline.push({ name: job.name, lane: 'pool', start: t, end: r.end });
    }
    const held = running.reduce((s, r) => s + r.job.weight, 0) + (trackActive ? trackGb : 0) + (dbUp ? dbGb : 0);
    peak = Math.max(peak, held);
    maxRunning = Math.max(maxRunning, running.length);

    const next = [...running.map((r) => r.end), ...(trackJob ? [trackJob.end] : [])];
    if (next.length === 0) {
      if (trackDone && Object.values(status).every((s) => s !== 'pending')) break;
      if (trackDone && readyJobs(plan.pool, status).ready.length === 0) break;
      if (!buildPassed) break;
      continue;
    }
    t = Math.min(...next);
    running = running.filter((r) => {
      if (r.end > t) return true;
      status[r.job.name] = 'passed';
      return false;
    });
    if (trackJob && trackJob.end <= t) {
      trackJob = null;
      if (plan.track.slice(trackIndex + 1).every((j) => !j.ds)) dsUp = false;
    }
  }
  return { wall: t, timeline, peakGb: peak, maxRunning };
}
