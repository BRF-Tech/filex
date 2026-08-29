// The Helm chart must ship the version we actually released.
//
// ⚠⚠ `deploy/helm/filex/values.yaml` ships `tag: ""`, and the `filex.image`
// helper resolves that to `.Chart.appVersion`. The chart's appVersion is
// therefore the image every Helm user runs — not a label on it.
//
// Measured 2026-08-29: appVersion sat at `v0.4.0` while the app was on v0.27.x.
// Twenty-three releases of fixes that a chart user never received, and nothing
// anywhere failed to mention it. `scripts/sync-chart-version.mjs` moves it; this
// test is why it cannot be forgotten.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const chart = readFileSync(path.join(REPO, 'deploy', 'helm', 'filex', 'Chart.yaml'), 'utf8');
const released = JSON.parse(
  readFileSync(path.join(REPO, 'web', 'package.json'), 'utf8'),
).version as string;

function field(name: string): string {
  const m = chart.match(new RegExp(`^${name}:\\s*"?([^"\\n]+)"?\\s*$`, 'm'));
  if (!m) throw new Error(`Chart.yaml has no ${name}`);
  return m[1]!.trim();
}

describe('helm chart version', () => {
  it('appVersion is the released version, with the v prefix the image tags use', () => {
    expect(field('appVersion')).toBe(`v${released}`);
  });

  it('the chart still leaves the image tag to appVersion — the reason this matters', () => {
    const values = readFileSync(path.join(REPO, 'deploy', 'helm', 'filex', 'values.yaml'), 'utf8');
    const tag = values.match(/^\s*tag:\s*"([^"]*)"/m);
    expect(tag, 'values.yaml should declare an image tag (empty = follow appVersion)').toBeTruthy();
    expect(
      tag![1],
      'a pinned tag here would silently outrank appVersion — if this is intentional, this test needs rewriting',
    ).toBe('');
  });

  it('the chart has its own version, and it is not the app version', () => {
    // Helm treats a chart as immutable at a given version: shipping different
    // contents under the same number makes `helm upgrade` unpredictable.
    expect(field('version')).toMatch(/^\d+\.\d+\.\d+$/);
    expect(field('version')).not.toBe(released);
  });
});
