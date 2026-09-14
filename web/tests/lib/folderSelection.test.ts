// A selection belongs to the listing it was made in.
//
// ⚠⚠ Found by the v0.41.0 screenshot pass (2026-09-14) and measured in a real
// browser: double-clicking into an encrypted folder left "1 selected" — with
// Download, Cut, Copy and DELETE — over the folder's lock screen, and those
// actions were aimed at the folder the person was now standing inside. Stepping
// back up with Alt+↑ brought the old row back selected.
//
// Two stale things carried it, and a fix that cleared only one would pass a
// plain-folder check and still fail on the lock screen:
//   • `selection` — `load()` never cleared it on a change of folder;
//   • `displayOrder` — the rows the view last drew. The lock screen (and the
//     not-found state) draw no view, so nothing replaced the PARENT's rows, and
//     `selection.nodes` kept resolving the double-clicked folder against them.
//     A plain folder hid the bug only because its own view re-published rows.
//
// FileExplorer has no mountable harness here (the core package has no test
// runner of its own, see splitOffered.test.ts), so this reads the one function
// every navigation goes through — a double-click, a crumb, Alt+↑, the address
// bar, a tab switch (applyTabLocation → load) — and pins that each place it
// commits a folder first settles the selection.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const EXPLORER = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

function body(signature: string): string {
  const start = EXPLORER.indexOf(signature);
  expect(start, `${signature} is gone — where do folders load now?`).toBeGreaterThanOrEqual(0);
  const end = EXPLORER.indexOf('\n}\n', start);
  return EXPLORER.slice(start, end);
}

describe('load() settles the selection when the folder changes', () => {
  const load = body('async function load(path?: string) {');

  it('clears BOTH the selection and the rows it resolves against', () => {
    const helper = load.match(/const arriveAt = \(to: string\) => \{([\s\S]*?)\n {2}\};/);
    expect(helper, 'arriveAt is gone').not.toBeNull();
    expect(helper![1]).toContain('selection.clear()');
    expect(helper![1]).toContain('displayOrder.value = []');
  });

  it('keeps it for a reload of the SAME folder', () => {
    const helper = load.match(/const arriveAt = \(to: string\) => \{([\s\S]*?)\n {2}\};/)![1];
    expect(helper).toMatch(/=== leaving\) return;/);
    expect(load).toMatch(/const leaving = String\(currentPath\.value/);
  });

  it('every commit of a folder goes through it — the listing, the drive root and a dead link', () => {
    // Everything after the virtual-view early return is a real folder load.
    const real = load.slice(load.indexOf('const leaving'));
    const commits = [...real.matchAll(/currentPath\.value = /g)].length;
    const arrivals = [...real.matchAll(/\barriveAt\(/g)].length;
    expect(commits).toBeGreaterThanOrEqual(2);
    // one arrival per currentPath commit, plus the not-found state
    expect(arrivals).toBe(commits + 1);
    expect(real).toMatch(/notFoundPath\.value = String\(requested\);\s*\n\s*arriveAt\(String\(requested\)\);/);
    // …and before the new rows land, so no frame shows the old selection over them
    expect(real.indexOf('arriveAt(arrived)')).toBeLessThan(real.indexOf('files.value = filterListing(resp.files)'));
  });

  it('the split pane follows the same rule on its own navigation', () => {
    const pane = body('function onPaneNavigate(p: string) {');
    expect(pane).toContain('splitSelection.clear()');
    expect(pane).toContain('splitDisplayOrder.value = []');
  });
});
