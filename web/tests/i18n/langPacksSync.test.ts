// The language packs kept in step with this tree (#177): scripts/langpacks.mjs
// and scripts/lib/langpacks.mjs.
//
// ⚠ Why this exists: through 0.52 every release translated its new strings on
// release day - 189 keys for 0.51, 119 + 168 for 0.52 (PR #90 alone cost
// 76 minutes on the critical path) - and `pack.mjs sync`, the only tool,
// kept a translation whose English had changed without a word. Now the
// packs follow `main` between releases: `status` names what every pack lacks
// against this tree, `todo` writes the translator's worklists, `apply` folds
// the answers in (checking them before a file is written) and commits
// locally, and `release` is the whole release-day step: the release
// catalogue, one patch version up, the README's status block, the
// validators, a commit, and the tag and push commands printed.
//
// The packs here are made on the spot in a temporary directory
// (tests/helpers/langPacks.ts): a manifest, a translation, a catalogue, and a
// stand-in for the template's scripts/pack.mjs that keeps its contract
// (`sync --from <dir>`, `build`, `build --check`), because a pack is another
// repository and this suite cannot reach one.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import { buildCatalogue } from '../../../scripts/lib/i18n-catalogue.mjs';
import {
  FIXED_NAMES,
  STATUS_CLOSE,
  STATUS_OPEN,
  bumpPatch,
  findPacks,
  formsNeeded,
  groupKeys,
  knownKey,
  NIGHTLY_REMOTE,
  mergeItems,
  nameProblem,
  packDiff,
  packParents,
  publishRemote,
  pullStep,
  replaceStatusBlock,
  staleKeys,
  statusBlock,
  syncedTranslation,
  tableCounts,
  templateReadme,
  withoutStale,
  worklistItems,
} from '../../../scripts/lib/langpacks.mjs';
import { type Catalogue, type Rows, PACK_README as README, filesOf, git, json, makePack, readJson, write, writeCatalogue } from '../helpers/langPacks';

const ROOT = path.resolve(__dirname, '../../..');
const CLI = path.join(ROOT, 'scripts', 'langpacks.mjs');
const SLOW = { timeout: 60_000 };

const temps: string[] = [];
function tmp(prefix: string): string {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  temps.push(d);
  return d;
}
afterAll(() => {
  for (const d of temps) fs.rmSync(d, { recursive: true, force: true });
});

function cli(args: string[], env: Record<string, string> = {}) {
  const r = spawnSync(process.execPath, [CLI, ...args], { cwd: ROOT, encoding: 'utf8', env: { ...process.env, ...env } });
  return { code: r.status, out: r.stdout ?? '', err: r.stderr ?? '' };
}

// The catalogue the pack was translated against, and the one this tree has
// now: `b.office` reworded, `c.gone` removed, `d.new` added.
const PREV: Catalogue = {
  'a.count': '{n} files',
  'a.count_one': '{n} file',
  'a.title': 'Files',
  'b.office': 'Open in ONLYOFFICE',
  'c.gone': 'Old thing',
};
const NEXT: Catalogue = {
  'a.count': '{n} files',
  'a.count_one': '{n} file',
  'a.title': 'Files',
  'b.office': 'Edit in ONLYOFFICE',
  'd.new': 'New thing',
};
const ROWS: Rows = {
  'a.count': { in: 'explorer', syntax: 'plain', tr: '{n} dosya', plural: true },
  'a.count_one': { in: 'explorer', syntax: 'plain', tr: '{n} dosya' },
  'a.title': { in: 'explorer', syntax: 'plain', tr: 'Dosyalar' },
  'b.office': { in: 'admin', syntax: 'vue-i18n', tr: "ONLYOFFICE'ta düzenle" },
  'c.gone': { in: 'explorer', syntax: 'plain' },
  'd.new': { in: 'explorer', syntax: 'plain', tr: 'Yeni şey' },
};
const DE = {
  'a.count': '{n} Dateien',
  'a.count_one': '{n} Datei',
  'a.title': 'Dateien',
  'b.office': 'In ONLYOFFICE öffnen',
  'c.gone': 'Altes Ding',
};

/* -- the pure part ------------------------------------------------------ */

