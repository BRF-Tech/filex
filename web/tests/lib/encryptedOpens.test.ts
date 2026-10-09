// #189 - an end-to-end encrypted file is offered no app, from any view.
//
// In an encrypted folder's listing the explorer offered only filex's own
// viewer (no "Open with -> app", no app action, no ONLYOFFICE), while the same
// file in Recent, Starred, a tag view, a search or Shared with me was offered
// every app - and the server then refused it (`encrypted`). The rows of those
// views say it themselves (`encrypted`, `e2e_root`, `e2e`), and ONE rule
// (lib/encryptedRow, read by lib/appViewer and lib/pluginMenu) now gives the
// folder's answer wherever the row comes from. Red before: openHandlersFor
// and pluginMenuRows read none of the three on a row, and onlyBuiltinOpens /
// rowIsEncrypted did not exist.
import { describe, expect, it } from 'vitest';

import {
  onlyBuiltinOpens,
  openHandlersFor,
  pickOpenHandler,
} from '@brftech/filex-core/src/lib/appViewer';
import { isEncryptedKind, rowInEncryptedFolder, rowIsEncrypted } from '@brftech/filex-core/src/lib/encryptedRow';
import { publicLinksOff } from '@brftech/filex-core/src/lib/e2eLinks';
import { pluginMenuRows } from '@brftech/filex-core/src/lib/pluginMenu';
import type { PluginActionRow, PluginViewRow } from '@brftech/filex-core/src/types/Plugins';

const view = (plugin: string, id: string, ext: string[]): PluginViewRow =>
  ({
    plugin,
    id,
    placement: 'viewer',
    label: { en: plugin, tr: plugin },
    applies: { kind: 'file', ext },
    ui: { url: `/_appui/${plugin}/0123456789abcdef/index.html`, grants: [], engine: false, version: '1' },
  }) as unknown as PluginViewRow;

const VIEWS = [view('office-editor', 'editor', ['docx', 'csv']), view('zeta', 'viewer', ['docx'])];
const docx = { type: 'file', basename: 'rapor.docx', extension: 'docx' };
const csv = { type: 'file', basename: 'liste.csv', extension: 'csv' };
const ids = (hs: { id: string }[]) => hs.map((h) => h.id);

const sign: PluginActionRow = {
  plugin: 'sign',
  id: 'sign',
  key: 'plugin:sign/sign',
  label: { en: 'Sign…', tr: 'İmzala…' },
  icon: 'sign',
  applies: { kind: 'file', ext: ['pdf'] },
};
const pdf = { type: 'file', extension: 'pdf', mime_type: 'application/pdf', basename: 'nda.pdf' };

describe('rowIsEncrypted - the server’s three words for it', () => {
  it('is a file’s `encrypted`, an encrypted folder’s `e2e`, anything’s `e2e_root`', () => {
    expect(rowIsEncrypted({ encrypted: 'folder' })).toBe(true);
    expect(rowIsEncrypted({ encrypted: 'vault' })).toBe(true);
    expect(rowIsEncrypted({ encrypted: 'file' })).toBe(true);
    expect(rowIsEncrypted({ e2e: true })).toBe(true);
    expect(rowIsEncrypted({ e2e_root: 'docs://Sifreli' })).toBe(true);
    expect(rowIsEncrypted({})).toBe(false);
    expect(rowIsEncrypted({ e2e_root: '', encrypted: 'yes' })).toBe(false);
    expect(rowIsEncrypted(null)).toBe(false);
  });

  it('a single encrypted file (`file`, a .fxe) is ciphertext but not inside an encrypted folder', () => {
    // The links rule (lib/e2eLinks) reads rowInEncryptedFolder: a .fxe may
    // still be shared, its recipient opens it with its password.
    expect(rowInEncryptedFolder({ encrypted: 'file' })).toBe(false);
    expect(rowInEncryptedFolder({ encrypted: 'folder' })).toBe(true);
    expect(isEncryptedKind('file')).toBe(true);
    expect(isEncryptedKind('yes')).toBe(false);
  });
});

