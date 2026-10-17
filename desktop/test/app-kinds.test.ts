// "Open with filex" takes the kinds of file the apps on the person's servers
// open (the owner, 2026-10-16: "someone installs a PSD editor, and a .psd
// opens with filex"): what a server opens, which kinds the OS is told, which
// server opens a double-clicked file, and what is said when none does.
//
// Run:  node --experimental-strip-types --test desktop/test/app-kinds.test.ts

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import * as ow from '../src/openwith.ts';
import {
  AppKindsStore,
  MAX_REGISTERED,
  NEVER_REGISTERED,
  candidatesFor,
  choiceKey,
  emptyAppKinds,
  kindRows,
  labelIn,
  mergeRecords,
  pickCandidate,
  registrableKinds,
  registrableMediaTypes,
  type ServerRecord,
} from '../src/app-kinds.ts';
import { appStrings } from './ui-strings.ts';

const ui = (plugin: string, version = '1.0.0') => ({ url: `/_appui/${plugin}/0123456789abcdef/index.html`, grants: ['files:read', 'files:write', 'ui'], version });

/** An app that opens Photoshop and Affinity files (the catalog's media-thumbs `design` view). */
const DESIGN = { plugin: 'media-thumbs', id: 'design', placement: 'viewer', label: { en: 'Design', tr: 'Tasarım' }, applies: { kind: 'file', ext: ['psd', 'psb', 'af'] }, ui: ui('media-thumbs') };
const DRAWIO = { plugin: 'drawio', id: 'editor', placement: 'viewer', label: { en: 'draw.io' }, applies: { ext: ['drawio', 'dio'] }, ui: ui('drawio') };
const OFFICE = { plugin: 'office-editor', id: 'editor', placement: 'viewer', label: { en: 'Office editor', tr: 'Ofis düzenleyici' }, applies: { kind: 'file', ext: ['docx', 'xlsx', 'pptx', 'doc', 'csv', 'docm'] }, ui: ui('office-editor') };
/** Another app that also opens Word documents, listed before the office editor (by name). */
const AWORD = { plugin: 'a-word', id: 'view', placement: 'viewer', label: { en: 'A Word' }, applies: { ext: ['docx'] }, ui: ui('a-word') };
const SVG = { plugin: 'vector', id: 'svg', placement: 'viewer', label: 'Vector', applies: { mime: ['image/svg+xml', 'image/*'] }, ui: ui('vector') };

const WITH_DS = { onlyoffice_url: 'https://docs.example.com', external: { onlyoffice: { enabled: true, state: 'ok', url: 'https://docs.example.com' } } };
const NO_DS = { external: { onlyoffice: { enabled: false, state: 'disabled' } } };

const actions = (views: unknown[], open_rules?: unknown) => ({ actions: [], views, ...(open_rules ? { open_rules } : {}) });

function rec(accountId: string, caps: unknown, views: unknown[], open_rules?: unknown): ServerRecord {
  return { accountId, serverUrl: `https://${accountId}.example.com`, readAt: 1, ...ow.serverAppsFrom(caps, actions(views, open_rules)) };
}

// ── what a server opens ──────────────────────────────────────────────

test('a server opens what its apps\' viewer interfaces open, with its Document Server', () => {
  const s = ow.serverAppsFrom(WITH_DS, actions([DESIGN, DRAWIO]));
  assert.equal(s.documentServer, true);
  assert.deepEqual(s.apps.map((a) => [a.plugin, a.view, a.ext]), [
    ['media-thumbs', 'design', ['psd', 'psb', 'af']],
    ['drawio', 'editor', ['drawio', 'dio']],
  ]);
  assert.deepEqual(s.apps[0]!.label, { en: 'Design', tr: 'Tasarım' });
  assert.deepEqual(ow.serverAppsFrom(null, null), { documentServer: false, apps: [], order: {}, uploadLimit: 0, choices: {} }, 'an answer that says nothing: nothing');
});