describe('what a pack still needs', () => {
  it('names the keys with no text, the ones whose English changed, and the ones filex dropped', () => {
    const d = packDiff({ prev: PREV, next: NEXT, translation: DE });
    expect(d.missing).toEqual(['d.new']);
    expect(d.changed).toEqual(['b.office']);
    expect(d.removed).toEqual(['c.gone']);
  });

  it('counts an empty value as missing, and a new key translated ahead as done', () => {
    const d = packDiff({ prev: PREV, next: NEXT, translation: { ...DE, 'a.title': '  ', 'd.new': 'Neues Ding' } });
    expect(d.missing).toEqual(['a.title']);
    expect(d.changed).toEqual(['b.office']);
  });

  it("takes the plural forms a language needs beyond the catalogue's keys from CLDR", () => {
    const row = ROWS['a.count'];
    // German: `one` is the catalogue's own `a.count_one` - an item of its own.
    expect(formsNeeded('de', 'a.count', row, NEXT)).toEqual([]);
    // Arabic writes zero, two, few and many beside it.
    expect(formsNeeded('ar', 'a.count', row, NEXT)).toEqual(['zero', 'two', 'few', 'many']);
    // A count the English writes with no `_one` key needs `one` in German.
    expect(formsNeeded('de', 'x.n', { in: 'server', syntax: 'server', plural: true }, { 'x.n': '{count} days' })).toEqual(['one']);
    // The admin panel writes its forms inside the string.
    expect(formsNeeded('ar', 'p.n', { in: 'admin', syntax: 'vue-i18n', plural: true }, { 'p.n': 'one | {n} more' })).toEqual([]);
    expect(formsNeeded('ar', 'a.count_one', { in: 'explorer', syntax: 'plain' }, NEXT)).toEqual([]);
  });

  it('gives a translator the English, the old English and the old translation, the Turkish and the forms to write', () => {
    const d = packDiff({ prev: PREV, next: NEXT, translation: DE });
    const items = worklistItems({ lang: 'de', diff: d, prev: PREV, next: NEXT, context: { keys: ROWS }, translation: DE });
    expect(items.map((i: { key: string }) => i.key)).toEqual(['b.office', 'd.new']);
    expect(items[0]).toMatchObject({ kind: 'changed', en: 'Edit in ONLYOFFICE', was: 'Open in ONLYOFFICE', current: 'In ONLYOFFICE öffnen', in: 'admin', syntax: 'vue-i18n', text: '' });
    expect(items[1]).toMatchObject({ kind: 'missing', en: 'New thing', tr: 'Yeni şey', text: '' });
    const ar = worklistItems({
      lang: 'ar',
      diff: { missing: [], changed: ['a.count'], removed: [] },
      prev: { ...NEXT, 'a.count': '{n} items' },
      next: NEXT,
      context: { keys: ROWS },
      translation: { 'a.count': 'old', 'a.count_few': 'old few' },
    });
    expect(ar[0]).toMatchObject({ forms_needed: ['zero', 'two', 'few', 'many'], current_forms: { few: 'old few' }, forms: {} });
  });
});

describe('the keys filex no longer has (#195)', () => {
  // 0.53 dropped `tenants.modeOff`. Every pack's `pack.mjs sync` kept it in
  // translations/<tag>.json, the packs' own validators refuse such a key
  // (ERROR UNKNOWN), and `apply` put every pack back until the key was
  // deleted by hand in each one. apply and release drop it themselves now.
  const TR = { 'a.count': '{n} Dateien', 'a.count_few': 'x', 'a.title': 'Dateien', 'c.gone': 'Altes Ding', 'c.gone_few': 'alt', 'b.office': 'In ONLYOFFICE bearbeiten', 'e.empty': '' };

  it('are the keys that are neither a catalogue key nor a plural form of one, filled or empty', () => {
    expect(staleKeys(TR, NEXT)).toEqual(['c.gone', 'c.gone_few', 'e.empty']);
    expect(knownKey('a.title', NEXT)).toBe(true);
    expect(knownKey('a.count_few', NEXT)).toBe(true);
    expect(knownKey('c.gone', NEXT)).toBe(false);
    expect(knownKey('c.gone_few', NEXT)).toBe(false);
    expect(staleKeys({ 'a.title': 'Dateien', 'd.new': '' }, NEXT)).toEqual([]);
  });

  it('leave the translation, and everything else keeps its order and its text', () => {
    const out = withoutStale(TR, NEXT);
    expect(Object.keys(out)).toEqual(['a.count', 'a.count_few', 'a.title', 'b.office']);
    expect(out['a.count_few']).toBe('x');
    expect(TR).toHaveProperty('c.gone');
  });

  it("keep a form the English no longer writes as a key of its own: it is the language's form of its base", () => {
    const next: Catalogue = { ...NEXT };
    delete next['a.count_one'];
    expect(staleKeys({ 'a.count': '{n} Dateien', 'a.count_one': '{n} Datei' }, next)).toEqual([]);
  });

  it("the stand-in pack refuses such a key as the real packs' validators do, and its sync keeps it, as theirs did", SLOW, () => {
    // Without both, the apply and release tests below would pass whatever
    // apply and release do with a key filex dropped.
    const base = tmp('filex-langpacks-strict-');
    const pack = makePack(base, { catalogue: { ...NEXT, 'c.gone': 'Old thing' }, rows: ROWS, translation: { ...DE, 'b.office': 'In ONLYOFFICE bearbeiten', 'd.new': 'Neues Ding' }, remote: false });
    const check = () => spawnSync(process.execPath, ['scripts/check.mjs'], { cwd: pack, encoding: 'utf8' });
    expect(check().status).toBe(0);
    const now = path.join(base, 'catalogue-now');
    writeCatalogue(now, NEXT, ROWS, '0.53.0');
    const sync = spawnSync(process.execPath, ['scripts/pack.mjs', 'sync', '--from', now], { cwd: pack, encoding: 'utf8' });
    expect(sync.status).toBe(0);
    expect(readJson(path.join(pack, 'translations', 'de.json'))['c.gone']).toBe('Altes Ding');
    const r = check();
    expect(r.status).toBe(1);
    expect(r.stdout).toContain('ERROR UNKNOWN de c.gone');
  });
});

