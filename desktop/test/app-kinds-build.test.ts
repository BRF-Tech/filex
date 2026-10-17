// The kinds the app store's apps open, as the packages that cannot learn them
// while they run declare them (macOS, the Microsoft Store, the snap):
// ONE source (build/app-kinds.json, read from the store's signed catalog by
// scripts/app-kinds.mjs refresh), rendered into electron-builder.yml and
// build/appx-extensions.xml by `pnpm run build` - never written by hand.
//
// Run:  node --experimental-strip-types --test desktop/test/app-kinds-build.test.ts

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  REGIONS,
  catalogVersions,
  render,
  renderMac,
  renderStore,
  replaceRegion,
  snapMediaTypes,
  snapshotOf,
  staticKinds,
  verifyIndex,
  viewerKindsOf,
} from '../scripts/app-kinds.mjs';
import { KIND_RE, OFFICE_EXTENSIONS } from '../src/openwith.ts';
import { NEVER_REGISTERED } from '../src/app-kinds.ts';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const SNAPSHOT = JSON.parse(read('build/app-kinds.json'));

test('electron-builder.yml and the Store manifest are what build/app-kinds.json makes them', () => {
  const yml = read('electron-builder.yml');
  const xml = read('build/appx-extensions.xml');
  const out = render(SNAPSHOT, yml, xml);
  assert.equal(out.yml, yml, 'electron-builder.yml differs from its source: run `pnpm --dir desktop app-kinds` (never edit between the app-kinds markers)');
  assert.equal(out.xml, xml, 'build/appx-extensions.xml differs from its source: run `pnpm --dir desktop app-kinds`');
});

test('the source is the store\'s catalog, verified, and well-formed', () => {
  assert.equal(SNAPSHOT.store, 'https://apps.filex.sh');
  assert.match(SNAPSHOT.index_key, /^[0-9a-f]{64}$/, 'the index key the snapshot was verified with');
  const exts = SNAPSHOT.kinds.map((k: { ext: string }) => k.ext);
  assert.deepEqual(exts, [...exts].sort(), 'sorted');
  assert.equal(new Set(exts).size, exts.length, 'once each');
  for (const k of SNAPSHOT.kinds) {
    assert.ok(KIND_RE.test(k.ext), k.ext);
    assert.ok(Array.isArray(k.apps) && k.apps.length, `${k.ext} names the apps that open it`);
    assert.ok(k.linux === null || Array.isArray(k.linux), `${k.ext}: the media types a desktop gives it, or null for unknown`);
  }
  // The catalog this was first made from (index serial 27, 2026-10-16): the
  // owner's example among it.
  assert.ok(exts.includes('psd'));
});

test('a package declares the store\'s kinds beside the office documents, never twice, never a program', () => {
  const kinds = staticKinds(SNAPSHOT);
  for (const e of kinds) {
    assert.ok(!OFFICE_EXTENSIONS.includes(e as never), `${e} is the office entry's`);
    assert.ok(!NEVER_REGISTERED.has(e), e);
    assert.ok(!e.includes('.'), `${e}: a package types a file by its last extension`);
  }
  const xml = read('build/appx-extensions.xml');
  const assoc = (name: string) => {
    const m = new RegExp(`<uap3:FileTypeAssociation Name="${name.replace('.', '\\.')}"[\\s\\S]*?</uap3:FileTypeAssociation>`).exec(xml);
    assert.ok(m, `no ${name} association`);
    return [...m[0].matchAll(/<uap:FileType>\.([^<]+)<\/uap:FileType>/g)].map((x) => x[1]);
  };
  assert.deepEqual(assoc('filex.office').sort(), [...OFFICE_EXTENSIONS].sort());
  assert.deepEqual(assoc('filex.apps'), kinds);
  // An MSIX association takes at most 1000 file types (uap:SupportedFileTypes).
  assert.ok(kinds.length <= 1000);
});