test('a view the explorer would not take is left out, never thrown on', () => {
  const bad = [
    { ...DESIGN, placement: 'modal' },
    { ...DESIGN, ui: undefined },
    { ...DESIGN, id: '../x' },
    { ...DESIGN, plugin: 'a b' },
    { ...DESIGN, applies: { ext: 'psd' } },
    { ...DESIGN, applies: { kind: 'dir', ext: ['psd'] } },
    // A rule about an app's own marks: no file on this computer carries one.
    { ...DESIGN, applies: { ext: ['psd'], state: ['pending'] } },
    { ...DESIGN, applies: { ext: ['psd'], min: 2, multi: true } },
    // Nothing well-formed left to open.
    { ...DESIGN, applies: { ext: ['PS D', '../x', ''] } },
    null,
    'x',
  ];
  assert.deepEqual(ow.serverAppsFrom(NO_DS, actions(bad)).apps, []);
  // Upper case and duplicates are the server's to fix; the desktop reads them lower-cased, once.
  assert.deepEqual(ow.serverAppsFrom(NO_DS, actions([{ ...DESIGN, applies: { ext: ['PSD', 'psd', 'tar.gz'] } }])).apps[0]!.ext, ['psd', 'tar.gz']);
});

test('Default apps: a kind an app is switched off for is not its, and the order is the administrator\'s', () => {
  const rules = {
    psd: { order: [], off: ['app:media-thumbs/design'] },
    docx: { order: ['app:office-editor/editor', 'app:a-word/view'], off: [] },
  };
  const s = ow.serverAppsFrom(NO_DS, actions([AWORD, DESIGN, OFFICE], rules));
  assert.deepEqual(s.apps.find((a) => a.plugin === 'media-thumbs')!.off, ['psd']);
  assert.deepEqual(ow.appsOpening(s, 'Logo.psd'), [], 'switched off for .psd');
  assert.deepEqual(ow.appsOpening(s, 'Logo.PSB').map((a) => a.plugin), ['media-thumbs'], 'still on for .psb');
  assert.deepEqual(ow.appsOpening(s, 'Rapor.docx').map((a) => a.plugin), ['office-editor', 'a-word'], 'the administrator\'s order first');
});

test('a media type rule opens the kinds core gives that type; a family too', () => {
  const s = ow.serverAppsFrom(NO_DS, actions([SVG]));
  assert.deepEqual(s.apps[0]!.mime, ['image/svg+xml', 'image/*']);
  assert.equal(ow.serverOpener(s, 'C:\\Users\\alice\\Desktop\\logo.svg')?.app, 'vector/svg');
  assert.equal(ow.serverOpener(s, '/home/alice/foto.PNG')?.app, 'vector/svg', 'image/* takes a .png');
  assert.equal(ow.serverOpener(s, 'notes.txt'), null);
});

test('how a server opens a file: its Document Server for an office document, else its first app', () => {
  const ds = ow.serverAppsFrom(WITH_DS, actions([OFFICE, DESIGN]));
  assert.deepEqual(ow.serverOpener(ds, 'Bütçe Özeti.xlsx'), { app: null, via: 'document-server' }, 'the page picks, as before');
  assert.equal(ow.serverOpener(ds, 'Rapor.docm')?.app, 'office-editor/editor', '.docm is not one the Document Server route takes');
  assert.equal(ow.serverOpener(ds, 'Logo.psd')?.app, 'media-thumbs/design');
  const app = ow.serverOpener(ds, 'Logo.psd')!;
  assert.equal(app.via, 'app');
  assert.deepEqual(app.label, { en: 'Design', tr: 'Tasarım' });

  const noDs = ow.serverAppsFrom(NO_DS, actions([AWORD, OFFICE]));
  assert.equal(ow.serverOpener(noDs, 'Rapor.docx')?.app, 'office-editor/editor', 'an office document: the office editor app ahead of the rest');
  assert.equal(ow.serverOpener(noDs, 'Rapor.rtf'), null, 'no Document Server and no app for it: the server does not open it');
  assert.equal(ow.serverOpener(ow.serverAppsFrom(NO_DS, actions([AWORD])), 'Rapor.docx')?.app, 'a-word/view');
});

// ── the person's own "always open with" ──────────────────────────────
// One record per ACCOUNT (GET /api/me/open-with, core lib/openWith): the
// browser and the desktop make the same choice for a kind - ahead of the
// administrator's order, only among the apps that are on for it.

