// filex 0.57 (task #268): with no ONLYOFFICE Document Server configured,
// filex's own viewer can only say one is missing for an office document. Where
// an app's interface opens the kind (the Office editor app), the rule that
// picks a file's handler (lib/appViewer pickOpenHandler) never chooses
// filex's own viewer for it - not the tab's choice, not a person's or an
// administrator's earlier pick, not as the first - on every surface that asks
// it (the explorer, the editor tab, the desktop app's windows). With a
// Document Server everything stays as it was.
//
// ⚠ Red on the code before: a person who once picked "filex's viewer" for
// .docx, or a Default apps rule that put it first, got "no Document Server"
// in the browser while the desktop app opened the editor.
import { describe, expect, it } from 'vitest';

import { BUILTIN_VIEWER, openHandlersFor, pickOpenHandler } from '@brftech/filex-core/src/lib/appViewer';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';

const office = {
  plugin: 'office-editor',
  id: 'editor',
  placement: 'viewer',
  label: { en: 'Office editor' },
  applies: { kind: 'file', ext: ['docx', 'doc', 'xlsx', 'csv'] },
  ui: { url: '/_appui/office-editor/0123456789abcdef/index.html', grants: [], engine: false, version: '0.4.0' },
} as unknown as PluginViewRow;
const APP = 'app:office-editor/editor';
const file = (name: string) => ({ type: 'file', basename: name, extension: name.split('.').pop() ?? '' });
const ids = (hs: { id: string }[]) => hs.map((h) => h.id);

describe('an office document with no Document Server', () => {
  it('opens in the app whatever was picked before: the choice, the person, the administrator', () => {
    const first = { docx: { order: [BUILTIN_VIEWER], off: [] } };
    expect(pickOpenHandler([office], file('rapor.docx'), BUILTIN_VIEWER, null, null, { onlyOffice: false })?.id).toBe(APP);
    expect(pickOpenHandler([office], file('rapor.docx'), null, null, BUILTIN_VIEWER, { onlyOffice: false })?.id).toBe(APP);
    expect(pickOpenHandler([office], file('rapor.docx'), null, first, null, { onlyOffice: false })?.id).toBe(APP);
    expect(pickOpenHandler([office], file('eski.doc'), BUILTIN_VIEWER, first, BUILTIN_VIEWER, {})?.id).toBe(APP);
  });

  it('still lists filex’s own viewer in "Open with"', () => {
    expect(ids(openHandlersFor([office], file('rapor.docx'), null, { onlyOffice: false }).on)).toEqual([APP, BUILTIN_VIEWER]);
  });

  it('keeps filex’s own viewer where no app opens the kind', () => {
    expect(pickOpenHandler([], file('rapor.docx'), BUILTIN_VIEWER, null, null, { onlyOffice: false })?.id).toBe(BUILTIN_VIEWER);
  });

  it('keeps the administrator’s switch: an app switched off for the kind is not taken', () => {
    const off = { docx: { order: [], off: [APP] } };
    expect(pickOpenHandler([office], file('rapor.docx'), null, off, null, { onlyOffice: false })?.id).toBe(BUILTIN_VIEWER);
  });
});

describe('everything else stays as it was', () => {
  it('with a Document Server, an earlier pick of filex’s own viewer holds', () => {
    expect(pickOpenHandler([office], file('rapor.docx'), BUILTIN_VIEWER, null, null, { onlyOffice: true })?.id).toBe(BUILTIN_VIEWER);
    expect(pickOpenHandler([office], file('rapor.docx'), null, null, BUILTIN_VIEWER, { onlyOffice: true })?.id).toBe(BUILTIN_VIEWER);
  });

  it('a .csv keeps filex’s own table, which needs no Document Server', () => {
    expect(pickOpenHandler([office], file('liste.csv'), BUILTIN_VIEWER, null, null, { onlyOffice: false })?.id).toBe(BUILTIN_VIEWER);
  });
});
