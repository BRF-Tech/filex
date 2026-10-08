// A language pack checkout made on the spot, for the suites of
// scripts/langpacks.mjs (tests/i18n/langPacksSync.test.ts) and of the
// nightly translation around it (tests/i18n/langPacksNightly.test.ts): a
// manifest, a translation, the catalogue it follows, a git history, and a
// stand-in for the template's scripts/pack.mjs and validators that keeps
// their contract - because a pack is another repository and these suites
// cannot reach one.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import { STATUS_CLOSE, STATUS_OPEN } from '../../../scripts/lib/langpacks.mjs';

export type Catalogue = Record<string, string>;
export type Rows = Record<string, { in: string; syntax: string; tr?: string; plural?: boolean }>;

export const write = (p: string, text: string) => {
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, text);
};
export const json = (v: unknown) => `${JSON.stringify(v, null, 2)}\n`;
export const readJson = (p: string) => JSON.parse(fs.readFileSync(p, 'utf8'));

export function git(dir: string, ...args: string[]): string {
  const r = spawnSync('git', ['-C', dir, ...args], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
  return r.stdout.trim();
}

/* A stand-in for the template's scripts/pack.mjs: the two commands
   langpacks.mjs runs, with the same contract. `sync --from <dir>` copies the
   catalogue, gives every translation each catalogue key (empty when new, in
   catalogue order) and keeps the rest after them; `build` writes the
   manifest's ui_locales from the non-empty values filex still knows (a key,
   or a plural form of one); `build --check` fails when the manifest is not
   what build would write. */
export const PACK_MJS = `import fs from 'node:fs';
import path from 'node:path';
const [cmd, ...args] = process.argv.slice(2);
const read = (p) => JSON.parse(fs.readFileSync(p, 'utf8'));
const text = (v) => JSON.stringify(v, null, 2) + '\\n';
const tags = fs.readdirSync('translations').filter((f) => f.endsWith('.json')).map((f) => f.slice(0, -5));
function manifest() {
  const m = read('filex-app.json');
  const en = read('catalogue/filex-catalogue-en.json');
  m.ui_locales = {};
  for (const t of tags) {
    const tr = read('translations/' + t + '.json');
    const done = {};
    for (const [k, v] of Object.entries(tr)) {
      const base = k.replace(/_(zero|one|two|few|many)$/, '');
      if (typeof v === 'string' && v.trim() && (k in en || base in en)) done[k] = v;
    }
    if (Object.keys(done).length) m.ui_locales[t] = done;
  }
  return m;
}
if (cmd === 'sync') {
  const from = args[args.indexOf('--from') + 1];
  for (const n of ['filex-catalogue-en.json', 'filex-catalogue-context.json']) {
    fs.writeFileSync(path.join('catalogue', n), fs.readFileSync(path.join(from, n)));
  }
  const en = read('catalogue/filex-catalogue-en.json');
  for (const t of tags) {
    const tr = read('translations/' + t + '.json');
    const next = {};
    for (const k of Object.keys(en)) next[k] = typeof tr[k] === 'string' ? tr[k] : '';
    for (const [k, v] of Object.entries(tr)) if (!(k in en)) next[k] = v;
    fs.writeFileSync('translations/' + t + '.json', text(next));
  }
  fs.writeFileSync('filex-app.json', text(manifest()));
} else if (cmd === 'build') {
  const want = text(manifest());
  if (args.includes('--check')) {
    if (want !== fs.readFileSync('filex-app.json', 'utf8')) {
      console.log('filex-app.json is out of date');
      process.exit(1);
    }
    console.log('filex-app.json is up to date.');
  } else fs.writeFileSync('filex-app.json', want);
}
`;

/* The pack's own check, as strict as the real packs' (validate-de.mjs,
   validate-fr.mjs, style-check.mjs): a key of translations/<tag>.json that
   the pack's catalogue does not have - neither a key nor a plural form of
   one - is an ERROR UNKNOWN. ⚠ The stand-in sync above KEEPS such a key, as
   every pack's pack.mjs did through 0.53: that pair is what put every pack
   back in 0.53 (`tenants.modeOff`), and it is why apply and release must
   drop the key themselves. Red on demand too (LANGPACK_TEST_RED). */
export const CHECK_MJS = `import fs from 'node:fs';
if (process.env.LANGPACK_TEST_RED) { console.log('pack check: 1 error'); process.exit(1); }
const read = (p) => JSON.parse(fs.readFileSync(p, 'utf8'));
const en = read('catalogue/filex-catalogue-en.json');
const unknown = [];
for (const f of fs.readdirSync('translations').filter((n) => n.endsWith('.json'))) {
  for (const k of Object.keys(read('translations/' + f))) {
    const base = k.replace(/_(zero|one|two|few|many)$/, '');
    if (!(k in en) && !(base in en)) unknown.push(f.slice(0, -5) + ' ' + k);
  }
}
if (unknown.length) {
  for (const k of unknown) console.log('ERROR UNKNOWN ' + k + ': key not in the catalogue');
  console.log('pack check: ' + unknown.length + ' error(s)');
  process.exit(1);
}
console.log('pack check: 0 errors, 0 warnings');
`;

export const PACK_README = `# filex - German (de) language pack

Intro.

${STATUS_OPEN}
| | |
|---|---|
| Version | 0.1.0, for filex 0.52.0 |
${STATUS_CLOSE}

## Install
`;

export function writeCatalogue(dir: string, strings: Catalogue, rows: Rows, filex: string) {
  write(path.join(dir, 'filex-catalogue-en.json'), json(strings));
  write(path.join(dir, 'filex-catalogue-context.json'), json({ filex, about: 'test', plural_categories: {}, keys: rows }));
}

export function makePack(
  parent: string,
  {
    catalogue,
    rows,
    translation,
    version = '0.1.0',
    name = 'filex-lang-de',
    tag = 'de',
    remote = true,
    glossary = '',
  }: {
    catalogue: Catalogue;
    rows: Rows;
    translation: Record<string, string>;
    version?: string;
    name?: string;
    tag?: string;
    remote?: boolean;
    glossary?: string;
  },
): string {
  const dir = path.join(parent, name);
  write(path.join(dir, 'filex-app.json'), json({ manifest_version: 1, name: `lang-${tag}`, version, label: { en: 'German language pack' }, permissions: [], ui_locales: {} }));
  write(path.join(dir, 'translations', `${tag}.json`), json(translation));
  writeCatalogue(path.join(dir, 'catalogue'), catalogue, rows, '0.52.0');
  write(path.join(dir, 'scripts', 'pack.mjs'), PACK_MJS);
  write(path.join(dir, 'scripts', 'check.mjs'), CHECK_MJS);
  write(
    path.join(dir, 'package.json'),
    json({ name, private: true, version, scripts: { validate: 'node scripts/pack.mjs build --check && node scripts/check.mjs' } }),
  );
  write(path.join(dir, 'README.md'), PACK_README);
  write(path.join(dir, 'validate-output.txt'), 'an older run\n');
  if (glossary) write(path.join(dir, 'glossary.md'), glossary);
  spawnSync(process.execPath, ['scripts/pack.mjs', 'build'], { cwd: dir });
  spawnSync('git', ['init', '-q', dir]);
  git(dir, 'symbolic-ref', 'HEAD', 'refs/heads/main');
  git(dir, 'config', 'user.name', 'Pack Test');
  git(dir, 'config', 'user.email', 'pack@example.com');
  git(dir, 'config', 'commit.gpgsign', 'false');
  git(dir, 'config', 'core.autocrlf', 'false');
  if (remote) git(dir, 'remote', 'add', 'github', 'https://example.invalid/filex-lang-de.git');
  git(dir, 'add', '-A');
  git(dir, 'commit', '-q', '-m', 'the pack');
  return dir;
}

/** Every file of a pack (but .git), as bytes: "nothing was written" is this, unchanged. */
export function filesOf(dir: string): Record<string, string> {
  const out: Record<string, string> = {};
  const walk = (d: string) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      if (e.name === '.git') continue;
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p);
      else out[path.relative(dir, p)] = fs.readFileSync(p).toString('base64');
    }
  };
  walk(dir);
  return out;
}