test('macOS: the store\'s kinds at rank Alternate, quoted (an extension like 001 is a number to YAML)', () => {
  assert.deepEqual(renderMac(['001', 'psd']), ['    - ext:', '        - "001"', '        - "psd"', '      name: Document', '      role: Editor', '      rank: Alternate']);
  assert.deepEqual(renderMac([]), [], 'nothing to declare: no entry');
  const yml = read('electron-builder.yml');
  const mac = yml.slice(yml.indexOf(REGIONS.mac.begin), yml.indexOf(REGIONS.mac.end));
  assert.ok(!/rank: (Owner|Default)/.test(mac), 'never a rank that takes a kind without being chosen');
});

test('the snap: only the media types a desktop gives those kinds - a snap cannot define one', () => {
  const snap = {
    kinds: [
      { ext: 'psd', apps: ['a'], linux: ['image/vnd.adobe.photoshop'] },
      { ext: 'fxtxt', apps: ['b'], linux: [] },
      { ext: 'drawio', apps: ['c'], linux: null },
      { ext: 'docx', apps: ['d'], linux: ['application/vnd.openxmlformats-officedocument.wordprocessingml.document'] },
      { ext: 'svg', apps: ['e'], linux: ['image/*'] },
    ],
  };
  assert.deepEqual(snapMediaTypes(snap), ['image/vnd.adobe.photoshop'], 'the office one is linux.mimeTypes\'; a family never');
});

test('the Store association: no double hyphen anywhere (makeappx refuses the whole manifest)', () => {
  const xml = read('build/appx-extensions.xml');
  for (const m of xml.matchAll(/<!--([\s\S]*?)-->/g)) assert.ok(!m[1]!.includes('--'), m[1]!.slice(0, 60));
  assert.ok(!renderStore(['psd']).join('\n').includes('--'));
  assert.deepEqual(renderStore([]), []);
});

test('a region whose markers are missing is an error, not a silent append', () => {
  assert.throws(() => replaceRegion('a\nb\n', REGIONS.mac, ['x']), /markers/);
  const text = ['top', REGIONS.snap.begin, 'old', REGIONS.snap.end, 'end'].join('\n');
  assert.equal(replaceRegion(text, REGIONS.snap, ['new1', 'new2']), ['top', REGIONS.snap.begin, 'new1', 'new2', REGIONS.snap.end, 'end'].join('\n'));
  assert.equal(replaceRegion(text.replace(/\n/g, '\r\n'), REGIONS.snap, []), ['top', REGIONS.snap.begin, REGIONS.snap.end, 'end'].join('\r\n'), 'the file\'s own line ends');
});

// ── reading the store ────────────────────────────────────────────────

test('the index is checked as filex checks it: ed25519 over the lower-hex SHA-256 of the bytes', () => {
  const { publicKey, privateKey } = crypto.generateKeyPairSync('ed25519');
  const keyHex = Buffer.from(publicKey.export({ format: 'jwk' }).x!, 'base64url').toString('hex');
  const body = Buffer.from('{"schema":1,"serial":3,"apps":[]}');
  const sha = crypto.createHash('sha256').update(body).digest('hex');
  const sig = crypto.sign(null, Buffer.from(sha), privateKey);
  assert.equal(verifyIndex(body, sig.toString('hex'), keyHex), true);
  assert.equal(verifyIndex(body, sig.toString('base64'), keyHex), true, 'base64 too');
  assert.equal(verifyIndex(Buffer.from(body.toString() + ' '), sig.toString('hex'), keyHex), false, 'one byte more');
  const other = crypto.generateKeyPairSync('ed25519').publicKey;
  assert.equal(verifyIndex(body, sig.toString('hex'), Buffer.from(other.export({ format: 'jwk' }).x!, 'base64url').toString('hex')), false, 'another key');
});

const MANIFEST = (sha: string) => ({ url: 'https://raw.githubusercontent.com/example/app/0123/filex-app.json', sha256: sha });

