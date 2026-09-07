// Every packaged deployment target must name the version we actually released.
//
// ⚠⚠ These are not labels. Each one decides which image a real installation
// pulls: `deploy/helm/filex/values.yaml` ships `tag: ""` and the `filex.image`
// helper resolves it to `.Chart.appVersion`, while the CasaOS, Umbrel and
// Runtipi manifests pin the tag outright and use their `version` field to
// decide whether an update exists.
//
// Both halves have already gone wrong, the same way, because nothing failed:
//
//   2026-08-29  the Helm appVersion sat at `v0.4.0` while the app shipped
//               v0.27.x — twenty-three releases a chart user never received.
//   2026-09-06  the three app-store manifests were STILL at `v0.4.0`, twenty-
//               nine behind, because the first fix covered only the chart.
//               Anyone installing filex from CasaOS, Umbrel or Runtipi got a
//               build from February.
//
// `scripts/sync-deploy-versions.mjs` moves them; this test is why the next
// release cannot forget one of them.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (...p: string[]) => readFileSync(path.join(REPO, ...p), 'utf8');

const released = (JSON.parse(read('web', 'package.json')) as { version: string }).version;
const tag = `v${released}`;

/** The image tag pinned in a manifest, whatever its file format. */
function pinnedImage(file: string[]): string {
  const m = read(...file).match(/ghcr\.io\/brf-tech\/filex:(\S+?)["\s]/);
  if (!m) throw new Error(`${file.join('/')} pins no filex image`);
  return m[1]!;
}

describe('helm chart', () => {
  const chart = read('deploy', 'helm', 'filex', 'Chart.yaml');
  const field = (name: string) => {
    const m = chart.match(new RegExp(`^${name}:\\s*"?([^"\\n]+)"?\\s*$`, 'm'));
    if (!m) throw new Error(`Chart.yaml has no ${name}`);
    return m[1]!.trim();
  };

  it('appVersion is the released version, with the v prefix the image tags use', () => {
    expect(field('appVersion')).toBe(tag);
  });

  it('still leaves the image tag to appVersion — the reason this matters', () => {
    const values = read('deploy', 'helm', 'filex', 'values.yaml');
    const t = values.match(/^\s*tag:\s*"([^"]*)"/m);
    expect(t, 'values.yaml should declare an image tag (empty = follow appVersion)').toBeTruthy();
    expect(
      t![1],
      'a pinned tag here would silently outrank appVersion — if this is intentional, this test needs rewriting',
    ).toBe('');
  });

  it('has its own version, and it is not the app version', () => {
    // Helm treats a chart as immutable at a given version: shipping different
    // contents under the same number makes `helm upgrade` unpredictable.
    expect(field('version')).toMatch(/^\d+\.\d+\.\d+$/);
    expect(field('version')).not.toBe(released);
  });
});

describe('app store manifests', () => {
  it('CasaOS pins the released image and shows the released version', () => {
    expect(pinnedImage(['deploy', 'casaos', 'docker-compose.yml'])).toBe(tag);
    const compose = read('deploy', 'casaos', 'docker-compose.yml');
    // Two spaces: the key under `x-casaos:`, not a top-level one.
    expect(compose.match(/^ {2}version:\s*"([^"]+)"/m)?.[1]).toBe(released);
  });

  it('Umbrel pins the released image and shows the released version', () => {
    expect(pinnedImage(['deploy', 'umbrel', 'filex', 'docker-compose.yml'])).toBe(tag);
    expect(read('deploy', 'umbrel', 'filex', 'umbrel-app.yml').match(/^version:\s*"([^"]+)"/m)?.[1]).toBe(
      released,
    );
  });

  it('Runtipi pins the released image and shows the released version', () => {
    expect(pinnedImage(['deploy', 'runtipi', 'filex', 'docker-compose.json'])).toBe(tag);
    const cfg = JSON.parse(read('deploy', 'runtipi', 'filex', 'config.json')) as {
      version: string;
      tipi_version: number;
    };
    expect(cfg.version).toBe(released);
    // ⚠ `tipi_version` is the integer Runtipi compares to decide an update
    // exists. Moving the version string alone changes what the store displays
    // and offers nobody an upgrade — so it has to have moved past its 1.
    expect(cfg.tipi_version).toBeGreaterThan(1);
  });

  it('Umbrel ships release notes, and they are the ones this release earned', async () => {
    // ⚠ Not "has a releaseNotes field": one typed by hand and then forgotten
    // is the exact failure this guards against — a frozen "what's new" carries
    // no version number, so nothing about it looks stale. The blurb is derived
    // from CHANGELOG.md, and this asserts the manifest still equals what the
    // derivation produces, which is false the moment somebody releases without
    // re-running scripts/sync-deploy-versions.mjs.
    const { readNotesBlock, releaseNotes } = (await import(
      '../../../scripts/release-notes.mjs'
    )) as {
      readNotesBlock: (yaml: string) => string | null;
      releaseNotes: (changelog: string, version: string) => string | null;
    };
    const want = releaseNotes(read('CHANGELOG.md'), released);
    expect(want, `CHANGELOG.md has no "## [${released}]" section`).toBeTruthy();
    expect(readNotesBlock(read('deploy', 'umbrel', 'filex', 'umbrel-app.yml'))).toBe(want);
    // It must name the release: that is what would make a stale one visible.
    expect(want).toContain(`filex v${released}`);
  });

  it('does not rewrite the compose schema version while doing it', () => {
    // The Umbrel compose file also carries `version: "3.7"`. A pattern loose
    // enough to hit the app version would break the app rather than update it.
    expect(read('deploy', 'umbrel', 'filex', 'docker-compose.yml')).toMatch(/^version:\s*"3\.\d+"/m);
  });
});