describe('the answers', () => {
  it('keeps the fixed names as the English writes them (ONLYOFFICE, filex)', () => {
    expect(FIXED_NAMES).toContain('ONLYOFFICE');
    expect(nameProblem('Edit in ONLYOFFICE', 'In OnlyOffice bearbeiten')).toMatch(/ONLYOFFICE/);
    expect(nameProblem('Edit in ONLYOFFICE', 'Im Editor bearbeiten')).toMatch(/ONLYOFFICE/);
    expect(nameProblem('Restart filex', 'Filex neu starten')).toMatch(/filex/);
    // A spelling the English itself uses (an environment variable) is not one.
    expect(nameProblem('Set FILEX_PUBLIC_URL for filex', 'FILEX_PUBLIC_URL für filex setzen')).toBe('');
    expect(nameProblem('Edit in ONLYOFFICE', 'In ONLYOFFICE bearbeiten')).toBe('');
  });

  it('refuses an empty answer, a key the catalogue does not have, and a form for a category the key does not take', () => {
    const { problems } = mergeItems(DE, [
      { key: 'd.new', text: ' ' },
      { key: 'z.none', text: 'x' },
      { key: 'a.count', text: '{n} Dateien', forms_needed: ['one'], forms: { few: 'x' } },
    ], { next: { ...NEXT, 'a.count': '{n} files' } });
    expect(problems.map((p: { code: string; key: string }) => `${p.code} ${p.key}`)).toEqual(['UNTRANSLATED d.new', 'UNKNOWN z.none', 'FORM a.count']);
  });

  it("writes the answers in the translation's order, each key's forms right after it, exactly the forms given", () => {
    const synced = syncedTranslation({ 'a.count': 'alt', 'a.count_few': 'alt few', 'a.count_many': 'alt many', 'a.title': 'Titel' }, { 'a.count': '{n} files', 'a.title': 'Files', 'd.new': 'New thing' });
    // sync's shape: catalogue keys first (new ones empty), the rest after them.
    expect(Object.keys(synced)).toEqual(['a.count', 'a.title', 'd.new', 'a.count_few', 'a.count_many']);
    const { translation, problems } = mergeItems(
      synced,
      [
        { key: 'a.count', text: 'كل {n}', forms_needed: ['zero', 'two', 'few', 'many'], forms: { many: 'كثير {n}', zero: 'لا شيء' } },
        { key: 'd.new', text: 'جديد' },
      ],
      { next: { 'a.count': '{n} files', 'a.title': 'Files', 'd.new': 'New thing' } },
    );
    expect(problems).toEqual([]);
    expect(Object.keys(translation)).toEqual(['a.count', 'a.count_zero', 'a.count_many', 'a.title', 'd.new']);
    expect(translation['a.count_few']).toBeUndefined();
    expect(translation['d.new']).toBe('جديد');
  });
});