/** A second app that opens Photoshop files, listed after the first by name. */
const PHOTO = { plugin: 'photo-edit', id: 'editor', placement: 'viewer', label: { en: 'Photo editor' }, applies: { ext: ['psd'] }, ui: ui('photo-edit') };

test('the person\'s choice for a kind comes before the administrator\'s order', () => {
  const rules = { psd: { order: ['app:media-thumbs/design', 'app:photo-edit/editor'], off: [] } };
  const none = ow.serverAppsFrom(NO_DS, actions([DESIGN, PHOTO], rules), { choices: {} });
  assert.equal(ow.serverOpener(none, 'Logo.psd')?.app, 'media-thumbs/design', 'no choice: the administrator\'s order');
  const mine = ow.serverAppsFrom(NO_DS, actions([DESIGN, PHOTO], rules), { choices: { psd: 'app:photo-edit/editor' } });
  assert.deepEqual(mine.choices, { psd: 'app:photo-edit/editor' });
  assert.equal(ow.serverOpener(mine, 'C:\\Users\\alice\\Masaüstü\\LOGO.PSD')?.app, 'photo-edit/editor', 'the person\'s, as the browser opens it');
  // …and the same on every server whose account made it; another account's choice is its own.
  const records = [
    { ...rec('work', NO_DS, [DESIGN, PHOTO], rules), choices: { psd: 'app:photo-edit/editor' } },
    rec('home', NO_DS, [DESIGN, PHOTO], rules),
  ];
  assert.deepEqual(candidatesFor('Logo.psd', records, ['work', 'home']).map((c) => [c.accountId, c.app]), [
    ['work', 'photo-edit/editor'],
    ['home', 'media-thumbs/design'],
  ]);
});

test('a choice is followed only where that app opens the file and is on; filex\'s own viewer is not taken', () => {
  const off = { psd: { order: [], off: ['app:photo-edit/editor'] } };
  const switchedOff = ow.serverAppsFrom(NO_DS, actions([DESIGN, PHOTO], off), { choices: { psd: 'app:photo-edit/editor' } });
  assert.equal(ow.serverOpener(switchedOff, 'Logo.psd')?.app, 'media-thumbs/design', 'switched off for .psd: the next that is on');
  const gone = ow.serverAppsFrom(NO_DS, actions([DESIGN]), { choices: { psd: 'app:photo-edit/editor' } });
  assert.equal(ow.serverOpener(gone, 'Logo.psd')?.app, 'media-thumbs/design', 'an app no longer installed');
  const builtin = ow.serverAppsFrom(NO_DS, actions([DESIGN]), { choices: { psd: 'builtin' } });
  assert.equal(ow.serverOpener(builtin, 'Logo.psd')?.app, 'media-thumbs/design', 'the window is for editing');
  // An office document: the person's choice even over the office editor app.
  const word = ow.serverAppsFrom(NO_DS, actions([AWORD, OFFICE]), { choices: { docx: 'app:a-word/view' } });
  assert.equal(ow.serverOpener(word, 'Rapor.docx')?.app, 'a-word/view');
  // With a Document Server the page picks (it reads the same choice itself).
  assert.equal(ow.serverOpener(ow.serverAppsFrom(WITH_DS, actions([AWORD, OFFICE]), { choices: { docx: 'app:a-word/view' } }), 'Rapor.docx')?.app, null);
});

test('only well-formed choices are kept; a server that says none has none', () => {
  assert.deepEqual(
    ow.openWithChoicesFrom({ choices: { psd: 'app:photo-edit/editor', csv: 'onlyoffice', md: 'builtin', 'tar.gz': 'builtin', PSD: 'builtin', x: 'app:../../evil', y: 7 } }),
    { psd: 'app:photo-edit/editor', csv: 'onlyoffice', md: 'builtin' },
  );
  assert.deepEqual(ow.openWithChoicesFrom(null), {});
  assert.deepEqual(ow.openWithChoicesFrom({ choices: ['psd'] }), {});
  assert.deepEqual(ow.serverAppsFrom(NO_DS, actions([DESIGN])).choices, {}, 'an older server: no choices');
});

