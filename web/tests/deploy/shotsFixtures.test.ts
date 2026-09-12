// The screenshot scripts have to be able to seed the instance they photograph.
//
// ⚠⚠ They could not, for five releases. `sync_mode: 'manual'` was a valid value
// once; `ValidateSyncMode` arrived on 2026-09-07 and started refusing it, and
// from that day `node e2e/shots/capture.mjs` died on its first API call with
// `invalid sync_mode "manual"`. The release process has a numbered step that
// says to retake the screenshots, and v0.35.0, v0.36.0, v0.37.0, v0.38.0 and
// v0.38.1 all shipped over a script that could not take one.
//
// A screenshot script is not covered by any suite — it needs a browser and a
// running server — so nothing said a word. This test is the cheap half: it
// does not run the scripts, it checks that the values they hardcode are still
// values the server accepts, which is the only way they have ever broken.

import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');

/** The modes the sync worker implements, read from the Go source that decides. */
function implementedSyncModes(): string[] {
  const src = readFileSync(
    path.join(REPO, 'backend', 'internal', 'model', 'storage.go'),
    'utf8',
  );
  // func SyncModes() []SyncMode { return []SyncMode{SyncModePoll, …} }
  const body = /func SyncModes\(\)[^{]*\{[^}]*\{([^}]*)\}/.exec(src);
  expect(body, 'SyncModes() no longer has the shape this test reads').not.toBeNull();
  const names = body![1].split(',').map((s) => s.trim()).filter(Boolean);
  return names.map((name) => {
    const lit = new RegExp(`${name}\\s+SyncMode\\s*=\\s*"([^"]+)"`).exec(src);
    expect(lit, `no string literal for ${name}`).not.toBeNull();
    return lit![1];
  });
}

/** Every `sync_mode: '…'` a fixture script hardcodes, with the file it is in. */
function hardcodedSyncModes(): Array<{ file: string; value: string }> {
  const dirs = [path.join(REPO, 'e2e', 'shots'), path.join(REPO, 'e2e', 'helpers')];
  const out: Array<{ file: string; value: string }> = [];
  for (const dir of dirs) {
    for (const name of readdirSync(dir)) {
      if (!/\.(mjs|ts)$/.test(name)) continue;
      const src = readFileSync(path.join(dir, name), 'utf8');
      for (const m of src.matchAll(/sync_?[Mm]ode:\s*['"]([^'"]+)['"]/g)) {
        out.push({ file: path.relative(REPO, path.join(dir, name)), value: m[1] });
      }
    }
  }
  return out;
}

describe('the screenshot + e2e fixtures can still seed a storage', () => {
  it('every hardcoded sync_mode is one the server implements', () => {
    const valid = implementedSyncModes();
    expect(valid.length).toBeGreaterThan(0);

    const used = hardcodedSyncModes();
    expect(used.length, 'no fixture sets sync_mode any more — has the seed moved?')
      .toBeGreaterThan(0);

    for (const { file, value } of used) {
      expect(
        valid,
        `${file} seeds a storage with sync_mode "${value}", which the server refuses — ` +
          `the script dies on its first call and the release step that runs it reports nothing`,
      ).toContain(value);
    }
  });
});