describe('release day', () => {
  it('moves a pack one patch version per filex release', () => {
    expect(bumpPatch('0.1.9')).toBe('0.1.10');
    expect(bumpPatch('1.4.0')).toBe('1.4.1');
    expect(() => bumpPatch('0.1.9-rc.1')).toThrow();
  });

  it("rewrites the README's status block and nothing around it, and says when there is none", () => {
    const counts = tableCounts({ keys: ROWS });
    expect(counts).toEqual({ admin: 1, explorer: 5, server: 0, both: 0 });
    const block = statusBlock({ version: '0.1.10', filex: '0.53.0', total: 5844, counts, languages: [{ tag: 'de', translated: 5844, total: 5844, percent: 100, extra: 1 }] });
    expect(block).toContain('| Version | 0.1.10, for filex 0.53.0 |');
    expect(block).toContain('100 % - 5,844 of 5,844 strings, plus 1 extra plural form |');
    const out = replaceStatusBlock(README, block);
    expect(out).not.toBeNull();
    expect(out!.startsWith('# filex - German (de) language pack\n\nIntro.\n\n')).toBe(true);
    expect(out!.endsWith(`${STATUS_CLOSE}\n\n## Install\n`)).toBe(true);
    expect(out).not.toContain('0.1.0, for filex 0.52.0');
    expect(replaceStatusBlock('# no block here\n', block)).toBeNull();
  });

  it("names the template's new catalogue in its README, with the README's own digit separator", () => {
    const nnbsp = String.fromCharCode(0x202f);
    const before = `gone, because filex's catalogue is 5${nnbsp}502 keys. One language\n\nThe catalogue here is for **filex v0.51.0**. Every filex release attaches its\n`;
    const after = templateReadme(before, '0.53.0', 5844);
    expect(after).toContain(`filex's catalogue is 5${nnbsp}844 keys. One language`);
    expect(after).toContain('The catalogue here is for **filex v0.53.0**. Every');
  });

  it('names key groups for a commit body', () => {
    expect(groupKeys(['tenancy.on', 'tenancy.off', 'nav.tenancy'])).toBe('tenancy.* (2), nav.tenancy');
  });

  it('writes no long dash into a pack: the block, the rules and the messages use the plain hyphen', () => {
    const block = statusBlock({ version: '0.1.1', filex: '0.53.0', total: 1, counts: { admin: 1, explorer: 0, server: 0, both: 0 }, languages: [{ tag: 'de', translated: 1, total: 1, percent: 100, extra: 0 }] });
    const src = fs.readFileSync(path.join(ROOT, 'scripts', 'lib', 'langpacks.mjs'), 'utf8');
    for (const text of [block, src]) {
      expect(text.includes(String.fromCharCode(0x2014)) || text.includes(String.fromCharCode(0x2013))).toBe(false);
    }
  });
});

describe('where the packs are', () => {
  it('finds the filex-lang-* packs beside the main checkout from one of its worktrees, the template and a non-pack left out', () => {
    const base = tmp('filex-langpacks-find-');
    const checkout = path.join(base, 'filex');
    const worktree = path.join(checkout, '.claude', 'worktrees', 'fx');
    fs.mkdirSync(worktree, { recursive: true });
    expect(packParents(worktree)).toEqual([path.join(checkout, '.claude', 'worktrees'), base]);
    makePack(base, { catalogue: PREV, rows: ROWS, translation: DE, remote: false });
    makePack(base, { catalogue: PREV, rows: ROWS, translation: DE, name: 'filex-lang-template', remote: false });
    fs.mkdirSync(path.join(base, 'filex-lang-notes'));
    expect(findPacks(worktree, { env: {} })).toEqual([path.join(base, 'filex-lang-de')]);
    expect(findPacks(checkout, { env: {} })).toEqual([path.join(base, 'filex-lang-de')]);
    // An explicit list, then FILEX_LANG_PACKS, win over the neighbours.
    expect(findPacks(worktree, { packs: [path.join(base, 'x')], env: {} })).toEqual([path.join(base, 'x')]);
    expect(findPacks(worktree, { env: { FILEX_LANG_PACKS: [path.join(base, 'a'), path.join(base, 'b')].join(path.delimiter) } })).toEqual([path.join(base, 'a'), path.join(base, 'b')]);
  });
});

/* -- the command ------------------------------------------------------- */

describe('langpacks --help', () => {
  it('prints usage, exits 0 and writes nothing', () => {
    const dir = tmp('filex-langpacks-help-');
    const r = spawnSync(process.execPath, [CLI, '--help'], { cwd: dir, encoding: 'utf8' });
    expect(r.status).toBe(0);
    expect(r.stdout).toMatch(/^usage: /);
    for (const c of ['status', 'todo', 'apply', 'pull', 'release']) expect(r.stdout).toContain(`  ${c} `);
    expect(fs.readdirSync(dir)).toEqual([]);
  });

  it('refuses an unknown option rather than ignoring it', () => {
    expect(cli(['status', '--frobnicate']).code).toBe(2);
  });
});

