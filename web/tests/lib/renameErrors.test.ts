// A failed rename reaches the person, not just the host's `error` listener.
//
// FileExplorer has no mountable harness here (the core package has no test
// runner of its own — see splitOffered.test.ts), so this reads the function the
// rename dialog submits to and pins what its failure path does. The dialog half
// is mounted for real in tests/components/renameModal.test.ts.
//
// ⚠ Before, the catch block only emitted `error`, and the stock web app's
// handler for it is a console.warn: a refused rename left the dialog open and
// silent. The server now answers a rename onto a taken name with 409
// NAME_TAKEN (instead of replacing that file), so the silence hid the one
// answer a person can act on.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const EXPLORER = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

function body(signature: string): string {
  const start = EXPLORER.indexOf(signature);
  expect(start, `${signature} is gone — where does a rename submit now?`).toBeGreaterThanOrEqual(0);
  const end = EXPLORER.indexOf('\n}\n', start);
  return EXPLORER.slice(start, end);
}

describe('submitRename shows its failure in the dialog', () => {
  const submit = body('async function submitRename(name: string) {');
  const failure = submit.slice(submit.indexOf('} catch'));

  it('a taken name (409) is said in words the person can act on', () => {
    expect(failure).toMatch(/status === 409/);
    expect(failure).toContain("t('newdoc.err.exists', { name })");
  });

  it('clears the last failure before it tries again', () => {
    // Retrying the SAME taken name produces the identical sentence: without
    // this line the ref never changes, the dialog (which cleared its line when
    // the name was edited) shows nothing, and Save reads as broken.
    const beforeTry = submit.slice(0, submit.indexOf('try {'));
    expect(beforeTry).toMatch(/renameError\.value = null/);
  });

  it('any other failure is shown too, not only emitted', () => {
    expect(failure).toMatch(/renameError\.value = /);
    // Still reported to the host too — through reportMutationError, the one
    // path every mutation's failure takes (it also says a lock refusal).
    expect(failure).toContain("reportMutationError(err, { op: 'rename' })");
  });

  it('the dialog is given the message', () => {
    expect(EXPLORER).toMatch(/<RenameModal[\s\S]*?:error="renameError"[\s\S]*?\/>/);
  });

  it('a reopened dialog does not carry the last attempt’s error', () => {
    expect(EXPLORER).toMatch(/watch\(showRename, \(open\) => \{\s*if \(open\) renameError\.value = null;/);
  });
});
