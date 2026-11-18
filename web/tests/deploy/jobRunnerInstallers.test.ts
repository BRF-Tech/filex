// The build host's timers and its job runner (task #362).
//
// ⚠ 2026-11-18: the build host's jobs - the nightly and its report, the
// language packs' night, the release round and its report - moved from their
// systemd timers to the host's job runner, which starts the jobs its list
// names (/etc/brf-watchdog/jobs.enabled) and logs each to
// /var/log/brf-jobs/<id>.log. An installer run with --enable after that turned
// a timer back on beside the runner, and the job would have run twice. These
// tests hold the installers to scripts/lib/job-runner.sh: a job the list names
// keeps its timer off, and --enable is refused for it.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { NIGHTLY_LOG_WORDS, composeReport } from '../../../scripts/chain/nightly-lib.mjs';
import { findBash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (rel: string) => fs.readFileSync(path.join(REPO, rel), 'utf8').replace(/\r\n/g, '\n');
const LIB = path.join(REPO, 'scripts', 'lib', 'job-runner.sh').replace(/\\/g, '/');
const bash = findBash();

function withHelper(script: string, env: Record<string, string> = {}) {
  const r = spawnSync(bash!, ['-c', `set -euo pipefail\n. "$LIB"\n${script}`], {
    encoding: 'utf8',
    env: { ...process.env, LIB, ...env },
    windowsHide: true,
  });
  return { code: r.status, out: (r.stdout ?? '').replace(/\r\n/g, '\n'), err: (r.stderr ?? '').replace(/\r\n/g, '\n') };
}

function tempList(text: string) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'jobrunner-'));
  const file = path.join(dir, 'jobs.enabled');
  fs.writeFileSync(file, text);
  return { dir, file: file.replace(/\\/g, '/') };
}

const INSTALLERS = [
  { file: 'scripts/chain/install-nightly.sh', who: 'install-nightly', helper: '. "$HERE/../lib/job-runner.sh"', ids: ['filex-nightly', 'filex-nightly-report'] },
  { file: 'scripts/chain/install-langpacks.sh', who: 'install-langpacks', helper: '. "$SCRIPTS/lib/job-runner.sh"', ids: ['filex-langpacks'] },
  { file: 'scripts/train/install-auto.sh', who: 'install-auto', helper: '. "$SCRIPTS/lib/job-runner.sh"', ids: ['filex-release-auto', 'filex-release-auto-report'] },
];