describe('openHandlersFor - an encrypted file opens in filex’s own viewer only', () => {
  it('a plain file is offered its apps, as before', () => {
    expect(ids(openHandlersFor(VIEWS, docx).on)).toEqual(['app:office-editor/editor', 'app:zeta/viewer', 'builtin']);
  });

  it('a row that says it is encrypted (Recent, Starred, a tag, a search, Shared with me) gets no app', () => {
    for (const row of [
      { ...docx, encrypted: 'folder' },
      { ...docx, encrypted: 'vault' },
      { ...docx, basename: 'rapor.docx.fxe', extension: 'fxe', encrypted: 'file' },
      { ...docx, e2e_root: 'docs://Sifreli' },
    ]) {
      const r = openHandlersFor(VIEWS, row, null, { onlyOffice: true });
      expect(ids(r.on), JSON.stringify(row)).toEqual(['builtin']);
      expect(r.off).toEqual([]);
      expect(onlyBuiltinOpens(row)).toBe(true);
    }
  });

  it('the same answer as the folder’s listing gives (inEncrypted), so the views agree', () => {
    const inFolder = openHandlersFor(VIEWS, docx, null, { inEncrypted: true });
    const fromRecent = openHandlersFor(VIEWS, { ...docx, encrypted: 'folder' }, null, {});
    expect(fromRecent).toEqual(inFolder);
  });

  it('neither an administrator’s rule, the person’s choice nor ONLYOFFICE opens one', () => {
    const rules = { docx: { order: ['app:zeta/viewer'], off: ['builtin'] } };
    const enc = { ...docx, encrypted: 'folder' };
    expect(ids(openHandlersFor(VIEWS, enc, rules).on)).toEqual(['builtin']);
    expect(pickOpenHandler(VIEWS, enc, 'app:zeta/viewer', rules, 'app:office-editor/editor')?.id).toBe('builtin');
    expect(pickOpenHandler(VIEWS, { ...csv, encrypted: 'folder' }, 'onlyoffice', null, null, { onlyOffice: true })?.id).toBe(
      'builtin',
    );
  });

  it('a folder row is still opened by nobody, encrypted or not', () => {
    expect(openHandlersFor(VIEWS, { type: 'dir', basename: 'Sifreli', e2e: true }).on).toEqual([]);
    expect(onlyBuiltinOpens({ type: 'dir', basename: 'Sifreli', e2e: true })).toBe(false);
  });
});

describe('pluginMenuRows - no app action on an encrypted row, from any view', () => {
  it('offers the action on a plain file', () => {
    expect(pluginMenuRows([sign], [pdf], { locale: 'en' }).length).toBeGreaterThan(0);
  });

  it('offers none on a file whose row says it is encrypted, or on a folder inside one', () => {
    expect(pluginMenuRows([sign], [{ ...pdf, encrypted: 'folder' }], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([sign], [{ ...pdf, encrypted: 'vault' }], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([sign], [{ ...pdf, encrypted: 'file' }], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([sign], [{ ...pdf, e2e_root: 'docs://Sifreli' }], { locale: 'en' })).toEqual([]);
    expect(pluginMenuRows([sign], [pdf, { ...pdf, basename: 'b.pdf', encrypted: 'folder' }], { locale: 'en' })).toEqual([]);
  });
});

describe('publicLinksOff - the same rule for links', () => {
  it('a file row saying `encrypted` gets no public link', () => {
    expect(publicLinksOff([{ encrypted: 'vault' }], false)).toBe(true);
    expect(publicLinksOff([{ encrypted: 'folder' }], false)).toBe(true);
    expect(publicLinksOff([{}], false)).toBe(false);
    expect(publicLinksOff([{ encrypted: 'file' }], false), 'a .fxe may still be shared').toBe(false);
  });
});
