// A refused change is said inside the explorer AND emitted as `error` — and a
// host that shows its own error UI for that event can say "leave it to me"
// (`config.refusalToasts: false`), the same in every surface.
//
// ⚠ #59 made every refused mutation a toast inside the component (both
// first-party hosts wrote `error` to the console, so a refused paste looked
// like one that worked). An embedder that already answers `error` with its
// own message now showed two; nothing said so, and nothing let it choose.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const explorer = readFileSync(path.join(REPO, 'packages/core/src/FileExplorer.vue'), 'utf8');
const config = readFileSync(path.join(REPO, 'packages/core/src/types/ExplorerConfig.ts'), 'utf8');

function fn(name: string): string {
  const body = explorer.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}`))?.[0] ?? '';
  expect(body, `${name} was not found`).not.toBe('');
  return body;
}

describe('refused changes', () => {
  it('are toasted unless the host said it shows its own, and are always emitted', () => {
    const report = fn('reportMutationError');
    expect(report).toMatch(/const toastIt = props\.config\.refusalToasts !== false;/);
    expect(report).toMatch(/if \(held\) \{\s*if \(toastIt\) flashToast\(lockWords/);
    expect(report).toMatch(/else if \(!opts\.inDialog && toastIt\) showToast/);
    expect(report).toMatch(/emit\('error'/);
    expect(config).toMatch(/refusalToasts\?: boolean;/);
  });

  it('a permanent delete refused outright goes the same way (and is emitted too)', () => {
    const purge = fn('purgeSelection');
    expect(purge).toMatch(/refuseInDialog\(deleteReq, ticket, firstError, \{ op: 'purge' \}, reason \?\? t\('toast\.failed'\)\)/);
  });

  it('an app action that failed is said in the failure words, not its raw message', () => {
    const run = fn('runPluginAction');
    expect(run, 'the raw message of a failed app action').not.toMatch(/flashToast\(err\?\.message \|\|/);
    expect(run).toMatch(/showToast\(\{ message: failureText\(err, t\('plugin\.failed', \{ label \}\)\) \}, ERROR_TOAST_MS\)/);
  });

  it('are documented for embedders', () => {
    for (const doc of ['docs/API.md', 'docs/INTEGRATION.md']) {
      const text = readFileSync(path.join(REPO, doc), 'utf8');
      expect(text, doc).toMatch(/refusalToasts/);
    }
  });
});