describe('status, todo and apply: a pack follows this tree', () => {
  let pack = '';
  let target = '';
  let work = '';
  let packArgs: string[] = [];
  beforeAll(() => {
    const base = tmp('filex-langpacks-flow-');
    pack = makePack(base, { catalogue: PREV, rows: ROWS, translation: DE });
    target = path.join(base, 'catalogue-now');
    writeCatalogue(target, NEXT, ROWS, '0.52.0+abcdef12');
    work = path.join(base, 'worklist');
    packArgs = ['--packs', pack];
  }, 60_000);

  it('status names what is pending, and --check turns it into exit 1', SLOW, () => {
    const r = cli(['status', ...packArgs, '--catalogue', target, '--json', '-']);
    expect(r.code).toBe(0);
    const report = JSON.parse(r.out);
    expect(report.pending).toBe(2);
    expect(report.packs[0].languages.de).toMatchObject({ missing: 1, changed: 1, removed: 1 });
    expect(report.packs[0].languages.de.keys).toEqual({ missing: ['d.new'], changed: ['b.office'], removed: ['c.gone'] });
    expect(cli(['status', ...packArgs, '--catalogue', target, '--check']).code).toBe(1);
    const text = cli(['status', ...packArgs, '--catalogue', target, '--keys']).out;
    expect(text).toContain('missing  d.new');
    expect(text).toContain('changed  b.office');
  });

  it('todo writes a worklist, the catalogue it was made from and the guide, and touches no pack', SLOW, () => {
    const before = filesOf(pack);
    const r = cli(['todo', ...packArgs, '--catalogue', target, '--out', work]);
    expect(r.code, r.err).toBe(0);
    expect(filesOf(pack)).toEqual(before);
    const wl = readJson(path.join(work, 'filex-lang-de.json'));
    expect(wl.worklist).toBe(1);
    expect(wl.filex.commit).toBe('abcdef12');
    expect(wl.languages.de.items.map((i: { key: string; kind: string }) => `${i.kind} ${i.key}`)).toEqual(['changed b.office', 'missing d.new']);
    expect(wl.rules.join(' ')).toContain('ONLYOFFICE');
    expect(readJson(path.join(work, 'catalogue', 'filex-catalogue-en.json'))).toEqual(NEXT);
    expect(fs.readFileSync(path.join(work, 'AGENT.md'), 'utf8')).toContain('apply --worklist');
    expect(readJson(path.join(work, 'status.json')).pending).toBe(2);
  });

  const answer = (texts: Record<string, string>) => {
    const file = path.join(work, 'filex-lang-de.json');
    const wl = readJson(file);
    for (const it of wl.languages.de.items) it.text = texts[it.key] ?? '';
    fs.writeFileSync(file, json(wl));
  };

  it('apply refuses a wrong answer and writes nothing to the pack', SLOW, () => {
    answer({ 'b.office': 'In OnlyOffice bearbeiten', 'd.new': 'Neues Ding' });
    const before = filesOf(pack);
    const head = git(pack, 'rev-parse', 'HEAD');
    const r = cli(['apply', '--worklist', work, '--commit']);
    expect(r.code).toBe(1);
    expect(r.out).toContain('NAME');
    expect(r.out).toContain('b.office');
    expect(filesOf(pack)).toEqual(before);
    expect(git(pack, 'rev-parse', 'HEAD')).toBe(head);
  });

  it("apply puts the pack back as it was when the pack's own validator goes red", SLOW, () => {
    answer({ 'b.office': 'In ONLYOFFICE bearbeiten', 'd.new': 'Neues Ding' });
    const before = filesOf(pack);
    const head = git(pack, 'rev-parse', 'HEAD');
    const r = cli(['apply', '--worklist', work, '--commit'], { LANGPACK_TEST_RED: '1' });
    expect(r.code).toBe(1);
    expect(r.out).toContain('put back as it was');
    expect(filesOf(pack)).toEqual(before);
    expect(git(pack, 'rev-parse', 'HEAD')).toBe(head);
    expect(git(pack, 'status', '--porcelain')).toBe('');
  });

  it('apply folds the answers in, drops the key filex no longer has, validates and commits locally', SLOW, () => {
    answer({ 'b.office': 'In ONLYOFFICE bearbeiten', 'd.new': 'Neues Ding' });
    const r = cli(['apply', '--worklist', work, '--commit', '--trailer', 'Co-Authored-By: Test Agent <agent@example.com>']);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    const tr = readJson(path.join(pack, 'translations', 'de.json'));
    expect(tr['b.office']).toBe('In ONLYOFFICE bearbeiten');
    expect(tr['d.new']).toBe('Neues Ding');
    // #195: the key filex dropped leaves the translation. The pack's sync
    // keeps it and the pack's validator refuses it (ERROR UNKNOWN): through
    // 0.53 apply went red right here and put the pack back.
    expect(tr).not.toHaveProperty('c.gone');
    expect(r.out).toContain('dropped 1 key(s) filex no longer has: de c.gone');
    const manifest = readJson(path.join(pack, 'filex-app.json'));
    expect(manifest.version).toBe('0.1.0');
    expect(manifest.ui_locales.de['c.gone']).toBeUndefined();
    expect(manifest.ui_locales.de['d.new']).toBe('Neues Ding');
    expect(readJson(path.join(pack, 'catalogue', 'filex-catalogue-en.json'))).toEqual(NEXT);
    expect(fs.readFileSync(path.join(pack, 'validate-output.txt'), 'utf8')).toContain('pack check: 0 errors');
    expect(git(pack, 'status', '--porcelain')).toBe('');
    expect(git(pack, 'log', '-1', '--format=%s')).toBe('Sync to filex abcdef12: 5 of 5');
    const body = git(pack, 'log', '-1', '--format=%B');
    expect(body).toContain('English changed, translated again: b.office');
    expect(body).toContain('Removed with filex, dropped from the translation: c.gone');
    expect(body).toContain('Co-Authored-By: Test Agent <agent@example.com>');
    // ...and now nothing is pending.
    expect(JSON.parse(cli(['status', ...packArgs, '--catalogue', target, '--json', '-']).out).pending).toBe(0);
  });
});

