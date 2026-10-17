// The Snap Store's channels follow every release (#265): one upload sends the
// release's revision to stable, candidate, beta and edge, and the release
// tool reads stable, candidate and beta back - telling a person at once when
// one lags, because a revision the Store holds for its review is not ours to
// hurry and is found by nobody otherwise.
//
// ⚠ Why (2026-10-15, measured on api.snapcraft.io): stable served 0.56.3 on
// amd64 and arm64, and candidate, beta and edge still served 0.52.0 rev 14 -
// amd64 only, arm64 had never had a revision there. Every release since
// 0.52.0 had gone to stable alone, so `snap install filex-app --candidate`
// installed a release four minors old, and nothing in the release said so.
//
// The workflow lives in the PUBLIC checkout only (releaseGatesImages.test.ts):
// the change reaches it as packaging/ci/release-snap-channels.patch, and the
// release tool names this file's workflow test in its guards
// (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there is red.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import plan, { snapHeld } from '../../../scripts/release/plan.mjs';
import { heldNotices } from '../../../scripts/release/stages.mjs';
import { SNAP_CHECKED_CHANNELS, SNAP_RELEASE_CHANNELS, snapChannel, snapChannelVerdict } from '../../../scripts/release/verify.mjs';
import { DIR, REPO, SNAP_UPLOAD, code, job, stepNamed } from '../helpers/releaseWorkflow';

const VERSION = '9.9.9';
const TAG = `v${VERSION}`;

/** A channel map as api.snapcraft.io/v2/snaps/info gives it. */
const entry = (name: string, architecture: string, version: string, revision: number) => ({ channel: { name, architecture }, version, revision });
const everywhere = (version: string) =>
  SNAP_RELEASE_CHANNELS.flatMap((ch, i) => [entry(ch, 'amd64', version, 40 + i), entry(ch, 'arm64', version, 50 + i)]);

/** A fetch that answers the one URL with that channel map. */
const store = (map: unknown[], status = 200) => async (url: string, init?: { headers?: Record<string, string> }) => {
  expect(url).toBe('https://api.snapcraft.io/v2/snaps/info/filex-app?fields=version,revision');
  expect(init?.headers?.['Snap-Device-Series']).toBe('16');
  return new Response(JSON.stringify({ 'channel-map': map }), { status });
};

