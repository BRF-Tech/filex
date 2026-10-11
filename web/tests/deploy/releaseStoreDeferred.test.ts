// #248: the Store copy's check off Windows - deferred by a person, never
// skipped, and owed until it ran.
//
// ⚠ Why: the release moved to a Linux workspace, and the pretag's Store gate
// (desktop/scripts/store-e2e.mjs) installs and activates an MSIX, which only
// Windows can do. It was a hard red there, with no way past it - and a "skip"
// would be the silent kind every gate of this tool exists to refuse. So on
// Windows the gate stays exactly as it was; anywhere else the pretag waits
// for `--ack store-deferred` (bound to the release commit like the audit's),
// the record keeps the deferral, and the deploy stage, --status and the ship
// list the steps a Windows machine takes until `--ack store-checked`.
// (The whole run, end to end: releaseCli.test.ts, "a gate this machine cannot run".)

import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { deferredSpecs, deferredWork, selectGates } from '../../../scripts/release/checks.mjs';
import plan from '../../../scripts/release/plan.mjs';
import { closeDeferred, deploy } from '../../../scripts/release/stages.mjs';
import { personLines, shipSettings } from '../../../scripts/train/ship.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const STORE = 'desktop: the Store package works as a Store copy';
const COMMIT = '0123456789abcdef0123456789abcdef01234567';

