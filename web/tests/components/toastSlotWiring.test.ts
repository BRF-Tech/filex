// How the explorer uses its one toast slot (lib/toastSlot) — read off
// FileExplorer.vue, which is too large to mount here (like the other explorer
// wiring tests). The rules themselves are tests/lib/toastSlot.test.ts.
//
// ⚠ #68: a drag-out's Stop sat in the Undo slot — "Undoing…", "Undone", and
// the listing read again — and the drag's per-file progress replaced any
// other toast within milliseconds, an 8 s Undo included.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const explorer = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

function fn(name: string): string {
  const body = explorer.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}`))?.[0] ?? '';
  expect(body, `${name} was not found`).not.toBe('');
  return body;
}

const dragHandler = explorer.slice(
  explorer.indexOf('dragOut.value?.onProgress?.((p) => {'),
  explorer.indexOf('/* wiring:f1 — an OS drag never fires'),
);

describe('the toast action', () => {
  it('says what KIND of action it ran: an Undo reads the listing again, a plain action does not', () => {
    const run = fn('runToastAction');
    expect(run).toMatch(/afterActionWords\(after, 'running', undefined, t\)/);
    expect(run).toMatch(/afterActionWords\(after, 'done', out, t\)/);
    expect(run).toMatch(/if \(!plain\) await load\(\);/);
    expect(run, 'Undone flashed for every action').not.toMatch(/flashToast\(sayUndo\(/);
  });

  it('a drag-out’s Stop is a plain action with its own words', () => {
    expect(dragHandler).toMatch(
      /after: \{ kind: 'plain', running: t\('dragout\.stopping'\), done: t\('dragout\.stopped'\) \}/,
    );
  });
});

describe('the drag-out’s progress', () => {
  it('repaints only its own line or an empty slot', () => {
    expect(dragHandler).toMatch(/if \(!slotTakenBy\(toast\.value, 'drag-out', 'hold'\)\) return;/);
    expect(dragHandler).toMatch(/source: 'drag-out'/);
  });

  it('keeps its last word for a free slot instead of dropping it or clobbering', () => {
    expect(dragHandler).toMatch(/if \(slotTakenBy\(toast\.value, 'drag-out', 'end'\)\)/);
    expect(dragHandler).toMatch(/dragEndWaiting = \{ message: say\.message, ms \}/);
    expect(fn('slotFreed')).toMatch(/if \(waiting\) showToast\(/);
  });

  it('holds its line with the named sticky time, and a failure with the error time', () => {
    expect(dragHandler).toMatch(/STICKY_TOAST_MS/);
    expect(dragHandler).toMatch(/p\.error && p\.error !== 'cancelled' \? ERROR_TOAST_MS : FLASH_TOAST_MS/);
    expect(explorer, 'a literal ten minutes beside STICKY_TOAST_MS').not.toMatch(/10 \* 60_000/);
  });
});

describe('the toast timer', () => {
  it('goes with the explorer', () => {
    expect(explorer).toMatch(/onBeforeUnmount\(\(\) => \{\s*if \(toastTimer\) clearTimeout\(toastTimer\);/);
  });
});

describe('the drag-out import', () => {
  it('is one name per line, like its neighbours', () => {
    expect(explorer).not.toMatch(/type DragItem, sayDragProgress \}/);
    expect(explorer).toMatch(/\n {2}sayDragProgress,\n {2}type DragItem,\n\} from '\.\/lib\/dragOut';/);
  });
});
