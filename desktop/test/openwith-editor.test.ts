// Which editor an "Open with filex" window opens (task #268): with no
// ONLYOFFICE Document Server answering, the office editor app's interface
// (`app=office-editor/<view>` on the editor page's address), for every kind
// the desktop opens; with one, what the page chooses, as before.
//
// Before 0.57 the window asked for nothing: the page took the person's
// "always open with" or the administrator's order, and a choice of filex's
// own viewer - made while a Document Server was there - opened a document
// nobody could edit ("the document server is missing"). The window's whole
// point is editing.
//
// ⚠ The module is imported as a namespace (as in openwith-beside.test.ts):
// run against a build without these functions, every test fails on its own
// with "is not a function" instead of the whole file failing to load.
//
// Run:  node --experimental-strip-types --test desktop/test/openwith-editor.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import * as ow from '../src/openwith.ts';

const api = ow as unknown as Record<string, any>;

/** The kinds the office editor app's interface opens (its manifest's `editor`
 *  view, filex-office-editor 0.4.0 `applies.ext`). */
const APP_KINDS = [
  'docx', 'docm', 'dotx', 'dotm', 'doc', 'dot', 'odt', 'ott', 'fodt', 'rtf', 'wps', 'wpt', 'stw', 'sxw', 'mht', 'mhtml',
  'xlsx', 'xlsm', 'xltx', 'xltm', 'xls', 'xlt', 'xlsb', 'ods', 'ots', 'fods', 'csv', 'et', 'ett', 'sxc',
  'pptx', 'pptm', 'potx', 'potm', 'ppsx', 'ppsm', 'ppt', 'pot', 'pps', 'odp', 'otp', 'fodp', 'dps', 'dpt', 'sxi',
];

const OFFICE_EDITOR_VIEW = {
  plugin: 'office-editor',
  id: 'editor',
  placement: 'viewer',
  label: { en: 'Office editor' },
  applies: { kind: 'file', ext: APP_KINDS },
  ui: { url: '/_appui/office-editor/0123456789abcdef/index.html', grants: ['files:read', 'files:write', 'ui'], version: '0.4.0' },
};

const DRAWIO_VIEW = {
  plugin: 'drawio',
  id: 'editor',
  placement: 'viewer',
  label: { en: 'draw.io' },
  applies: { ext: ['drawio', 'dio'] },
  ui: { url: '/_appui/drawio/fedcba9876543210/index.html', grants: ['files:read', 'ui'], version: '31.5.3' },
};

/** The capabilities of a server with a Document Server that answers. */
const WITH_DS = {
  onlyoffice_url: 'https://docs.example.com',
  external: { onlyoffice: { enabled: true, state: 'ok', url: 'https://docs.example.com' } },
};

/** And of servers without one: none set up, set up but down, or a bare answer. */
const WITHOUT_DS = [
  { name: 'none set up', caps: { external: { onlyoffice: { enabled: false, state: 'disabled' } } } },
  { name: 'set up, not answering', caps: { onlyoffice_url: 'https://docs.example.com', external: { onlyoffice: { enabled: true, state: 'down' } } } },
  { name: 'answering, no address', caps: { onlyoffice_url: '', external: { onlyoffice: { enabled: true, state: 'ok' } } } },
  { name: 'an answer that says nothing', caps: {} },
];

const actions = (...views: unknown[]) => ({ actions: [], views });

test('a Document Server answering: the window asks for nothing, as before', () => {
  assert.equal(api.documentServerUsable(WITH_DS), true);
  assert.equal(api.officeEditorChoice(WITH_DS, actions(OFFICE_EDITOR_VIEW), 'Rapor.docx'), null);
  assert.equal(api.officeEditorChoice(WITH_DS, actions(OFFICE_EDITOR_VIEW), 'Eski.doc'), null);
});

test('no Document Server, the office editor app installed: its editor view', () => {
  for (const { name, caps } of WITHOUT_DS) {
    assert.equal(api.documentServerUsable(caps), false, name);
    assert.equal(api.officeEditorChoice(caps, actions(DRAWIO_VIEW, OFFICE_EDITOR_VIEW), 'Bütçe Özeti.xlsx'), 'office-editor/editor', name);
  }
});

