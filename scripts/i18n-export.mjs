#!/usr/bin/env node
/**
 * i18n-export — the complete filex string catalogue, for translators.
 *
 *   node scripts/i18n-export.mjs [--out <dir>] [--no-where] [--lang <tag> …]
 *
 * Writes two files (default directory: ./filex-catalogue):
 *
 *   filex-catalogue-en.json       { "<key>": "<English>" } — EXACTLY the shape
 *                                 of one language in a pack's `ui_locales`.
 *                                 Copy it to translations/<lang>.json and
 *                                 translate the values.
 *   filex-catalogue-context.json  per key: which table it belongs to (`in`:
 *                                 explorer · admin · both · server) and so which
 *                                 grammar its translation must follow
 *                                 (`syntax`), the Turkish reference (`tr`), the
 *                                 source files that use it (`where`) — or, for
 *                                 the text the server writes, which mail or
 *                                 page shows it (`about`) and what each
 *                                 placeholder holds (`vars`) — and whether it
 *                                 takes plural forms (`plural`). Top level:
 *                                 `plural_categories`, the CLDR categories of
 *                                 ~60 languages (the forms a plural needs in
 *                                 each); `--lang <tag>` adds any other.
 *
 * The same two files are built into every filex binary — the running server
 * serves them at /admin/i18n/filex-catalogue-en.json — and attached to every
 * release, so a translator never needs this repository.
 *
 * Node alone: no install, no build (the sources are read as text).
 * Format rules: docs/PLUGIN-KIT.md → "Writing a language pack".
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { catalogueFiles, pluralCategories } from './lib/i18n-catalogue.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const argv = process.argv.slice(2);

// ⚠ `--help` used to fall through and WRITE the catalogue into the current
// directory — the one flag a person runs to find out what a tool does before
// letting it do anything (translator report, 2026-09-22).
if (argv.includes('--help') || argv.includes('-h')) {
  console.log(`usage: node scripts/i18n-export.mjs [--out <dir>] [--no-where] [--lang <tag> …]

Writes the complete filex string catalogue for translators:
  filex-catalogue-en.json       { "<key>": "<English>" } — one language of a pack's ui_locales
  filex-catalogue-context.json  per key: its table, grammar, the Turkish reference, where it is used

  --out <dir>    where to write (default: ./filex-catalogue)
  --no-where     leave out the source files each key is used in
  --lang <tag>   add the CLDR plural categories of another language (repeatable)
  -h, --help     print this and exit

Format rules: docs/PLUGIN-KIT.md → "Writing a language pack".`);
  process.exit(0);
}
const opt = (name, dflt) => {
  const i = argv.indexOf(name);
  return i >= 0 && argv[i + 1] ? argv[i + 1] : dflt;
};
const langs = argv.flatMap((a, i) => (a === '--lang' && argv[i + 1] ? [argv[i + 1]] : []));
const out = path.resolve(opt('--out', 'filex-catalogue'));
const files = catalogueFiles(root, { where: !argv.includes('--no-where'), langs });

fs.mkdirSync(out, { recursive: true });
for (const [name, text] of Object.entries(files)) {
  fs.writeFileSync(path.join(out, name), text);
}
const strings = JSON.parse(files['filex-catalogue-en.json']);
const n = Object.keys(strings).length;
const srv = Object.keys(strings).filter((k) => k.startsWith('server.')).length;
console.log(`${n} strings (${srv} of them the server's) → ${path.relative(process.cwd(), out) || out}/{${Object.keys(files).join(',')}}`);
for (const l of langs) console.log(`  ${l}: plural categories ${pluralCategories(l).join(', ')}`);
