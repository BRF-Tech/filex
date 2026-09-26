// The New document dialog's name, without a DOM (#56).
//
// Until #56 the dialog tied the extension to the type: Plain text drew `.txt`
// as a read-only suffix, so `LICENSE`, `Makefile`, `test.conf` or
// `example.custom` could not be created at all. Now the field holds the WHOLE
// name. The type fills in its extension (`Untitled.txt`), and the person may
// change it or remove it — except where the editor needs it: an office
// document or a diagram is found by its extension, so those types keep it.
//
// The dialog is mounted in tests/components/newDocumentModal.test.ts; this is
// the arithmetic under it, where a wrong answer is easiest to see.

import { describe, expect, it } from 'vitest';
import {
  docNameProblem,
  extLocked,
  finalDocName,
  retypeDocName,
  stemEnd,
  suggestDocName,
} from '@brftech/filex-core/src/lib/newDocName';
import type { NewDocType } from '@brftech/filex-core/src/types/FileNode';

const txt: NewDocType = { ext: 'txt', group: 'text', mime: 'text/plain; charset=utf-8', ext_required: false };
const md: NewDocType = { ext: 'md', group: 'text', mime: 'text/markdown; charset=utf-8', ext_required: false };
const docx: NewDocType = {
  ext: 'docx',
  group: 'document',
  mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  requires: 'onlyoffice',
  ext_required: true,
};
const drawio: NewDocType = { ext: 'drawio', group: 'diagram', mime: 'application/vnd.jgraph.mxfile', requires: 'drawio', ext_required: true };
const ALL = [docx, md, txt, drawio];

describe('the prefilled name', () => {
  it('is the full name, with the type’s extension in it', () => {
    expect(suggestDocName('Untitled', 'txt', new Set())).toBe('Untitled.txt');
    expect(suggestDocName('Untitled', 'docx', new Set())).toBe('Untitled.docx');
  });

  it('steps past a name already in the folder, case-insensitively', () => {
    const taken = new Set(['untitled.txt', 'untitled (2).txt']);
    expect(suggestDocName('Untitled', 'txt', taken)).toBe('Untitled (3).txt');
    // Another type's Untitled is no collision.
    expect(suggestDocName('Untitled', 'md', taken)).toBe('Untitled.md');
  });
});

describe('switching the type keeps what the person typed', () => {
  it('swaps the extension only while it is still the previous type’s default', () => {
    expect(retypeDocName('notes.txt', txt, md)).toBe('notes.md');
    expect(retypeDocName('notes.TXT', txt, md)).toBe('notes.md');
    expect(retypeDocName('report.docx', docx, txt)).toBe('report.txt');
  });

  it('leaves a name with no extension, or with one of the person’s own, alone', () => {
    expect(retypeDocName('LICENSE', txt, md)).toBe('LICENSE');
    expect(retypeDocName('test.conf', txt, md)).toBe('test.conf');
    expect(retypeDocName('example.custom', md, txt)).toBe('example.custom');
  });

  it('adds the extension a type whose editor needs it, since the server would', () => {
    expect(retypeDocName('LICENSE', txt, docx)).toBe('LICENSE.docx');
    expect(retypeDocName('test.conf', txt, docx)).toBe('test.conf.docx');
    expect(retypeDocName('flow.docx', docx, drawio)).toBe('flow.drawio');
  });

  it('a name that is only the old extension is not a stem to keep', () => {
    expect(retypeDocName('.txt', txt, md)).toBe('.txt');
    expect(retypeDocName('', txt, md)).toBe('');
  });
});

describe('what gets created', () => {
  it('a text type is created under exactly the name typed', () => {
    expect(finalDocName('LICENSE', txt)).toBe('LICENSE');
    expect(finalDocName('  test.conf ', txt)).toBe('test.conf');
    expect(finalDocName('example.custom', txt)).toBe('example.custom');
    expect(finalDocName('notes.md', txt)).toBe('notes.md');
  });

  it('a type whose editor needs its extension gains it when it is missing', () => {
    expect(finalDocName('report', docx)).toBe('report.docx');
    expect(finalDocName('report.DOCX', docx)).toBe('report.DOCX');
    expect(finalDocName('flow', drawio)).toBe('flow.drawio');
  });

  it('a server from before #56 (no ext_required) appends to every type, and the dialog says so', () => {
    const oldTxt: NewDocType = { ext: 'txt', group: 'text', mime: 'text/plain' };
    expect(extLocked(oldTxt)).toBe(true);
    expect(finalDocName('LICENSE', oldTxt)).toBe('LICENSE.txt');
    expect(extLocked(txt)).toBe(false);
    expect(extLocked(docx)).toBe(true);
  });
});

describe('where the cursor lands', () => {
  it('selects the stem, like a rename, so typing keeps the extension', () => {
    expect(stemEnd('Untitled.txt')).toBe('Untitled'.length);
    expect(stemEnd('archive.tar.gz')).toBe('archive.tar'.length);
  });

  it('selects everything when there is no extension to keep', () => {
    expect(stemEnd('LICENSE')).toBe('LICENSE'.length);
    // A dotfile's dot starts the NAME, not an extension.
    expect(stemEnd('.gitignore')).toBe('.gitignore'.length);
    expect(stemEnd('')).toBe(0);
  });
});

describe('names the dialog refuses before the server does', () => {
  it('accepts any leaf name for a text type', () => {
    for (const n of ['LICENSE', 'NOTICE', 'Makefile', 'Dockerfile', 'test.conf', 'example.custom', '.gitignore', 'notes.md']) {
      expect(docNameProblem(n, txt, ALL), n).toBeNull();
    }
  });

  it('refuses a slash, a name of dots, and the bare extension', () => {
    expect(docNameProblem('a/b', txt, ALL)).toEqual({ kind: 'slash' });
    expect(docNameProblem('a\\b', txt, ALL)).toEqual({ kind: 'slash' });
    expect(docNameProblem('..', txt, ALL)).toEqual({ kind: 'dots' });
    expect(docNameProblem('.txt', txt, ALL)).toEqual({ kind: 'bare_ext' });
    expect(docNameProblem('.docx', docx, ALL)).toEqual({ kind: 'bare_ext' });
  });

  it('refuses a name filex keeps for itself', () => {
    expect(docNameProblem('.keepdir', txt, ALL)).toEqual({ kind: 'reserved', name: '.keepdir' });
  });

  it('refuses an empty .docx made as Plain text — the server would too', () => {
    expect(docNameProblem('x.docx', txt, ALL)).toEqual({ kind: 'ext_needs_type', ext: 'docx' });
    expect(docNameProblem('Flow.DRAWIO', md, ALL)).toEqual({ kind: 'ext_needs_type', ext: 'drawio' });
    // Under its own type it is simply the right name.
    expect(docNameProblem('x.docx', docx, ALL)).toBeNull();
  });

  it('an empty field is no message — Create is simply off', () => {
    expect(docNameProblem('   ', txt, ALL)).toBeNull();
  });
});