test('officeEditorChoice is serverOpener\'s office editor case (one rule, two callers)', () => {
  for (const caps of [WITH_DS, NO_DS, null]) {
    for (const views of [[OFFICE], [AWORD, OFFICE], [AWORD], [DRAWIO]]) {
      for (const name of ['Rapor.docx', 'Eski.doc', 'Tablo.csv', 'Logo.psd']) {
        const o = ow.serverOpener(ow.serverAppsFrom(caps, actions(views)), name);
        const want = o?.via === 'app' && o.plugin === ow.OFFICE_EDITOR_APP ? o.app : null;
        assert.equal(ow.officeEditorChoice(caps, actions(views), name), want, `${name} with ${views.map((v) => v.plugin).join('+')}`);
      }
    }
  }
});

// ── what the OS is told ──────────────────────────────────────────────

test('the OS is told every kind an app on any server opens, beside the office documents', () => {
  const records = [rec('work', NO_DS, [DESIGN, OFFICE]), rec('home', WITH_DS, [DRAWIO, { ...DESIGN, applies: { ext: ['psd', 'pxd'] } }])];
  // The office documents are the installers' own (OFFICE_EXTENSIONS); .docm is not one of them.
  assert.deepEqual(registrableKinds(records), ['af', 'dio', 'docm', 'drawio', 'psb', 'psd', 'pxd']);
  assert.deepEqual(registrableKinds([]), [], 'no server: nothing');
});

test('never a program, never a kind an app is switched off for, never more than the cap', () => {
  const PROGRAMS = { plugin: 'evil', id: 'v', placement: 'viewer', label: 'x', applies: { ext: ['exe', 'lnk', 'bat', 'ps1', 'msi', 'desktop', 'appimage', 'setup.exe', 'txt'] }, ui: ui('evil') };
  assert.deepEqual(registrableKinds([rec('a', NO_DS, [PROGRAMS])]), ['txt']);
  for (const k of ['exe', 'lnk', 'url', 'scr', 'msi', 'reg', 'desktop', 'appimage', 'dmg']) assert.ok(NEVER_REGISTERED.has(k), k);
  assert.deepEqual(registrableKinds([rec('a', NO_DS, [DESIGN], { psd: { off: ['app:media-thumbs/design'] } })]), ['af', 'psb']);
  const many = { ...DESIGN, applies: { ext: Array.from({ length: 300 }, (_, i) => 'k' + String(i).padStart(3, '0')) } };
  assert.equal(registrableKinds([rec('a', NO_DS, [many])]).length, MAX_REGISTERED);
});

test('a media type an app names exactly reaches a Linux desktop entry; a family never does', () => {
  const PSD_TYPE = { ...DESIGN, applies: { mime: ['image/vnd.adobe.photoshop', 'image/*', 'application/msword'] } };
  assert.deepEqual(registrableMediaTypes([rec('a', NO_DS, [PSD_TYPE, SVG])]), ['image/svg+xml', 'image/vnd.adobe.photoshop']);
});

// ── which server opens a double-clicked file ─────────────────────────

test('the servers that open a file, one each, in the accounts\' order', () => {
  const records = [rec('work', WITH_DS, [OFFICE]), rec('home', NO_DS, [OFFICE, DESIGN]), rec('gone', NO_DS, [DESIGN])];
  const docx = candidatesFor('C:\\Belgeler\\Rapor.docx', records, ['home', 'work']);
  assert.deepEqual(docx.map((c) => [c.accountId, c.app, c.via]), [
    ['home', 'office-editor/editor', 'app'],
    ['work', null, 'document-server'],
  ]);
  assert.deepEqual(candidatesFor('Logo.psd', records, ['work', 'home']).map((c) => c.accountId), ['home'], 'an account no longer here is not asked');
  assert.deepEqual(candidatesFor('Notlar.fxtxt', records, ['work', 'home']), [], 'nobody opens it');
});