test('the version read of each app: its newest not yanked, with an interface; never a revoked app or a language pack', () => {
  const sha = 'a'.repeat(64);
  const index = {
    apps: [
      { name: 'z-design', kind: 'app', latest: '2.0.0', revoked: null, versions: [{ version: '2.0.0', yanked: { at: 'x' }, ui: {}, manifest: MANIFEST(sha) }, { version: '1.9.0', yanked: null, ui: {}, manifest: MANIFEST(sha) }] },
      { name: 'convert', kind: 'app', latest: '0.2.0', revoked: null, versions: [{ version: '0.2.0', ui: null, manifest: MANIFEST(sha) }] },
      { name: 'lang-de', kind: 'language_pack', latest: '0.1.13', versions: [{ version: '0.1.13', ui: {}, manifest: MANIFEST(sha) }] },
      { name: 'gone', kind: 'app', latest: '1.0.0', revoked: { reason: 'x' }, versions: [{ version: '1.0.0', ui: {}, manifest: MANIFEST(sha) }] },
      { name: 'bad-pin', kind: 'app', latest: '1.0.0', versions: [{ version: '1.0.0', ui: {}, manifest: MANIFEST('nope') }] },
      { name: 'drawio', kind: 'app', latest: '31.5.3', versions: [{ version: '31.5.3', ui: {}, manifest: MANIFEST(sha) }] },
    ],
  };
  assert.deepEqual(catalogVersions(index).map((v: { name: string; version: string }) => [v.name, v.version]), [['drawio', '31.5.3'], ['z-design', '1.9.0']]);
});

test('what a manifest opens: its viewer views with an interface', () => {
  const m = {
    views: [
      { id: 'design', placement: 'viewer', ui: 'index.html', applies: { ext: ['PSD', 'af', '../x'], mime: ['image/vnd.adobe.photoshop'] } },
      { id: 'panel', placement: 'modal', ui: 'panel.html', applies: { ext: ['exe'] } },
      { id: 'old', placement: 'viewer', applies: { ext: ['bmp'] } },
    ],
  };
  assert.deepEqual(viewerKindsOf(m), { ext: ['af', 'psd'], mime: ['image/vnd.adobe.photoshop'] });
});

test('the snapshot: every extension with the apps that open it; media types kept until a Linux refresh', () => {
  const apps = [
    { name: 'media-thumbs', version: '0.1.0', ext: ['psd', 'otf'], mime: [] },
    { name: 'photo', version: '1.0.0', ext: ['psd', 'fxp'], mime: [] },
  ];
  const previous = { linux_types_from: 'shared-mime-info 2.4', kinds: [{ ext: 'psd', apps: ['media-thumbs'], linux: ['image/vnd.adobe.photoshop'] }] };
  const s = snapshotOf({ store: 'https://apps.filex.sh', indexKey: 'ab', serial: 28, apps, previous });
  assert.deepEqual(s.kinds, [
    { ext: 'fxp', apps: ['photo'], linux: null },
    { ext: 'otf', apps: ['media-thumbs'], linux: null },
    { ext: 'psd', apps: ['media-thumbs', 'photo'], linux: ['image/vnd.adobe.photoshop'] },
  ]);
  assert.equal(s.linux_types_from, 'shared-mime-info 2.4');
  const withGlobs = snapshotOf({ store: 'x', indexKey: 'ab', serial: 28, apps, previous, globs: new Map([['otf', { weight: 50, types: ['font/otf'] }]]), globsLabel: 'sm 2.4' });
  assert.deepEqual(withGlobs.kinds.map((k: { linux: unknown }) => k.linux), [[], ['font/otf'], []], 'a globs2 file decides for every kind');
});

test('the snap names only the type an app says it opens, when it says one (the running app\'s own rule)', () => {
  const globs = new Map([['otf', { weight: 50, types: ['application/vnd.oasis.opendocument.formula-template', 'font/otf'] }]]);
  const fontApp = [{ name: 'fonts', version: '1.0.0', ext: ['otf'], mime: ['font/otf'] }];
  const silent = [{ name: 'fonts', version: '1.0.0', ext: ['otf'], mime: [] }];
  const narrowed = snapshotOf({ store: 'x', indexKey: 'ab', serial: 1, apps: fontApp, globs, globsLabel: 'sm' });
  assert.deepEqual(narrowed.kinds[0].linux, ['font/otf'], 'not a formula template');
  const all = snapshotOf({ store: 'x', indexKey: 'ab', serial: 1, apps: silent, globs, globsLabel: 'sm' });
  assert.deepEqual(all.kinds[0].linux, ['application/vnd.oasis.opendocument.formula-template', 'font/otf'], 'the app said nothing: every type, as before');
});
