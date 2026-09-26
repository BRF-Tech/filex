// The explorer asks for a queued rename or restore — wiring in FileExplorer.vue,
// which is far too large to mount here, so it is read as source (the way
// explorerFeedbackWiring.test.ts reads it). What a person sees is driven end to
// end in e2e/tests/159-queued-rename-restore.spec.ts.
//
// A folder rename and a restore from the trash ran inside the request. On an
// object store that is one request per object: the dialog waited with nothing
// on screen until the proxy gave up, then said it had failed while the server
// carried on.
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

describe('queued rename and restore wiring', () => {
  it('asks only a server that says it queues them', () => {
    expect(explorer).toMatch(/capabilitiesData\.value\?\.queued\?\.includes\(kind\)/);
  });

  it('queues the rename of a folder, and follows the job it gets back', () => {
    const rename = fn('submitRename');
    expect(rename).toMatch(/target\.type === 'dir' && serverQueues\('rename'\)/);
    expect(rename).toMatch(/api\.renameQueued\(/);
    expect(rename).toMatch(/pendingOps\.register\(/);
    // The undo waits for the job, like a queued move's.
    expect(rename).toMatch(/opUndo\.set\(/);
  });

  it('queues a restore from the trash as one request', () => {
    const restore = fn('restoreSelection');
    expect(restore).toMatch(/serverQueues\('restore'\)/);
    expect(restore).toMatch(/api\.restoreQueued\(ids\)/);
    expect(restore).toMatch(/pendingOps\.register\(/);
  });

  it('says how a queued rename or restore ended', () => {
    const settled = explorer.match(/onSettled: \(op: PendingOp\) => \{[\s\S]*?\n  \},\n\}\);/)?.[0] ?? '';
    expect(settled, 'the onSettled handler was not found').not.toBe('');
    expect(settled).toMatch(/op\.op_type === 'rename'/);
    expect(settled).toMatch(/op\.op_type === 'restore'/);
    expect(settled).toMatch(/toast\.restore_partial/);
  });
});