// The body of the GitHub release page. Same derivation, different surface:
// GoReleaser's own body is a filtered `git log`, and on v0.34.2 that produced
// one line -- the release commit -- while CHANGELOG.md had 1,445 characters of
// prose for it. The workflow feeds this to `goreleaser --release-notes`.
describe('github release body', () => {
  const load = async () =>
    (await import('../../../scripts/release-notes.mjs')) as {
      githubReleaseBody: (
        changelog: string,
        version: string,
        opts?: { max?: number },
      ) => Promise<string | null>;
      changelogSection: (
        changelog: string,
        version: string,
      ) => { heading: string; body: string } | null;
    };

  it('carries the released version’s changelog prose, not a commit hash', async () => {
    const { githubReleaseBody } = await load();
    const body = await githubReleaseBody(read('CHANGELOG.md'), released);
    expect(body, `CHANGELOG.md has no "## [${released}]" section`).toBeTruthy();
    // Long enough to be prose. The failure this guards against produced about
    // sixty characters, so any threshold in this range separates the two.
    expect(body!.length).toBeGreaterThan(400);
    expect(body).toContain('## What changed');
    // ⚠ The header GoReleaser prepends already prints `## filex vX`, so
    // repeating it here would title the page twice.
    expect(body).not.toContain(`## filex v${released}`);
    expect(body).toContain('CHANGELOG.md#');
  });

  it('fails loudly for a version the changelog never mentions', async () => {
    const { githubReleaseBody } = await load();
    // null, so the workflow can stop the release before anything is published.
    // An empty string would be published, as an empty page.
    expect(await githubReleaseBody(read('CHANGELOG.md'), '99.99.99')).toBeNull();
  });

  it('caps a huge entry at a line boundary and links to the rest', async () => {
    // ⚠⚠ GoReleaser truncates a body over 125,000 characters SILENTLY, and
    // it truncates the END — which is where the footer's "Report a bug" link
    // is. v0.34.0's section alone is 57,408 characters, so the cap is real.
    const { githubReleaseBody, changelogSection } = await load();
    const body = (await githubReleaseBody(read('CHANGELOG.md'), released, { max: 800 }))!;
    expect(body.length).toBeLessThan(1600);
    expect(body).toContain('more to it than fits on one page');

    // ⚠ The strong assertion, and the one a naive `slice(0, max)` fails:
    // what survives has to be a PREFIX of the real section that ends exactly on
    // one of its own line breaks. A cut anywhere else lands mid-word or inside
    // a link, and the release page shows half a sentence.
    const section = changelogSection(read('CHANGELOG.md'), released)!.body.trim();
    const prose = body.split('## What changed\n\n')[1]!.split('\n\n**This release has more')[0]!;
    expect(section.startsWith(prose)).toBe(true);
    expect(section[prose.length]).toBe('\n');
  });
});
