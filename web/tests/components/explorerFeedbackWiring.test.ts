// How the explorer answers an action that fails or takes long — wiring that
// lives in FileExplorer.vue, which is far too large to mount here, so it is
// read as source (the way sideNavStorageLine.test.ts reads it). What a person
// sees is also driven end to end in e2e/tests/158-explorer-feedback.spec.ts.
//
// Audit, 2026-09-26:
//   - a refused paste, drag-move, duplicate or copy went out as `emit('error')`
//     only, and both first-party hosts write that event to the console;
//   - "Move to…" said "Moved to X" whatever happened, even over the refusal;
//   - a batch upload read the open folder again for every file, so browsing
//     during the batch sent the rest of it somewhere else;
//   - Restore and the multi-item Download had no busy state at all.
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

describe('explorer feedback wiring', () => {
  it('says a refused change on screen, not only to the host', () => {
    const report = fn('reportMutationError');
    expect(report).toMatch(/showToast\(\{ message: failureText\(err\) \}, ERROR_TOAST_MS\)/);
    expect(report).toMatch(/inDialog/);
    // A dialog that shows the failure itself does not get a second copy of it.
    expect(fn('submitRename')).toMatch(/reportMutationError\(err, \{ op: 'rename' \}, \{ inDialog: true \}\)/);
    expect(fn('confirmDelete')).toMatch(/reportMutationError\(err, \{ op: 'delete' \}, \{ inDialog: true \}\)/);
    // The copy path no longer prints the raw message beside it.
    expect(fn('transferItems')).not.toMatch(/flashToast\(\(err as Error\)\.message\)/);
  });

  it('reads the upload destination once per batch', () => {
    const upload = fn('uploadFiles');
    expect(upload).toMatch(/const target = qualify\(currentPath\.value\)/);
    expect(upload).toMatch(/chunkedUpload\(f, target\)/);
    expect(upload).toMatch(/legacyUpload\(f, target\)/);
    expect(upload.match(/currentPath\.value/g) ?? [], 'the folder is read exactly once').toHaveLength(1);
  });

  it('only says "moved" or "copied" when the job was queued', () => {
    const picked = fn('onDestinationPicked');
    expect(picked).toMatch(/const queued = await transferItems\(/);
    expect(picked).toMatch(/if \(queued\)/);
    expect(fn('transferItems')).toMatch(/Promise<boolean>/);
    expect(fn('moveSourcesAsync')).toMatch(/Promise<boolean>/);
  });

  it('keeps each dialog busy while its request runs, and takes no second one', () => {
    expect(fn('submitRename')).toMatch(/if \(renameBusy\.value\) return;/);
    expect(fn('submitNewFolder')).toMatch(/if \(newFolderBusy\.value\) return;/);
    expect(fn('confirmDelete')).toMatch(/if \(deleteBusy\.value\) return;/);
    expect(explorer).toMatch(/<RenameModal[\s\S]*?:busy="renameBusy"/);
    expect(explorer).toMatch(/<NewFolderModal[\s\S]*?:busy="newFolderBusy"[\s\S]*?:error="newFolderError"/);
    expect(explorer).toMatch(/<DeleteConfirmModal[\s\S]*?:busy="deleteBusy"[\s\S]*?:error="deleteError"/);
  });

  it('says a restore is running, and what did not come back', () => {
    const restore = fn('restoreSelection');
    expect(restore).toMatch(/if \(restoreBusy\.value\) return;/);
    expect(restore).toMatch(/t\('toast\.restoring'/);
    expect(restore).toMatch(/failed > 0/);
  });

  it('keeps "preparing the archive" up until the download starts, once', () => {
    const download = fn('downloadSelection');
    expect(download).toMatch(/if \(archivePreparing\.value\) return;/);
    expect(download).toMatch(/showToast\(\{ message: t\('toast\.archive\.preparing'\) \}, STICKY_TOAST_MS\)/);
  });
});
