#!/usr/bin/env node
// A nightly snap to the Snap Store's edge channel, from GitHub's arm64
// runner (task #266, .github/workflows/nightly-desktop.yml): edge only, never
// a release's channels.
//
//   node scripts/chain/nightly-snap-edge.mjs <snap file> <result.json>
//
// The credential is SNAPCRAFT_STORE_CREDENTIALS (the repository's secret, the
// one release.yml uploads with), handed to snapcraft in its environment and
// never printed. Without it the snap is not uploaded and the run says
// "skipped: no Snap Store credential" - a line, not a red run. The outcome
// goes to <result.json> ({ state: released | review | failed | skipped,
// revision?, why? }), which the night's manifest carries: the build host's
// morning report says it, and says every morning after it until edge serves
// the revision (snapEdgeState) - a review that never ends is not quiet.
//
// Exit: 0 released, held for a review, or skipped; 1 the upload failed; 2 usage.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { snapUploadVerdict } from './nightly-lib.mjs';

/** The line the run prints, and its GitHub annotation, for an outcome. */
export function edgeSummary(v, name) {
  if (v.state === 'skipped') return { line: v.why, annotation: `::notice title=Snap Store::${v.why}` };
  if (v.state === 'released') return { line: `${name} is on edge${v.revision ? ` (revision ${v.revision})` : ''}`, annotation: '' };
  if (v.state === 'review') return { line: `${name} is uploaded and held for the Snap Store's review${v.revision ? ` (revision ${v.revision})` : ''}`, annotation: `::warning title=Snap Store: review::${name} waits for the Snap Store's review` };
  return { line: `${name} was NOT uploaded: ${v.why ?? 'see above'}`, annotation: `::error title=Snap Store::${name} was not uploaded to edge` };
}

function main(argv) {
  const [file, out] = argv;
  if (!file || !out) {
    console.error('usage: node scripts/chain/nightly-snap-edge.mjs <snap file> <result.json>');
    return 2;
  }
  const name = path.basename(file);
  const creds = String(process.env.SNAPCRAFT_STORE_CREDENTIALS ?? '').trim();
  let v;
  if (!creds) {
    v = { state: 'skipped', why: 'skipped: no Snap Store credential (SNAPCRAFT_STORE_CREDENTIALS is not set)' };
  } else if (!fs.existsSync(file)) {
    v = { state: 'failed', why: `there is no ${name} to upload` };
  } else {
    const r = spawnSync(process.env.SNAPCRAFT || 'snapcraft', ['upload', '--release=edge', file], {
      encoding: 'utf8',
      env: { ...process.env, SNAPCRAFT_STORE_CREDENTIALS: creds },
      maxBuffer: 16 * 1024 * 1024,
      timeout: 45 * 60_000,
    });
    const text = `${r.stdout ?? ''}\n${r.stderr ?? ''}`.split(creds).join('<credential>');
    process.stdout.write(text);
    v = snapUploadVerdict(r.status ?? (r.error ? 127 : 1), r.error ? `${text}\n${r.error.message}` : text);
  }
  fs.writeFileSync(out, `${JSON.stringify(v)}\n`);
  const s = edgeSummary(v, name);
  console.log(s.line);
  if (s.annotation) console.log(s.annotation);
  return v.state === 'failed' ? 1 : 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