type Gate = {
  name: string;
  lane?: string;
  always?: boolean;
  after?: string[];
  sh?: string;
  env?: (c: object) => Record<string, string>;
  check?: () => { ok: boolean; detail?: string };
  deferred?: { ack: string; checkedAck: string; why: string; command: string; artifact?: string; later: (c: object) => string[] };
};
const storeGate = (platform: string) => (plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9', platform }).pretag as Gate[]).find((g) => g.name === STORE)!;

/** A record as the pretag writes it, for a release cut off Windows. */
function recordWithDeferral(checked: object | null = null) {
  const g = storeGate('linux');
  return {
    tag: 'v9.9.9',
    acks: { 'store-deferred': { head: COMMIT, at: '2026-10-11T18:00:00.000Z' } },
    stages: { gate: { status: 'done', dryRun: 'https://github.com/BRF-Tech/filex/actions/runs/42' } },
    deferred: {
      'store-deferred': {
        gate: g.name,
        ack: 'store-deferred',
        checkedAck: 'store-checked',
        why: g.deferred!.why,
        command: g.deferred!.command,
        head: COMMIT,
        at: '2026-10-11T18:00:00.000Z',
        later: g.deferred!.later({ tag: 'v9.9.9', releaseCommit: COMMIT }),
        artifact: g.deferred!.artifact,
        checked,
      },
    },
  };
}

describe('the Store copy, by the machine the release runs on', () => {
  it('on Windows it is required, as before: store-e2e runs against the binary the pretag built, and nothing defers it', () => {
    const g = storeGate('win32');
    expect(g.deferred).toBeUndefined();
    expect(g.always).toBe(true);
    expect(g.after).toContain('build: server binary');
    expect(g.sh).toBe('cd desktop && pnpm run build && node scripts/fetch-cli.mjs --platform win32 && node scripts/store-e2e.mjs');
    expect(g.env!({ repo: REPO }).FILEX_CLI_BIN).toBe(path.resolve(REPO, 'bin/filex-release-e2e.exe'));
    expect(deferredSpecs(plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9', platform: 'win32' }))).toEqual([]);
  });

  it('anywhere else it is deferred - never run, never skipped: a person acks it, and the record owes the Windows check', () => {
    for (const platform of ['linux', 'darwin']) {
      const g = storeGate(platform);
      expect(g.always, platform).toBe(true);
      expect(g.deferred, platform).toMatchObject({ ack: 'store-deferred', checkedAck: 'store-checked', artifact: 'release-files-store' });
      expect(g.deferred!.why).toContain("the Store package's activation is checked on a Windows machine");
      expect(g.deferred!.command).toContain('node scripts/store-e2e.mjs');
      // Anything that ran it anyway would be red, never a pass.
      expect(g.check!().ok).toBe(false);
    }
    const steps = storeGate('linux').deferred!.later({ tag: 'v9.9.9', releaseCommit: COMMIT }).join('\n');
    // the tag's commit, the builds the gate stands on, the Windows binary, the check itself
    expect(steps).toContain(`clean checkout of v9.9.9 (${COMMIT.slice(0, 10)})`);
    expect(steps).toContain("pnpm -r --filter './packages/*' build && pnpm --filter ./web build && node scripts/sync-embed.mjs");
    expect(steps).toContain('node scripts/build-backend.mjs --out bin/filex-release-e2e.exe');
    expect(steps).toContain('FILEX_CLI_BIN="$PWD/bin/filex-release-e2e.exe"');
    expect(steps).toContain('node scripts/fetch-cli.mjs --platform win32 && node scripts/store-e2e.mjs');
    // "ALL PASSED" alone also shows when the packaged app never opened.
    expect(steps).toContain("the Store copy's own checks");
  });

  it('a patch keeps it on both: always, whatever changed', () => {
    for (const platform of ['win32', 'linux']) {
      const p = plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9', platform });
      expect(selectGates(p, 'patch', []).pretag.map((g: { name: string }) => g.name), platform).toContain(STORE);
    }
  });
});

describe('what a deferred check owes, and who is told', () => {
  it('a record owes each deferral nobody recorded as done: the steps, what went out instead, and the ack that closes it', () => {
    expect(deferredWork(null, '9.9.9')).toEqual([]);
    expect(deferredWork({ deferred: {} }, '9.9.9')).toEqual([]);
    const owed = deferredWork(recordWithDeferral(), '9.9.9').join('\n');
    expect(owed).toContain(`${STORE}: the Store package's activation is checked on a Windows machine`);
    expect(owed).toContain('node scripts/store-e2e.mjs');
    expect(owed).toContain("the dry run's release-files-store (https://github.com/BRF-Tech/filex/actions/runs/42)");
    expect(owed).toContain('pnpm release 9.9.9 --resume --ack store-checked');
    expect(deferredWork(recordWithDeferral({ at: '2026-10-12T08:00:00.000Z' }), '9.9.9')).toEqual([]);
  });

  it('--ack store-checked records it; with nothing deferred it is refused, not taken', () => {
    const specs = deferredSpecs(plan({ repo: REPO, version: '9.9.9', tag: 'v9.9.9', platform: 'linux' }));
    expect(specs.map((s: { ack: string }) => s.ack)).toEqual(['store-deferred']);
    const state = recordWithDeferral();
    const r = closeDeferred(state, new Set(['store-checked']), specs);
    expect(r.problem).toBeNull();
    expect(r.closed).toHaveLength(1);
    expect(state.deferred['store-deferred'].checked).toMatchObject({ by: 'a person (--ack)' });
    // again: nothing more to close
    expect(closeDeferred(state, new Set(['store-checked']), specs).closed).toEqual([]);
    // not asked: nothing changes
    expect(closeDeferred(recordWithDeferral(), new Set(['deploy']), specs).closed).toEqual([]);
    const none = closeDeferred({ tag: 'v9.9.9' } as never, new Set(['store-checked']), specs);
    expect(none.problem).toContain('defers no "desktop: the Store package works as a Store copy"');
  });

  it('the deploy stage lists it as left to do, and does not wait for it', async () => {
    const R = {
      state: recordWithDeferral(),
      plan: { deployChecklist: ['1. deploy {tag}'], deployed: [] },
      version: '9.9.9',
      tag: 'v9.9.9',
      exp: '/x/filex-export',
      dry: false,
      acks: new Set<string>(),
      gates: { all: async () => true },
      ctx: () => ({}),
    };
    const waiting = await deploy(R);
    expect(waiting.status).toBe('waiting');
    const text = waiting.instructions.join('\n');
    expect(text).toContain('Left for another machine (deferred at the pretag; it does not hold the deploy)');
    expect(text).toContain('node scripts/store-e2e.mjs');
    expect(text).toContain('pnpm release 9.9.9 --resume --ack store-checked');
    // --ack deploy finishes the release with the check still owed
    const done = await deploy({ ...R, acks: new Set(['deploy']) });
    expect(done.status).toBe('done');
    // a check recorded as done is not listed
    const after = await deploy({ ...R, state: recordWithDeferral({ at: '2026-10-12T08:00:00.000Z' }) });
    expect(after.instructions.join('\n')).not.toContain('Left for another machine');
  });

  it("the ship's list of what is left to a person carries it", () => {
    const { cfg } = shipSettings({ SHIP_HOST: 'app-server', SHIP_INSTANCES: 'demo', SHIP_DEMO_DIR: '/srv/filex-demo', SHIP_DEMO_PORT: '5212' });
    const lines = personLines(cfg, { tag: 'v9.9.9', version: '9.9.9', release: recordWithDeferral() }).join('\n');
    expect(lines).toContain('left for another machine - the release deferred it at the pretag');
    expect(lines).toContain('node scripts/store-e2e.mjs');
    expect(lines).toContain('pnpm release 9.9.9 --resume --ack store-checked');
    expect(lines).toContain('pnpm release 9.9.9 --resume --ack deploy');
    expect(personLines(cfg, { tag: 'v9.9.9', version: '9.9.9' }).join('\n')).not.toContain('left for another machine');
  });
});