describe("the job runner's list (scripts/lib/job-runner.sh)", () => {
  it('is the build host runner\'s own list unless --runner-list names another', () => {
    expect(withHelper('echo "$JOB_RUNNER_LIST_DEFAULT"').out.trim()).toBe('/etc/brf-watchdog/jobs.enabled');
  });

  it('names the jobs the list holds, whole ids only, past comments and spaces', () => {
    const { dir, file } = tempList('# what this host runs\nfilex-nightly\n  filex-langpacks   # the packs\nfilex-nightly-report-old\n\nbrf-runner\n');
    try {
      const r = withHelper(`runner_jobs "${file}" filex-nightly filex-nightly-report filex-langpacks filex-release-auto`);
      expect(r.code, r.err).toBe(0);
      expect(r.out).toBe('filex-nightly\nfilex-langpacks\n');
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  it('names none on a host without the list, and does not fail there', () => {
    const r = withHelper('runner_jobs /nonexistent/jobs.enabled filex-nightly; echo "rc=$?"');
    expect(r.out).toBe('rc=0\n');
  });

  it('says why the timers stay off, on stderr, and how to reach the jobs the runner starts', () => {
    const refuse = withHelper('runner_refuse install-nightly /etc/brf-watchdog/jobs.enabled filex-nightly filex-nightly-report');
    expect(refuse.out).toBe('');
    expect(refuse.err).toContain('install-nightly: /etc/brf-watchdog/jobs.enabled names filex-nightly filex-nightly-report');
    expect(refuse.err).toContain('a timer would start each one a second time');
    const hints = withHelper('runner_hints install-nightly filex-nightly filex-nightly-report').out;
    expect(hints).toContain('docker exec brf-watchdog watchdog jobs status');
    for (const id of ['filex-nightly', 'filex-nightly-report']) {
      expect(hints).toContain(`docker exec brf-watchdog watchdog jobs run ${id}; tail -f /var/log/brf-jobs/${id}.log`);
    }
  });
});

describe('the installers', () => {
  for (const ins of INSTALLERS) {
    it(`${ins.file}: refuses --enable for a job the runner starts, before any timer is enabled`, () => {
      const sh = read(ins.file);
      expect(sh).toContain(ins.helper);
      expect(sh).toMatch(/^RUNNER_LIST="\$JOB_RUNNER_LIST_DEFAULT"$/m);
      expect(sh).toContain('--runner-list) RUNNER_LIST="${2:?--runner-list needs a file}"; shift 2 ;;');
      const guard =
        `OWNED="$(runner_jobs "$RUNNER_LIST" ${ins.ids.join(' ')})"\n` +
        `if [ "$ENABLE" = 1 ] && [ -n "$OWNED" ]; then\n` +
        `  runner_refuse ${ins.who} "$RUNNER_LIST" $OWNED\n` +
        `  exit 3\n` +
        `fi\n` +
        `if [ "$ENABLE" = 1 ]; then\n`;
      expect(sh).toContain(guard);
      // Before the only `systemctl enable` the installer runs.
      expect(sh.match(/^\s+run systemctl enable --now /gm)).toHaveLength(1);
      expect(sh.indexOf(guard)).toBeLessThan(sh.indexOf('  run systemctl enable --now'));
      // The ids are the units' own names: a job the list names is the timer refused.
      for (const id of ins.ids) expect(sh).toContain(`${id}.timer`);
      // Where the runner starts them, the hints are the runner's, not systemctl's.
      expect(sh).toMatch(/elif \[ -n "\$OWNED" \]; then\n(?:\s+say [^\n]*\n)*\s+runner_hints [a-z-]+ \$OWNED\n/);
    });
  }

  it.runIf(process.platform === 'linux' && ['git', 'docker', 'systemctl', 'python3'].every((t) => spawnSync('sh', ['-c', `command -v ${t}`]).status === 0))(
    'install-nightly.sh --enable --dry-run stops at exit 3 where the list names the night',
    () => {
      const { dir, file } = tempList('filex-nightly\n');
      try {
        const r = spawnSync(
          'bash',
          [path.join(REPO, 'scripts', 'chain', 'install-nightly.sh'), '--root', path.join(dir, 'root'), '--env', path.join(dir, 'x.env'), '--remote', 'https://example.com/x.git', '--unit-dir', dir, '--runner-list', file, '--enable', '--dry-run'],
          { encoding: 'utf8' },
        );
        expect(r.status, r.stdout + r.stderr).toBe(3);
        expect(r.stderr).toContain(`${file} names filex-nightly`);
        expect(r.stdout).not.toContain('+ systemctl enable');
      } finally {
        fs.rmSync(dir, { recursive: true, force: true });
      }
    },
  );
});

describe("the morning report names the night's log where the runner keeps it", () => {
  it('when there is no record, and when the night died', () => {
    expect(NIGHTLY_LOG_WORDS).toContain('/var/log/brf-jobs/filex-nightly.log');
    expect(NIGHTLY_LOG_WORDS).toContain('journalctl -u filex-nightly.service');
    const now = Date.parse('2026-11-19T04:00:00Z');
    const none = composeReport({ tonight: null, history: [], nowMs: now });
    expect(none.message).toContain(`Look at: ${NIGHTLY_LOG_WORDS}`);
    expect(none.message).not.toMatch(/its timer did not fire/);
    const died = composeReport({
      tonight: { at: '2026-11-18T23:00:00Z', night: '2026-11-19', sha: '1a2b3c4d5e', phase: 'export', pid: 42 },
      history: [],
      running: false,
      nowMs: now,
    });
    expect(died.message).toContain(`it died or was killed - ${NIGHTLY_LOG_WORDS}`);
  });
});
