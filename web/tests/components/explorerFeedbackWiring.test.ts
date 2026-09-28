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
    expect(report).toMatch(/showToast\(\{ message: opts\.words \?\? failureText\(err\) \}, ERROR_TOAST_MS\)/);
    expect(report).toMatch(/inDialog/);
    // A dialog that shows the failure itself does not get a second copy of it
    // — but only while it IS on screen: a refusal that comes after its dialog
    // closed, or was opened again on another item, is a toast (lesson #498).
    expect(fn('refuseInDialog')).toMatch(/reportMutationError\(err, context, \{ inDialog: req\.refuse\(ticket, words\), words \}\)/);
    expect(fn('submitRename')).toMatch(/refuseInDialog\(renameReq, ticket, err, \{ op: 'rename' \}, words\)/);
    expect(fn('submitNewFolder')).toMatch(/refuseInDialog\(newFolderReq, ticket, err, \{ op: 'newfolder' \}\)/);
    expect(fn('confirmDelete')).toMatch(/refuseInDialog\(deleteReq, ticket, err, \{ op: 'delete' \}\)/);
    expect(fn('purgeSelection')).toMatch(/refuseInDialog\(deleteReq, ticket, firstError, \{ op: 'purge' \}/);
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
    expect(picked).toMatch(/const outcome = await transferItems\(/);
    expect(picked).toMatch(/if \(outcome === 'queued'\) \{\s*flashToast\(move \? t\('toast\.moved_to'/);
    expect(fn('transferItems')).toMatch(/Promise<TransferOutcome>/);
    expect(fn('moveSourcesAsync')).toMatch(/Promise<'queued' \| 'done' \| 'refused'>/);
  });

  it('keeps the Undo of a move done inside the request, and says when nothing was sent', () => {
    // Without a `moveAsync` endpoint (a custom host) the move is done inside
    // the request and offers Undo; "Move to X queued" over it was untrue and
    // lost the Undo. "Move to…" onto the items' own folder said nothing.
    const picked = fn('onDestinationPicked');
    expect(fn('moveSourcesAsync')).toMatch(/return api\.endpoints\.moveAsync \? 'queued' : 'done';/);
    expect(picked).not.toMatch(/outcome === 'done'\) \{\s*flashToast/);
    expect(picked).toMatch(/else if \(outcome === 'nothing'\) \{\s*flashToast\(t\('toast\.already_there', \{ name \}\)\);/);
    expect(fn('transferItems')).toMatch(/return 'nothing';/);
  });

  it('keeps each dialog busy while its request runs, and takes no second one', () => {
    // composables/useDialogRequest: `begin` is null while one is on its way.
    for (const [f, req] of [
      ['submitRename', 'renameReq'],
      ['submitNewFolder', 'newFolderReq'],
      ['confirmDelete', 'deleteReq'],
      ['purgeSelection', 'deleteReq'],
    ] as const) {
      expect(fn(f)).toMatch(new RegExp(`const ticket = ${req}\\.begin\\(\\);\\s*if \\(ticket === null\\) return;`));
    }
    expect(explorer).toMatch(/<RenameModal[\s\S]*?:busy="renameBusy"/);
    expect(explorer).toMatch(/<NewFolderModal[\s\S]*?:busy="newFolderBusy"[\s\S]*?:error="newFolderError"/);
    expect(explorer).toMatch(/<DeleteConfirmModal[\s\S]*?:busy="deleteBusy"[\s\S]*?:error="deleteError"/);
  });

  it('says a restore is running, and what did not come back', () => {
    const restore = fn('restoreSelection');
    expect(restore).toMatch(/if \(restoreBusy\.value\) return;/);
    expect(restore).toMatch(/t\('toast\.restoring'/);
    // What did not come back, and why (lib/restoreWords).
    expect(restore).toMatch(/sayRestore\(\{ restored, taken, failed,/);
  });

  it('frees a dialog as soon as it closes, not after the listing read behind it', () => {
    // A Rename opened on another file while the first one's listing was read
    // again came up already "Renaming…" with its button shut.
    const rename = fn('submitRename');
    expect(rename.indexOf('renameReq.end(ticket);')).toBeGreaterThan(rename.indexOf('showRename.value = false;'));
    expect(rename.indexOf('renameReq.end(ticket);')).toBeLessThan(rename.indexOf('await load();'));
    const folder = fn('submitNewFolder');
    expect(folder.indexOf('newFolderReq.end(ticket);')).toBeLessThan(folder.indexOf('await load();'));
    const del = fn('confirmDelete');
    expect(del.indexOf('deleteReq.end(ticket);')).toBeLessThan(del.indexOf('else await load();'));
  });

  it('keeps "preparing the archive" up until the download starts, once', () => {
    const download = fn('downloadSelection');
    expect(download).toMatch(/if \(archivePreparing\.value\) return;/);
    expect(download).toMatch(/showToast\(\{ message: t\('toast\.archive\.preparing'\) \}, STICKY_TOAST_MS\)/);
  });
});
