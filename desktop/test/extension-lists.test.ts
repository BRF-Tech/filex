// "Open with filex" is declared in five places, and they must agree.
//
// Run:  node --experimental-strip-types --test desktop/test/extension-lists.test.ts
//
// OFFICE_EXTENSIONS (src/openwith.ts) is what the app will actually open. The
// operating systems learn it from files that cannot import TypeScript:
// electron-builder.yml (mac.fileAssociations, linux.mimeTypes),
// build/installer.nsh (the NSIS build's Windows registration) and
// build/appx-extensions.xml (the Microsoft Store package). A list widened in
// one place and not the others is an app that shows up in "Open with" on one
// OS and not another — or offers to open a type it then refuses. They used to
// be kept in step by a comment; this keeps them in step by failing.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { OFFICE_EXTENSIONS, OFFICE_MIME_TYPES } from '../src/openwith.ts';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const sorted = (xs: Iterable<string>) => [...xs].sort();
const EXPECTED = sorted(OFFICE_EXTENSIONS);

/** The lines of one top-level YAML block (`mac:`, `linux:` …). */
function yamlBlock(yml: string, key: string): string {
  const m = new RegExp(`^${key}:\\n((?:[ \\t].*\\n|\\s*\\n)*)`, 'm').exec(yml);
  assert.ok(m, `electron-builder.yml has no top-level "${key}:" block`);
  return m[1];
}

test('the MIME table covers exactly the extensions', () => {
  assert.deepEqual(sorted(Object.keys(OFFICE_MIME_TYPES)), EXPECTED);
});

test('mac.fileAssociations names the same extensions', () => {
  const block = yamlBlock(read('electron-builder.yml'), 'mac');
  const ext = /fileAssociations:[\s\S]*?ext:\n((?:\s+-\s+\w+\n)+)/.exec(block);
  assert.ok(ext, 'mac.fileAssociations[0].ext not found');
  assert.deepEqual(sorted(ext[1].match(/\w+/g) ?? []), EXPECTED);
});

test('linux.mimeTypes names the same types', () => {
  const block = yamlBlock(read('electron-builder.yml'), 'linux');
  const list = /mimeTypes:\n((?:[ \t]+(?:-[ \t]+\S+|#.*)\n)+)/.exec(block);
  assert.ok(list, 'linux.mimeTypes not found');
  // `x-scheme-handler/filex` sits in the same list — it is the filex:// link,
  // not a document type (see the yml, and test/linux-desktop-entry.test.ts).
  const declared = [...list[1].matchAll(/^[ \t]+-[ \t]+(\S+)$/gm)]
    .map((m) => m[1])
    .filter((t) => !t.startsWith('x-scheme-handler/'));
  assert.deepEqual(sorted(declared), sorted(Object.values(OFFICE_MIME_TYPES)));
});

test('the NSIS installer claims and releases the same extensions', () => {
  const nsh = read('build/installer.nsh');
  const claim = [...nsh.matchAll(/!insertmacro filexClaimExt "\.(\w+)"/g)].map((m) => m[1]);
  const release = [...nsh.matchAll(/!insertmacro filexReleaseExt "\.(\w+)"/g)].map((m) => m[1]);
  assert.deepEqual(sorted(claim), EXPECTED);
  // Uninstall must take back everything install added.
  assert.deepEqual(sorted(release), EXPECTED);
});

test('the Microsoft Store package declares the same extensions', () => {
  const xml = read('build/appx-extensions.xml');
  const types = [...xml.matchAll(/<uap:FileType>\.(\w+)<\/uap:FileType>/g)].map((m) => m[1]);
  assert.deepEqual(sorted(types), EXPECTED);
});