describe('release: the whole release-day step of a pack', () => {
  let base = '';
  let pack = '';
  let release = '';
  beforeAll(() => {
    base = tmp('filex-langpacks-release-');
    // The last apply's catalogue still had `c.gone`; the release dropped it
    // (#195: 0.53 dropped `tenants.modeOff` the same way).
    pack = makePack(base, { catalogue: { ...NEXT, 'c.gone': 'Old thing' }, rows: ROWS, translation: { ...DE, 'b.office': 'In ONLYOFFICE bearbeiten', 'd.new': 'Neues Ding' } });
    release = path.join(base, 'v0.53.0');
    writeCatalogue(release, NEXT, ROWS, '0.53.0');
  }, 60_000);

  it('refuses a catalogue of another version', SLOW, () => {
    const other = path.join(base, 'v0.52.9');
    writeCatalogue(other, NEXT, ROWS, '0.52.9');
    const r = cli(['release', '0.53.0', '--packs', pack, '--catalogue', other]);
    expect(r.code).toBe(2);
    expect(r.err).toContain('not 0.53.0');
  });

  it('--dry-run goes through every step and keeps nothing', SLOW, () => {
    const before = filesOf(pack);
    const r = cli(['release', '0.53.0', '--packs', pack, '--catalogue', release, '--dry-run']);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(r.out).toContain('0.1.0 -> 0.1.1 would be committed');
    expect(filesOf(pack)).toEqual(before);
  });

  it('moves the version, drops the key filex no longer has, rewrites the status block, validates, commits and prints the tag and push commands', SLOW, () => {
    const r = cli(['release', '0.53.0', '--packs', pack, '--catalogue', release]);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    // #195: through 0.53 the pack's validator refused the key its sync kept,
    // and release put the pack back here.
    expect(readJson(path.join(pack, 'translations', 'de.json'))).not.toHaveProperty('c.gone');
    expect(r.out).toContain('dropped 1 key(s) filex no longer has: de c.gone');
    expect(git(pack, 'log', '-1', '--format=%B').replace(/\s+/g, ' ')).toContain('1 key(s) filex 0.53.0 no longer has, dropped from the translation (c.gone)');
    expect(readJson(path.join(pack, 'filex-app.json')).version).toBe('0.1.1');
    expect(readJson(path.join(pack, 'package.json')).version).toBe('0.1.1');
    const readme = fs.readFileSync(path.join(pack, 'README.md'), 'utf8');
    expect(readme).toContain('| Version | 0.1.1, for filex 0.53.0 |');
    expect(readme).toContain('100 % - 5 of 5 strings');
    expect(readme).toContain('## Install');
    expect(readJson(path.join(pack, 'catalogue', 'filex-catalogue-context.json')).filex).toBe('0.53.0');
    expect(git(pack, 'status', '--porcelain')).toBe('');
    expect(git(pack, 'log', '-1', '--format=%s')).toBe('filex-lang-de 0.1.1: for filex 0.53.0, the catalogue from the release tag');
    expect(r.out).toContain('tag -s v0.1.1 -m "filex-lang-de 0.1.1 (for filex 0.53.0)"');
    expect(r.out).toContain('push github main');
    expect(r.out).toContain('push github v0.1.1');
    // It tags and pushes nothing itself.
    expect(git(pack, 'tag', '-l')).toBe('');
  });

  it('run again for the same release, changes nothing: the version moves once', SLOW, () => {
    const before = filesOf(pack);
    const head = git(pack, 'rev-parse', 'HEAD');
    const r = cli(['release', '0.53.0', '--packs', pack, '--catalogue', release]);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(r.out).toContain('already released for filex 0.53.0');
    expect(filesOf(pack)).toEqual(before);
    expect(git(pack, 'rev-parse', 'HEAD')).toBe(head);
  });

  it('brings the template along: the release catalogue, exactly its keys in the example, the README; no version, no tag', SLOW, () => {
    // `c.gone` is one of the example's filled keys: a key filex dropped
    // leaves the example filled or empty (before #195, a filled one stayed).
    const tpl = makePack(base, {
      name: 'filex-lang-template',
      tag: 'xx',
      catalogue: PREV,
      rows: ROWS,
      translation: { 'a.count': '', 'a.count_one': '', 'a.title': 'Files (xx)', 'b.office': '', 'c.gone': 'Old thing (xx)' },
    });
    write(path.join(tpl, 'README.md'), "Sizes: filex's catalogue is 5 keys.\n\nThe catalogue here is for **filex v0.52.0**. Every filex release attaches its own.\n");
    git(tpl, 'commit', '-q', '-am', 'the template README');
    const r = cli(['release', '0.53.0', '--packs', pack, '--template', tpl, '--catalogue', release]);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(r.out).toContain('already released for filex 0.53.0');
    expect(r.out).toContain('template: follows filex 0.53.0');
    expect(r.out).toMatch(/push github main/);
    const xx = readJson(path.join(tpl, 'translations', 'xx.json'));
    expect(Object.keys(xx).sort()).toEqual(Object.keys(NEXT).sort());
    expect(xx['a.title']).toBe('Files (xx)');
    expect(readJson(path.join(tpl, 'filex-app.json')).version).toBe('0.1.0');
    expect(readJson(path.join(tpl, 'catalogue', 'filex-catalogue-context.json')).filex).toBe('0.53.0');
    expect(fs.readFileSync(path.join(tpl, 'README.md'), 'utf8')).toContain('The catalogue here is for **filex v0.53.0**.');
    expect(git(tpl, 'log', '-1', '--format=%s')).toBe('The catalogue follows filex v0.53.0');
    expect(git(tpl, 'status', '--porcelain')).toBe('');
    expect(git(tpl, 'tag', '-l')).toBe('');
  });

  it('holds back a pack that still lacks a translation, and changes nothing in it', SLOW, () => {
    const later = path.join(base, 'v0.53.1');
    writeCatalogue(later, { ...NEXT, 'e.late': 'Landed after the nightly run' }, ROWS, '0.53.1');
    const before = filesOf(pack);
    const r = cli(['release', '0.53.1', '--packs', pack, '--catalogue', later]);
    expect(r.code).toBe(1);
    expect(r.out).toContain('held back at 0.1.1');
    expect(r.out).toContain('e.late');
    expect(filesOf(pack)).toEqual(before);
  });
});

