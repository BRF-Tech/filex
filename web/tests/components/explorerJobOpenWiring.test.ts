// The explorer follows a finished app job's `open` (filex #78) - wiring in
// FileExplorer.vue, which is far too large to mount here, so it is read as
// source (the way queuedRenameRestoreWiring.test.ts reads it). The decision
// itself is lib/jobOpen (tests/lib/jobOpen.test.ts); the page that queued a
// job is measured mounted (pluginPageFollowsJob.test.ts) and in the browser
// (e2e 192).
//
// A dialog queued the job and closed; when the job ends with an `open`, the
// explorer opens that screen on the output - the dialog flow's half of "Convert
// to PDF, then the signing wizard on the PDF".
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');
const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');

function fn(name: string): string {
  const body = explorer.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}`))?.[0] ?? '';
  expect(body, `${name} was not found`).not.toBe('');
  return body;
}

describe("the explorer follows a finished job's open", () => {
  const settled = explorer.match(/onSettled: \(op: PendingOp\) => \{[\s\S]*?\n  \},\n\}\);/)?.[0] ?? '';

  it('asks lib/jobOpen, not a copy of its rule, and never over an open screen', () => {
    expect(settled, 'the onSettled handler was not found').not.toBe('');
    expect(settled).toMatch(/jobOpenOf\(op, \{ busy: !!pluginView\.value \|\| !!appFrameView\.value \}\)/);
    expect(settled).not.toMatch(/op\.open &&/);
  });

  it('opens the screen through onSurfaceOpen - the folder, then the app screen on the file', () => {
    expect(settled).toMatch(/onSurfaceOpen\(go\.plugin, go\.open\)/);
    const opener = fn('onSurfaceOpen');
    expect(opener).toMatch(/await load\(target\)/);
    expect(opener).toMatch(/openAppTarget\(/);
  });

  it('a page screen that gets no tab opens in the dialog, as the blocked-tab toast says', () => {
    // Its parameter type spans lines and ends in a `}` at column 0, so the
    // body is read from the line that opens it.
    const target = explorer.match(/async function openAppTarget\([\s\S]*?\): Promise<boolean> \{[\s\S]*?\n\}\n/)?.[0] ?? '';
    expect(target, 'openAppTarget was not found').not.toBe('');
    // The page branch returns only when the tab really opened...
    expect(target).toMatch(/isPagePlacement\(owner\.view_placement\) && openPluginPage\(owner, \[node\]\)\) \{\s*return true;/);
    // ...and the dialog below is reached otherwise.
    expect(target).not.toMatch(/return openPluginPage\(owner, \[node\]\);/);
    expect(target).toMatch(/api\.pluginView\(p\.plugin, p\.view, p\.path\)/);
  });
});
