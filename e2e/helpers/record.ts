/**
 * What a Playwright run records of its tests: `E2E_RECORD`, read by
 * e2e/playwright.config.ts. Pure, so web/tests/deploy/chainSchedule.test.ts
 * holds it.
 *
 *   failures (the default)  every test is recorded - trace and video - and a
 *                           test that passes has its recording deleted. A
 *                           failure's FIRST attempt is on record: task #81
 *                           needed exactly that (was the missing chunk
 *                           requested, pending or refused?).
 *   retry                   nothing is recorded on a test's first attempt. A
 *                           test that fails runs once more, recorded, and the
 *                           run is red all the same: a test that passes only
 *                           on its second attempt is flaky, and
 *                           failOnFlakyTests makes a flaky test a failure.
 *
 * ⚠⚠ Task #194, measured on the build host on 2026-10-15 (the same tree, the
 * whole suite on Chromium, 634 tests): the one Playwright worker that runs a
 * line's specs grew to 3.4 GiB recording every test, and the line held 7.15
 * GiB (memory.peak 7.38) in 27.9 minutes; with nothing recorded the worker
 * was 1.2 GiB at the end and the line held 4.15 GiB, in 23.5 minutes. The
 * recordings are deleted when a test passes; what the worker keeps of them is
 * not. The build host's chain runs `retry` (scripts/chain/run.mjs
 * CHAIN_E2E_RECORD); a run on a person's machine and GitHub's keep
 * `failures`.
 */
export type RecordMode = 'failures' | 'retry';

export const RECORD_MODES: readonly RecordMode[] = ['failures', 'retry'];

export interface RecordSettings {
  mode: RecordMode;
  retries: number;
  failOnFlakyTests: boolean;
  trace: 'retain-on-failure' | 'on-first-retry';
  video: 'retain-on-failure' | 'on-first-retry';
}

/**
 * The settings of a mode (E2E_RECORD; empty is `failures`). `ci` is a run on
 * a CI service, which retries a failed test once in `failures` too.
 * ⚠ A mode this suite does not know stops the run: a typo that fell back to
 * the default would record every test on a host that cannot hold it.
 */
export function recordSettings(raw: string | undefined, { ci = false }: { ci?: boolean } = {}): RecordSettings {
  const mode = String(raw ?? '').trim() || 'failures';
  if (!(RECORD_MODES as readonly string[]).includes(mode)) {
    throw new Error(`E2E_RECORD: "${mode}" is none of ${RECORD_MODES.join(', ')}`);
  }
  if (mode === 'retry') {
    return { mode, retries: 1, failOnFlakyTests: true, trace: 'on-first-retry', video: 'on-first-retry' };
  }
  return { mode: 'failures', retries: ci ? 1 : 0, failOnFlakyTests: false, trace: 'retain-on-failure', video: 'retain-on-failure' };
}
