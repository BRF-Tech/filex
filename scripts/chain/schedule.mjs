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
//     smaller jobs behind it cannot keep taking the memory it is waiting for.
//   - The database sidecars hold their share until the last job that uses
//     them is settled.

/** Memory left for pool jobs, in GiB. */
export function poolBudget({ memGb, trackActive, trackGb, dbUp, dbGb }) {
  return memGb - (trackActive ? trackGb : 0) - (dbUp ? dbGb : 0);
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
 * running. A job heavier than the whole budget still starts when nothing
 * else in the pool runs: refusing it would stop the chain for good.
 */
export function pickJobs({ ready, running, budget, maxJobs }) {
  let free = budget - running.reduce((sum, j) => sum + j.weight, 0);
  let slots = maxJobs - running.length;
  const start = [];
  for (const job of ready) {
    const alone = running.length === 0 && start.length === 0;
    if (slots <= 0 || (free <= 0 && !alone)) break;
    if (job.weight <= free + 1e-9 || alone) {
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
    const { ready } = readyJobs(plan.pool, status);
    for (const job of pickJobs({ ready, running: running.map((r) => r.job), budget, maxJobs: poolMax })) {
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
