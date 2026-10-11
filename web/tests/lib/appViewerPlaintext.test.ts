// filex 0.56, task #189 - THE BROWSER'S KIND RULE (lib/appViewer
// plaintextOpenOf): which app opens a file the server holds only as
// ciphertext is decided in the browser, from the file's PLAINTEXT name - in a
// folder whose names are encrypted (level 2) the server cannot read the kind
// at all, and neither the name nor its extension is sent to it.
//
// Held here:
//  - the kind is the decrypted name's extension; the app interfaces granted
//    files:e2e-plaintext whose `applies` take it are offered, filex's own
//    viewer last; never an app without the grant, never ONLYOFFICE;
//  - the administrator's rule for the kind and the person's choice apply as
//    to any file;
//  - a level-2 row (its name decrypted in this tab, `e2e_stored` apart) opens
//    in the app; one whose name this tab could not decrypt (locked,
//    unreadable) opens in filex's own viewer - the kind would be a guess;
//  - the explorer offers the app there (it no longer refuses a folder with
//    encrypted names) and never offers "Always use this app for .<ext>"
//    for a name the server cannot read (the kind would reach the account).
//
// Red before 0.56's level-2 work: there was no plaintextOpenOf, and the
// explorer refused every app in a folder whose names are encrypted.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  BUILTIN_VIEWER,
  E2E_PLAINTEXT_GRANT,
  nameHiddenFromServer,
  openHandlersFor,
  pickOpenHandler,
  plaintextOpenOf,
} from '@brftech/filex-core/src/lib/appViewer';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';

const view = (plugin: string, grants: string[], applies: Record<string, unknown>): PluginViewRow =>
  ({
    plugin,
    id: 'editor',
    placement: 'viewer',
    label: { en: plugin },
    applies: { kind: 'file', ...applies },
    ui: { url: `/_appui/${plugin}/0123456789abcdef/index.html`, grants, engine: false, version: '1' },
  }) as unknown as PluginViewRow;

const PLAIN = ['files:read', 'files:write', 'ui', E2E_PLAINTEXT_GRANT];
const VIEWS = [
  view('office-editor', PLAIN, { ext: ['docx', 'xlsx'] }),
  view('peek', ['files:read', 'ui'], { ext: ['docx'] }),
  view('gallery', PLAIN, { mime: ['image/*'] }),
];
const ids = (hs: { id: string }[]) => hs.map((h) => h.id);
const OFFICE = 'app:office-editor/editor';

describe('plaintextOpenOf - by the plaintext name alone', () => {
  it('the kind is the name’s extension; the granted app, then filex’s own viewer', () => {
    const got = plaintextOpenOf(VIEWS, 'Maaş bordrosu.DOCX');
    expect(got.kind).toBe('docx');
    expect(ids(got.on)).toEqual([OFFICE, BUILTIN_VIEWER]);
    expect(got.handler?.id).toBe(OFFICE);
    expect(got.custom).toBe(false);
  });

  it('never an app without the grant; nothing that takes the kind: filex’s own viewer', () => {
    expect(ids(plaintextOpenOf(VIEWS, 'notlar.txt').on)).toEqual([BUILTIN_VIEWER]);
    expect(ids(plaintextOpenOf(VIEWS, 'README').on)).toEqual([BUILTIN_VIEWER]);
    expect(plaintextOpenOf(VIEWS, 'README').kind).toBe('');
  });

  it('a view that names a MIME type takes the kinds it implies', () => {
    expect(ids(plaintextOpenOf(VIEWS, 'tatil.png').on)).toEqual(['app:gallery/editor', BUILTIN_VIEWER]);
  });

  it('the administrator’s rule for the kind, and the person’s choice', () => {
    const order = { docx: { order: [BUILTIN_VIEWER], off: [] } };
    expect(ids(plaintextOpenOf(VIEWS, 'a.docx', { rules: order }).on)).toEqual([BUILTIN_VIEWER, OFFICE]);
    const off = { docx: { order: [], off: [OFFICE] } };
    const got = plaintextOpenOf(VIEWS, 'a.docx', { rules: off });
    expect(ids(got.on)).toEqual([BUILTIN_VIEWER]);
    expect(ids(got.off)).toEqual([OFFICE]);
    expect(got.custom).toBe(true);

    expect(plaintextOpenOf(VIEWS, 'a.docx', { choice: BUILTIN_VIEWER }).handler?.id).toBe(BUILTIN_VIEWER);
    const asked: string[] = [];
    const personal = (kind: string) => {
      asked.push(kind);
      return BUILTIN_VIEWER;
    };
    expect(plaintextOpenOf(VIEWS, 'a.docx', { personal }).handler?.id).toBe(BUILTIN_VIEWER);
    expect(asked).toEqual(['docx']);
    expect(plaintextOpenOf(VIEWS, 'a.docx', { personal: 'app:peek/editor' }).handler?.id, 'a choice that is not on is not followed').toBe(OFFICE);
  });
});

describe('a row of a folder whose names are encrypted (level 2)', () => {
  const STORED = 'ugkmCcOceKUyA7tsdZxzltk';
  const level2 = {
    type: 'file',
    path: `docs://Kasa/${STORED}`,
    basename: 'Maaş bordrosu.docx',
    extension: 'docx',
    mime_type: 'application/octet-stream',
    encrypted: 'folder',
    e2e_root: 'docs://Kasa',
    e2e_stored: STORED,
    e2e_name_state: 'enc',
  };
  const opts = { inEncrypted: true, e2ePlaintext: true };

  it('opens in the granted app, chosen by its decrypted name', () => {
    expect(ids(openHandlersFor(VIEWS, level2, null, opts).on)).toEqual([OFFICE, BUILTIN_VIEWER]);
    expect(pickOpenHandler(VIEWS, level2, null, null, null, opts)?.id).toBe(OFFICE);
    const rules = { docx: { order: [], off: [OFFICE] } };
    expect(ids(openHandlersFor(VIEWS, level2, rules, opts).on), 'the rule of the DECRYPTED kind').toEqual([BUILTIN_VIEWER]);
  });

  it('a name this tab could not decrypt opens in filex’s own viewer', () => {
    for (const state of ['locked', 'unreadable']) {
      const n = { ...level2, basename: 'x.docx', extension: 'docx', e2e_name_state: state };
      expect(ids(openHandlersFor(VIEWS, n, null, opts).on), state).toEqual([BUILTIN_VIEWER]);
    }
  });

  it('says the server cannot read its name; a level-1 row says nothing', () => {
    expect(nameHiddenFromServer(level2)).toBe(true);
    const { e2e_stored: _s, e2e_name_state: _n, ...level1 } = level2;
    expect(nameHiddenFromServer({ ...level1, basename: 'rapor.docx' })).toBe(false);
    expect(nameHiddenFromServer(null)).toBe(false);
  });
});

describe('the explorer', () => {
  // FileExplorer is not mounted in unit tests; the wiring is read off its
  // source, like the other explorer wiring tests.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

  it('offers an app in a folder whose names are encrypted', () => {
    const fn = src.slice(src.indexOf('function e2ePlainRootOf'), src.indexOf('function openEncryptedInApp'));
    expect(fn).toMatch(/e2eRing\.has\(root\)/);
    expect(fn, 'no refusal of a folder with encrypted names').not.toMatch(/writesEncrypted/);
  });

  it('never offers "always use this app" for a name the server cannot read', () => {
    const kind = src.slice(src.indexOf('const openWithKind'), src.indexOf('const openWithCurrent'));
    expect(kind).toMatch(/nameHiddenFromServer\(openWithTarget\.value\)/);
  });
});