test('every kind the desktop opens goes to the app, the legacy ones included', () => {
  const caps = WITHOUT_DS[0]!.caps;
  for (const ext of ow.OFFICE_EXTENSIONS) {
    assert.equal(api.officeEditorChoice(caps, actions(OFFICE_EDITOR_VIEW), `Rapor.${ext}`), 'office-editor/editor', ext);
    assert.equal(api.officeEditorChoice(caps, actions(OFFICE_EDITOR_VIEW), `/home/alice/Belgeler/RAPOR.${ext.toUpperCase()}`), 'office-editor/editor', `${ext} by a full path, upper case`);
  }
});

test('no Document Server and no office editor app: nothing asked, the page opens what it always did', () => {
  const caps = WITHOUT_DS[0]!.caps;
  assert.equal(api.officeEditorChoice(caps, actions(), 'Rapor.docx'), null);
  assert.equal(api.officeEditorChoice(caps, actions(DRAWIO_VIEW), 'Rapor.docx'), null, 'another app is not the office editor');
  assert.equal(api.officeEditorChoice(caps, actions({ ...OFFICE_EDITOR_VIEW, applies: { ext: ['docx'] } }), 'Rapor.doc'), null, 'a kind the app does not open');
  assert.equal(api.officeEditorChoice(caps, actions({ ...OFFICE_EDITOR_VIEW, placement: 'modal' }), 'Rapor.docx'), null, 'only its viewer view');
  assert.equal(api.officeEditorChoice(caps, actions({ ...OFFICE_EDITOR_VIEW, ui: undefined }), 'Rapor.docx'), null, 'only an interface');
  assert.equal(api.officeEditorChoice(caps, actions({ ...OFFICE_EDITOR_VIEW, id: '../x' }), 'Rapor.docx'), null, 'a view id that is not one');
  assert.equal(api.officeEditorChoice(caps, actions({ ...OFFICE_EDITOR_VIEW, applies: { ext: 'docx' } }), 'Rapor.docx'), null, 'a rule that is not one: skipped, never thrown');
  // An answer that could not be read (apps off, an older server) is no answer.
  assert.equal(api.officeEditorChoice(caps, null, 'Rapor.docx'), null);
  assert.equal(api.officeEditorChoice(caps, { views: 'x' }, 'Rapor.docx'), null);
  assert.equal(api.officeEditorChoice(null, actions(OFFICE_EDITOR_VIEW), 'Rapor.docx'), 'office-editor/editor', 'no capabilities read as no Document Server, as the page reads them');
});

test('the editor page\'s address carries the choice, under the server\'s own path', () => {
  const plain = new URL(api.documentRouteUrl('https://files.example.com/filex', 'home://.filex-open/a1b2c3d4e5f6-Bütçe.xlsx'));
  assert.equal(plain.pathname, '/filex/files/edit');
  assert.equal(plain.searchParams.get('path'), 'home://.filex-open/a1b2c3d4e5f6-Bütçe.xlsx');
  assert.equal(plain.searchParams.get('type'), 'xlsx');
  assert.equal(plain.searchParams.get('mode'), 'edit');
  assert.equal(plain.searchParams.has('app'), false, 'no choice: no app=');
  const app = new URL(api.documentRouteUrl('https://files.example.com', 'home://.filex-open/a1b2c3d4e5f6-Rapor.doc', 'office-editor/editor'));
  assert.equal(app.searchParams.get('app'), 'office-editor/editor');
  assert.equal(app.searchParams.get('type'), 'doc');
});

test('closing with unsaved changes: the draft\'s words only for a draft', () => {
  const page = (p: string) => 'https://files.example.com/files/edit?' + new URLSearchParams({ path: p, mode: 'edit' }).toString();
  assert.equal(api.leaveQuestionFor(page('home://.filex-drafts/7/k1/Rapor.docx')), 'draft');
  // The office editor app in an "Open with filex" window: the edit is lost.
  assert.equal(api.leaveQuestionFor(page('home://.filex-open/a1b2c3d4e5f6-Rapor.docx')), 'unsaved');
  assert.equal(api.leaveQuestionFor(page('home://Reports/.filex-drafts-notes.docx')), 'unsaved');
  assert.equal(api.leaveQuestionFor('not an address'), 'unsaved');
});