describe('the nightly translations, home: pull, and release after them', () => {
  // The nightly translation commits in the build host's pack checkouts
  // (scripts/langpacks-nightly.mjs). A maintainer's checkout names that one
  // as its `nightly` remote: `pull` fast-forwards to it, `release` refuses
  // to go without it, and the release commit is pushed back to it.
  const DONE = { ...DE, 'b.office': 'In ONLYOFFICE bearbeiten', 'd.new': 'Neues Ding' };
  const config = (dir: string) => {
    git(dir, 'config', 'user.name', 'Nightly Test');
    git(dir, 'config', 'user.email', 'nightly@example.com');
    git(dir, 'config', 'commit.gpgsign', 'false');
    git(dir, 'config', 'core.autocrlf', 'false');
  };
  /** A maintainer's pack, and the build host's checkout of it one nightly commit ahead. */
  function pair(prefix: string, { remote = true } = {}) {
    const base = tmp(prefix);
    const pack = makePack(base, { catalogue: NEXT, rows: ROWS, translation: { ...DONE, 'a.title': 'Alte Dateien' }, remote });
    const host = path.join(base, 'host', 'filex-lang-de');
    fs.mkdirSync(path.dirname(host), { recursive: true });
    git(base, 'clone', '-q', pack, host);
    config(host);
    git(host, 'config', 'receive.denyCurrentBranch', 'updateInstead');
    write(path.join(host, 'translations', 'de.json'), json(DONE));
    git(host, 'commit', '-q', '-am', 'Sync to filex abcdef12: the nightly translation');
    git(pack, 'remote', 'add', NIGHTLY_REMOTE, host);
    const release = path.join(base, 'v0.53.0');
    writeCatalogue(release, NEXT, ROWS, '0.53.0');
    return { base, pack, host, release };
  }

  it('names the publishing remote apart from the nightly one, and what pull does with where a checkout stands', () => {
    expect(publishRemote(['nightly', 'github'])).toBe('github');
    expect(publishRemote(['nightly', 'origin'])).toBe('origin');
    expect(publishRemote(['nightly', 'gitlab'])).toBe('gitlab');
    expect(publishRemote(['nightly'])).toBe('');
    expect(pullStep({ ahead: 0, behind: 0 })).toBe('up to date');
    expect(pullStep({ ahead: 2, behind: 0 })).toBe('up to date');
    expect(pullStep({ ahead: 0, behind: 3 })).toBe('fast-forward');
    expect(pullStep({ ahead: 1, behind: 3 })).toBe('diverged');
  });

  it('release holds a pack back while its nightly remote has commits it lacks; pull brings them; release then pushes the release commit back', SLOW, () => {
    const { pack, host, release } = pair('filex-langpacks-pull-');
    const head = git(pack, 'rev-parse', 'HEAD');
    const held = cli(['release', '0.53.0', '--packs', pack, '--catalogue', release]);
    expect(held.code).toBe(1);
    expect(held.out).toContain('1 nightly commit(s) this checkout lacks');
    expect(held.out).toContain('node scripts/langpacks.mjs pull');
    expect(git(pack, 'rev-parse', 'HEAD')).toBe(head);

    const pulled = cli(['pull', '--packs', pack]);
    expect(pulled.code, `${pulled.out}${pulled.err}`).toBe(0);
    expect(pulled.out).toContain('1 nightly commit(s) from nightly/main');
    expect(git(pack, 'rev-parse', 'HEAD')).toBe(git(host, 'rev-parse', 'HEAD'));
    // A fast-forward: no merge commit.
    expect(git(pack, 'log', '-1', '--format=%P').split(' ')).toHaveLength(1);
    expect(cli(['pull', '--packs', pack]).out).toContain('up to date with nightly/main');

    const r = cli(['release', '0.53.0', '--packs', pack, '--catalogue', release]);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(r.out).toContain('push github main');
    expect(r.out).toContain(`push ${NIGHTLY_REMOTE} main`);
    // The publishing push never goes to the build host.
    expect(r.out).not.toContain(`push ${NIGHTLY_REMOTE} v0.1.1`);
    // Pushed back, the build host's checkout follows the release commit.
    git(pack, 'push', '-q', NIGHTLY_REMOTE, 'main');
    expect(git(host, 'rev-parse', 'HEAD')).toBe(git(pack, 'rev-parse', 'HEAD'));
    expect(readJson(path.join(host, 'filex-app.json')).version).toBe('0.1.1');
    expect(git(host, 'status', '--porcelain')).toBe('');
  });

  it('pull leaves a checkout that moved on its own, or has changes, for a person', SLOW, () => {
    const { pack, host } = pair('filex-langpacks-pull-diverged-');
    write(path.join(pack, 'README.md'), '# changed here\n');
    const dirty = cli(['pull', '--packs', pack]);
    expect(dirty.code).toBe(1);
    expect(dirty.out).toContain('uncommitted changes');
    git(pack, 'commit', '-q', '-am', 'a change of its own');
    const head = git(pack, 'rev-parse', 'HEAD');
    const r = cli(['pull', '--packs', pack]);
    expect(r.code).toBe(1);
    expect(r.out).toContain('both moved, merge it by hand');
    expect(git(pack, 'rev-parse', 'HEAD')).toBe(head);
    expect(git(host, 'log', '-1', '--format=%s')).toContain('the nightly translation');
  });

  it('a pack whose only remote is the nightly one is committed and pushed back, never published', SLOW, () => {
    const { pack, release } = pair('filex-langpacks-pull-local-', { remote: false });
    expect(cli(['pull', '--packs', pack]).code).toBe(0);
    const r = cli(['release', '0.53.0', '--packs', pack, '--catalogue', release]);
    expect(r.code, `${r.out}${r.err}`).toBe(0);
    expect(r.out).toContain('has no remote to publish to');
    expect(r.out).toContain(`push ${NIGHTLY_REMOTE} main`);
    expect(r.out).not.toContain('tag -s');
  });

  it('a pack with no nightly remote has nothing to pull', SLOW, () => {
    const base = tmp('filex-langpacks-pull-none-');
    const pack = makePack(base, { catalogue: NEXT, rows: ROWS, translation: DONE });
    const r = cli(['pull', '--packs', pack]);
    expect(r.code).toBe(0);
    expect(r.out).toContain('nothing to pull');
  });
});