describe('the Snap Store channels of a release (#265)', () => {
  it.runIf(!!DIR)('sends each Linux snap to stable, candidate, beta and edge in one upload, and says so', () => {
    const desktop = job(code(), 'desktop');
    const s = stepNamed(desktop, 'Upload to the Snap Store');
    // One command, every channel: a release never leaves a preview channel behind.
    expect(s.text).toMatch(SNAP_UPLOAD);
    expect(s.text.match(/snapcraft upload/g)).toHaveLength(1);
    expect(s.text).not.toMatch(/--release=stable "/);
    // The summary and the manual-review warning name the four.
    expect(s.text).toContain('uploaded to stable, candidate, beta and edge');
    expect(s.text).toMatch(/reaches stable, candidate, beta and edge once the review passes/);
    // Both architectures, each from its own row, as before.
    expect(s.text).toMatch(/snap=desktop\/release\/filex-desktop-amd64\.snap/);
    expect(s.text).toMatch(/\[ "\$LABEL" = linux-arm64 \] && snap=desktop\/release\/filex-desktop-arm64\.snap/);
  });

  it('the workflow patch changes the upload and its words, and nothing else', () => {
    const patch = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'release-snap-channels.patch'), 'utf8');
    expect(patch).toMatch(/^diff --git a\/\.github\/workflows\/release\.yml b\/\.github\/workflows\/release\.yml$/m);
    const changed = patch.split('\n').filter((l) => /^[+-](?![+-])/.test(l));
    const commands = changed.filter((l) => !/^[+-]\s*#/.test(l));
    expect(commands).toEqual([
      '-          out=$(snapcraft upload --release=stable "$snap" 2>&1)',
      '+          out=$(snapcraft upload --release=stable,candidate,beta,edge "$snap" 2>&1)',
      expect.stringMatching(/^-\s+msg=.*it reaches stable once the review passes/),
      expect.stringMatching(/^\+\s+msg=.*it reaches stable, candidate, beta and edge once the review passes/),
      expect.stringMatching(/^-\s+echo "### Snap Store: .* uploaded to stable" >>/),
      expect.stringMatching(/^\+\s+echo "### Snap Store: .* uploaded to stable, candidate, beta and edge" >>/),
    ]);
    // CONTRIBUTING names it in the order the patches apply.
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n8\. `release-snap-channels\.patch` \(#265/);
  });

  it('reads stable, candidate and beta back for both architectures, and names each that lags', async () => {
    expect(SNAP_RELEASE_CHANNELS).toEqual(['stable', 'candidate', 'beta', 'edge']);
    // Not edge: the nightly run moves it on after a release.
    expect(SNAP_CHECKED_CHANNELS).toEqual(['stable', 'candidate', 'beta']);
    const gate = snapChannel('snap', 'filex-app', ['amd64', 'arm64'], { channels: SNAP_CHECKED_CHANNELS, fetch: store(everywhere(VERSION)) });
    const green = await gate.check({ version: VERSION });
    expect(green.ok).toBe(true);
    for (const ch of SNAP_CHECKED_CHANNELS) for (const a of ['amd64', 'arm64']) expect(green.detail).toContain(`${ch}/${a}: ${VERSION}`);

    // What 2026-10-15 looked like: stable moved, the preview channels did not.
    const stale = [entry('stable', 'amd64', VERSION, 29), entry('stable', 'arm64', VERSION, 28), entry('candidate', 'amd64', '0.52.0', 14), entry('beta', 'amd64', '0.52.0', 14)];
    const red = await snapChannel('snap', 'filex-app', ['amd64', 'arm64'], { channels: SNAP_CHECKED_CHANNELS, fetch: store(stale) }).check({ version: VERSION });
    expect(red.ok).toBe(false);
    expect(red.detail.split('\n')).toEqual([
      `candidate/amd64: 0.52.0 (revision 14), not ${VERSION}`,
      'candidate/arm64: no revision',
      `beta/amd64: 0.52.0 (revision 14), not ${VERSION}`,
      'beta/arm64: no revision',
    ]);
    expect(snapChannelVerdict(stale, { channels: ['stable'], arches: ['amd64', 'arm64'], version: VERSION }).lagging).toEqual([]);

    // One channel, as every caller before #265 asked: still the default.
    expect((await snapChannel('snap', 'filex-app', ['amd64'], { fetch: store(stale) }).check({ version: VERSION })).ok).toBe(true);
    // Could not ask is not a pass.
    const down = await snapChannel('snap', 'filex-app', ['amd64'], { fetch: store([], 503) }).check({ version: VERSION });
    expect(down).toEqual({ ok: false, detail: expect.stringMatching(/answered HTTP 503/) });
  });

  it("the release's Snap Store gate reads those channels, and is one a store holds (held)", () => {
    const p = plan({ repo: REPO, version: VERSION, tag: TAG });
    const gate = p.published.find((g: { name: string }) => g.name.startsWith('Snap Store: filex-app'));
    expect(gate.name).toBe(`Snap Store: filex-app ${VERSION} on stable, candidate, beta for amd64 and arm64`);
    expect(typeof gate.held).toBe('function');
    // No other published gate says a store holds it: a red GitHub Release or
    // image is ours, and stops the release as before.
    expect(p.published.filter((g: { held?: unknown }) => g.held)).toHaveLength(1);
  });

  it('a lagging channel is said to a person at once, with what to look at and how to release a revision by hand', () => {
    const p = plan({ repo: REPO, version: VERSION, tag: TAG });
    const gate = p.published.find((g: { name: string }) => g.name.startsWith('Snap Store: filex-app'));
    const detail = 'candidate/arm64: no revision';
    const results = [
      { stage: 'ci', name: 'GitHub Release v9.9.9 carries the installers, the feeds and the CLI', ok: true, detail: '40 assets' },
      { stage: 'ci', name: gate.name, ok: false, detail },
    ];
    const notices = heldNotices('ci', p.published, results);
    expect(notices).toHaveLength(1);
    expect(notices[0]).toMatchObject({ gate: gate.name, result: 'waiting', title: `filex ${TAG}: the Snap Store does not offer ${VERSION} on every channel yet` });
    expect(notices[0].message).toContain(detail);
    expect(notices[0].message).toContain('snapcraft status filex-app');
    expect(notices[0].message).toContain('snapcraft release filex-app <revision> stable,candidate,beta,edge');
    expect(notices[0].message).toContain(`pnpm release ${VERSION} --resume --only deploy`);
    expect(snapHeld(TAG, VERSION, { detail }).message).toBe(notices[0].message);

    // Green says nothing; a red gate that is ours is no notice of this kind;
    // only the newest record of a gate counts (a resume that turned it green).
    expect(heldNotices('ci', p.published, [{ stage: 'ci', name: gate.name, ok: true, detail: '' }])).toEqual([]);
    expect(heldNotices('ci', p.published, [{ stage: 'ci', name: 'npm: every package', ok: false, detail: 'missing' }])).toEqual([]);
    expect(heldNotices('ci', p.published, [{ stage: 'ci', name: gate.name, ok: false, detail }, { stage: 'ci', name: gate.name, ok: true, detail: '' }])).toEqual([]);
    expect(heldNotices('deploy', p.published, results)).toEqual([]);
  });

  it('the ci stage sends the notices through the train settings, and records when only a store holds it', () => {
    const stages = fs.readFileSync(path.join(REPO, 'scripts', 'release', 'stages.mjs'), 'utf8');
    const ci = stages.slice(stages.indexOf('export async function ci(R)'), stages.indexOf('// ── 12. deploy'));
    expect(ci).toMatch(/const published = ofThisRelease\(R, R\.plan\.published\);/);
    expect(ci).toMatch(/heldNotices\('ci', published, R\.gates\.results\)/);
    expect(ci).toMatch(/await sayHeld\(R, held\)/);
    expect(ci).toMatch(/return red\(onlyHeld \? \{ held: held\.map\(\(h\) => h\.gate\) \} : \{\}\)/);
    expect(stages).toMatch(/loadSettings\(\{ envVar: 'FILEX_TRAIN_ENV', fallback: 'filex-train\.env' \}\)/);
    expect(stages).toMatch(/import \{ announce \} from '\.\.\/lib\/notify\.mjs';/);
  });
});
