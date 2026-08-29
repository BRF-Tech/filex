#!/usr/bin/env node
// Keeps the Helm chart's appVersion in step with the release.
//
// ⚠⚠ This is not bookkeeping. `deploy/helm/filex/values.yaml` ships
// `tag: ""`, which the `filex.image` helper resolves to `.Chart.appVersion` —
// so the chart's appVersion IS the image everybody who installs the chart
// runs. It sat at `v0.4.0` while the app shipped v0.27.x (found 2026-08-29):
// twenty-three versions of fixes that a Helm user never got, with nothing
// failing to say so.
//
// The release process calls this; `web/tests/deploy/chartVersion.test.ts`
// fails the build if the two ever drift again.
//
//   node scripts/sync-chart-version.mjs          # rewrite Chart.yaml
//   node scripts/sync-chart-version.mjs --check  # exit 1 if it is behind
//
// The chart's OWN `version` is bumped too (patch) whenever appVersion moves:
// Helm treats a chart as immutable at a given version, so shipping different
// contents under the same number is what makes `helm upgrade` a coin toss.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const CHART = path.join(REPO, 'deploy', 'helm', 'filex', 'Chart.yaml');
// The release version, from the package the release step bumps. The repo root
// is private and stays at 0.1.0, so it cannot be the source of truth.
const SOURCE = path.join(REPO, 'web', 'package.json');

const check = process.argv.includes('--check');
const version = JSON.parse(fs.readFileSync(SOURCE, 'utf8')).version;
const want = `v${version}`;

const original = fs.readFileSync(CHART, 'utf8');
const appMatch = original.match(/^appVersion:\s*"?([^"\n]+)"?\s*$/m);
const verMatch = original.match(/^version:\s*([0-9]+)\.([0-9]+)\.([0-9]+)\s*$/m);
if (!appMatch || !verMatch) {
  console.error('sync-chart-version: Chart.yaml has no appVersion/version line to read');
  process.exit(2);
}

if (appMatch[1] === want) {
  console.log(`chart appVersion already ${want}`);
  process.exit(0);
}

if (check) {
  console.error(
    `chart appVersion is ${appMatch[1]}, the release is ${want} — ` +
      'anyone installing the chart would run the OLD image. ' +
      'Run: node scripts/sync-chart-version.mjs',
  );
  process.exit(1);
}

const chartVersion = `${verMatch[1]}.${Number(verMatch[2])}.${Number(verMatch[3]) + 1}`;
const updated = original
  .replace(/^appVersion:\s*"?[^"\n]+"?\s*$/m, `appVersion: "${want}"`)
  .replace(/^version:\s*[0-9]+\.[0-9]+\.[0-9]+\s*$/m, `version: ${chartVersion}`);
fs.writeFileSync(CHART, updated);
console.log(`chart appVersion ${appMatch[1]} -> ${want}, chart version ${verMatch[0].split(':')[1].trim()} -> ${chartVersion}`);