describe("this tree's catalogue", () => {
  it('a pack that translates all of it has nothing pending, and the catalogue names the commit it was built from', SLOW, () => {
    const built = buildCatalogue(ROOT, { where: false });
    const strings = built.strings as Catalogue;
    const rows = built.context.keys as Rows;
    const base = tmp('filex-langpacks-tree-');
    const translation = Object.fromEntries(Object.entries(strings).map(([k, v]) => [k, `de ${v}`]));
    const pack = makePack(base, { catalogue: strings, rows, translation, remote: false });
    const r = cli(['status', '--packs', pack, '--json', '-']);
    expect(r.code, r.err).toBe(0);
    const report = JSON.parse(r.out);
    expect(report.pending).toBe(0);
    expect(report.filex.strings).toBe(Object.keys(strings).length);
    expect(report.filex.version).toMatch(/^\d+\.\d+\.\d+(\+[0-9a-f]{7,40})?$/);
  });

  it('is the release catalogue too: its sources hold nothing scripts/export-public.sh rewrites', () => {
    // The nightly run translates against the PRIVATE tree, release day
    // against the release asset built from the public export. The export
    // rewrites the maintainers' own domain and the GitLab address in every
    // text file, so one such word in a string would make every pack look out
    // of date on release day, for a change nobody made.
    const sources = [
      'packages/core/src/locales/en.ts',
      'packages/core/src/locales/tr.ts',
      'web/src/locales/en.json',
      'web/src/locales/tr.json',
      'backend/internal/srvtext/locales/en.json',
      'backend/internal/srvtext/locales/tr.json',
      'backend/internal/srvtext/locales/context.json',
    ];
    for (const f of sources) {
      const text = fs.readFileSync(path.join(ROOT, f), 'utf8');
      expect(text.match(/brf\.sh|gitlab\.com\/brftech/g) ?? [], f).toEqual([]);
    }
  });
});