test('one server: it; a remembered choice while it still holds; more than one: the person is asked', () => {
  const records = [rec('work', NO_DS, [DESIGN]), rec('home', NO_DS, [DESIGN])];
  const both = candidatesFor('Logo.psd', records, ['work', 'home']);
  assert.equal(pickCandidate([]).kind, 'none');
  const one = pickCandidate(both.slice(0, 1));
  assert.ok(one.kind === 'one' && one.candidate.accountId === 'work' && !one.remembered);
  const ask = pickCandidate(both);
  assert.ok(ask.kind === 'ask' && ask.candidates.length === 2);
  const kept = pickCandidate(both, { accountId: 'home', app: 'media-thumbs/design' });
  assert.ok(kept.kind === 'one' && kept.candidate.accountId === 'home' && kept.remembered);
  // The app the choice named is gone from that server (another one opens the kind now): asked again.
  assert.equal(pickCandidate(both, { accountId: 'home', app: 'photo/edit' }).kind, 'ask');
  assert.equal(pickCandidate(both, { accountId: 'elsewhere', app: 'media-thumbs/design' }).kind, 'ask');
  assert.equal(choiceKey('/home/alice/Masaüstü/LOGO.PSD'), 'psd', 'remembered per kind');
});

test('a server that could not be asked keeps what it said last; a signed-out or removed account takes its kinds', () => {
  const prev = [rec('work', NO_DS, [DESIGN]), rec('home', NO_DS, [DRAWIO]), rec('old', NO_DS, [DRAWIO])];
  const fresh = [rec('home', NO_DS, [])];
  const out = mergeRecords(prev, fresh, new Set(['work', 'home']));
  assert.deepEqual(out.map((r) => [r.accountId, r.apps.length]), [['work', 1], ['home', 0]]);
  assert.deepEqual(registrableKinds(out), ['af', 'psb', 'psd'], 'drawio\'s kinds went with the app removed from home and with the account removed');
});

test('Settings lists every kind and who opens it; a label in the window\'s language', () => {
  const rows = kindRows([rec('work', NO_DS, [DESIGN]), rec('home', NO_DS, [{ ...DESIGN, applies: { ext: ['psd'] } }])]);
  assert.deepEqual(rows.map((r) => [r.ext, r.openers.map((o) => o.accountId)]), [['af', ['work']], ['psb', ['work']], ['psd', ['work', 'home']]]);
  assert.equal(labelIn({ en: 'Design', tr: 'Tasarım' }, 'tr'), 'Tasarım');
  assert.equal(labelIn({ en: 'Design' }, 'tr'), 'Design');
  assert.equal(labelIn(undefined, 'en'), '');
});

test('the record survives a restart, and a broken one reads as empty', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-appkinds-'));
  try {
    const file = path.join(dir, 'openwith-apps.json');
    const store = new AppKindsStore(file);
    assert.deepEqual(store.load(), emptyAppKinds(), 'nothing yet');
    const f = { ...emptyAppKinds(), servers: [rec('work', NO_DS, [DESIGN])], choices: { psd: { accountId: 'work', app: 'media-thumbs/design' } }, registered: { route: 'windows', kinds: ['af', 'psb', 'psd'], types: [], own: [] } };
    store.save(f);
    assert.deepEqual(store.load(), f);
    assert.equal(fs.existsSync(file + '.part'), false, 'written through a temp file and renamed');
    fs.writeFileSync(file, '{ not json');
    assert.deepEqual(store.load(), emptyAppKinds());
    fs.writeFileSync(file, JSON.stringify({ version: 2, servers: [] }));
    assert.deepEqual(store.load(), emptyAppKinds(), 'a version this app does not know');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('the Settings row says how many kinds come from the servers\' apps, in both languages', () => {
  const s = appStrings();
  assert.equal(s['ow.apps_count']?.[0], 'Kinds filex opens: {n}, from the apps on your servers');
  assert.equal(s['ow.apps_count']?.[1], 'filex ile açılabilen türler: {n}, sunuculardaki uygulamalardan');
  for (const k of ['ow.apps_sub_dynamic', 'ow.apps_sub_build', 'ow.apps_sub_none', 'ow.apps_none', 'ow.apps_on', 'ow.apps_refresh', 'ow.apps_show', 'ow.apps_hide', 'ow.apps_forget']) {
    assert.ok(s[k]?.[0] && s[k]?.[1], `${k} needs English and Turkish`);
    assert.notEqual(s[k]![0], s[k]![1], `${k}: the Turkish is a translation, not the English again`);
  }
  // The words that come out ASCII-folded when typed carelessly, as they are written.
  assert.match(s['ow.apps_none']![1], /hiçbir uygulama henüz bir dosya türü açmıyor/);
  assert.match(s['ow.apps_sub_dynamic']![1], /“Birlikte aç” listesinde görünür/);
});
