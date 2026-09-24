// The translator's validator (scripts/i18n-validate.mjs — copied verbatim into
// the filex-lang-template repository).
//
// ⚠⚠ Two things are pinned here:
//
//  1. Its BUILT-IN checker agrees with vue-i18n's own parser on every string
//     the admin panel ships, English and Turkish, and on the traps below. The
//     template repository runs without node_modules unless the translator
//     installs them, so the built-in checker is the one most translators
//     meet; a checker weaker than the renderer passes a pack the panel cannot
//     render. (The idea — compile every string with vue-i18n's parser — is
//     the Spanish translator's; this file keeps the fallback honest to it.)
//  2. Each rule catches the mistake it names. A validator that reports
//     nothing is indistinguishable from one that checks nothing.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';
import { loadCatalogue as loadSources } from '../../../scripts/lib/i18n-catalogue.mjs';
// A plain .mjs (the validator is copied verbatim into the template repo).
import * as V from '../../../scripts/i18n-validate.mjs';

const ROOT = path.resolve(__dirname, '../../..');
const SCRIPT = path.join(ROOT, 'scripts', 'i18n-validate.mjs');
const src = loadSources(ROOT);
const cat = V.loadCatalogue({ src: ROOT });

// vue-i18n's own parser, the one the admin panel compiles with.
const req = createRequire(path.join(ROOT, 'web', 'package.json'));
const vi18n = fs.realpathSync(req.resolve('vue-i18n/package.json'));
const coreBase = fs.realpathSync(createRequire(vi18n).resolve('@intlify/core-base/package.json'));
const mc = createRequire(createRequire(coreBase).resolve('@intlify/message-compiler/package.json'))('./dist/message-compiler.cjs');

function realParse(text: string): { branches: string[]; errors: string[] } {
  const errors: string[] = [];
  const warnings: string[] = [];
  const ast = mc.createParser({ onError: (e: Error) => errors.push(e.message), onWarn: (w: Error) => warnings.push(w.message) }).parse(text);
  const cases = ast.body.type === 1 ? ast.body.cases : [ast.body];
  const branches = cases.map((c: { items?: Array<{ type: number; key?: string; index?: number; value?: string }> }) => {
    const toks: string[] = [];
    for (const it of c.items ?? []) {
      if (it.type === 4) toks.push(`{${it.key}}`);
      else if (it.type === 5) toks.push(`{${it.index}}`);
      else if (it.type === 9) toks.push(`{'${it.value}'}`);
      else if (it.type === 6) toks.push('@linked');
    }
    return toks.sort().join(' ');
  });
  if (warnings.some((w) => /modulo/i.test(w))) errors.push('modulo');
  return { branches, errors };
}

const TRAPS = [
  'Sign in as name@example.com',
  "Sign in as name{'@'}example.com",
  "{'{'} and {'}'} and {'|'}",
  "a {'\\''} quote",
  '{count} file | {count} files',
  'none | one | {n} many',
  '%{percent} done',
  "{'%'}{percent} done",
  'unclosed {name',
  'stray } brace',
  '{not a placeholder}',
  '{0} and {1}',
  '@:common.save',
  'plain text with a | bar',
];

describe('the built-in checker agrees with vue-i18n', () => {
  const admin = [...Object.values(src.admin), ...Object.values(src.adminTr)];

  it('on every string the admin panel ships, in both languages', () => {
    expect(admin.length).toBeGreaterThan(2000);
    const disagree: string[] = [];
    for (const s of admin) {
      const a = realParse(s);
      const b = V.parseVueI18nFallback(s);
      if (JSON.stringify(a.branches) !== JSON.stringify(b.branches) || (a.errors.length > 0) !== (b.errors.length > 0)) {
        disagree.push(`${JSON.stringify(s)}\n  vue-i18n: ${JSON.stringify(a)}\n  built-in: ${JSON.stringify(b)}`);
      }
    }
    expect(disagree.slice(0, 5)).toEqual([]);
  });

  it.each(TRAPS)('on the trap %j', (s) => {
    const a = realParse(s);
    const b = V.parseVueI18nFallback(s);
    expect(b.errors.length > 0).toBe(a.errors.length > 0);
    if (!a.errors.length) expect(b.branches).toEqual(a.branches);
  });
});

describe('each rule catches what it names', () => {
  const check = (pack: Record<string, string>, lang = 'es') => V.checkLanguage(lang, pack, cat);
  const codes = (r: { errors: Array<{ key: string; code: string }> }) => r.errors.map((e) => `${e.code}:${e.key}`);
  const adminKey = 'appPlugins.title';
  const explorerKey = 'ctx.download';
  const sharedKey = Object.keys(src.explorer).find((k) => k in src.admin)!;
  const pluralKey = Object.keys(src.admin).find((k) => src.admin[k].split('|').length === 2 && /\{count\}/.test(src.admin[k]))!;

  it('a bare @ in an admin-panel string', () => {
    expect(codes(check({ [adminKey]: 'Apps @ work' }))).toContain(`SYNTAX:${adminKey}`);
    expect(codes(check({ [adminKey]: "Apps {'@'} work" }))).toEqual([]);
  });

  it("{'@'} in an explorer string (printed as written there)", () => {
    expect(codes(check({ [explorerKey]: "Descargar {'@'}" }))).toContain(`LITERAL:${explorerKey}`);
    expect(codes(check({ [explorerKey]: 'Descargar @ ya' }))).toEqual([]);
  });

  it('@, | or a literal in a key BOTH catalogues draw', () => {
    expect(codes(check({ [sharedKey]: 'algo | otro' }))).toContain(`SHARED:${sharedKey}`);
    expect(codes(check({ [sharedKey]: "algo {'@'} otro" }))).toContain(`LITERAL:${sharedKey}`);
  });

  it("vue-i18n's modulo form, which eats the % (the Turkish catalogue had two)", () => {
    const key = 'pendingOps.progress';
    expect(codes(check({ [key]: '{done} / {total} · %{percent}' }))).toContain(`SYNTAX:${key}`);
    expect(codes(check({ [key]: "{done} / {total} · {'%'}{percent}" }))).toEqual([]);
  });

  it('a placeholder the English does not have', () => {
    expect(codes(check({ [explorerKey]: 'Descargar {nombre}' }))).toContain(`PLACEHOLDER:${explorerKey}`);
    expect(codes(check({ [adminKey]: 'Aplicaciones {nombre}' }))).toContain(`PLACEHOLDER:${adminKey}`);
  });

  it('a | in a string that is not a plural, and more forms than the rule can pick', () => {
    expect(codes(check({ [adminKey]: 'Aplicaciones | Apps' }))).toContain(`PLURAL:${adminKey}`);
    expect(codes(check({ [pluralKey]: 'a | b | {count} c | {count} d' }))).toContain(`PLURAL:${pluralKey}`);
    // One form (no inflection), or zero | one | other, is rendered correctly.
    expect(codes(check({ [pluralKey]: '{count} cosa' }))).toEqual([]);
    expect(codes(check({ [pluralKey]: 'ninguna | una | {count} cosas' }))).toEqual([]);
  });

  it('a key the server would refuse, and one it does not know', () => {
    expect(codes(check({ '__proto__.x': 'y' }))).toContain('KEY:__proto__.x');
    expect(codes(check({ 'a b': 'y' }))).toContain('KEY:a b');
    expect(check({ 'no.such.key': 'y' }).warnings.map((w: { code: string }) => w.code)).toContain('UNKNOWN');
  });

  it('a string over the per-string ceiling', () => {
    expect(codes(check({ [explorerKey]: 'x'.repeat(V.LIMITS.MaxUILocaleValueBytes + 1) }))).toContain(`SIZE:${explorerKey}`);
  });

  it('coverage is the CURRENT catalogue, floor-rounded, and an RTL language says so', () => {
    const r = check({ [explorerKey]: 'تحميل' }, 'ar');
    expect(r.coverage.translated).toBe(1);
    expect(r.coverage.percent).toBe(0);
    expect(r.coverage.rtl).toBe(true);
  });

  it('passes the whole Turkish catalogue — a validator that flags our own translation is wrong', () => {
    const tr: Record<string, string> = { ...src.adminTr, ...src.serverTr };
    for (const [k, v] of Object.entries(src.explorerTr)) if (!(k in tr)) tr[k] = v;
    const r = check(tr, 'tr');
    expect(r.errors).toEqual([]);
    // Turkish writes no singular forms of the server's sentences (it does not
    // inflect after a number): those English `_one` keys are all it lacks.
    expect(r.coverage.missing.filter((k: string) => !/_one$/.test(k))).toEqual([]);
  });
});

// The server's text: e-mails, public pages, notifications. Its grammar is
// `{name}` and nothing else, and a placeholder is not optional there — a mail
// line that lost {pin} is delivered without the PIN (the server refuses such
// a translation and sends the English line).
describe('the server table', () => {
  const check = (pack: Record<string, string>, lang = 'es') => V.checkLanguage(lang, pack, cat);
  const codes = (r: { errors: Array<{ key: string; code: string }> }) => r.errors.map((e) => `${e.code}:${e.key}`);
  const warns = (r: { warnings: Array<{ key: string; code: string }> }) => r.warnings.map((e) => `${e.code}:${e.key}`);

  it('is in the catalogue the validator reads', () => {
    expect(cat.table['server.mail.label.pin']).toBe('server');
    expect(cat.table['server.notify.file.uploaded.title']).toBe('server');
    expect(cat.plural['server.mail.valid_days']).toBe(true);
  });

  it('refuses a translation that loses a placeholder — as an error, not a warning', () => {
    expect(codes(check({ 'server.mail.label.pin': 'PIN: pregunte al remitente' }))).toContain('PLACEHOLDER:server.mail.label.pin');
    expect(codes(check({ 'server.mail.label.pin': 'PIN (código): {pin}' }))).toEqual([]);
    expect(codes(check({ 'server.mail.label.file': 'Archivo: {name} {size}' }))).toContain('PLACEHOLDER:server.mail.label.file');
  });

  it('knows only {name}: a Go template or a printf verb prints as written', () => {
    expect(codes(check({ 'server.mail.label.file': 'Archivo: {{.Name}} {name}' }))).toContain('SYNTAX:server.mail.label.file');
    expect(codes(check({ 'server.mail.label.file': 'Archivo: %s {name}' }))).toContain('SYNTAX:server.mail.label.file');
    expect(codes(check({ 'server.mail.label.file': "Archivo{'@'} {name}" }))).toContain('LITERAL:server.mail.label.file');
    // @ and | are ordinary characters here.
    expect(codes(check({ 'server.mail.label.file': 'Archivo @ | {name}' }))).toEqual([]);
  });

  it('keeps a subject on one line', () => {
    expect(codes(check({ 'server.mail.grant.subject': 'Compartido\nBcc: x@y' }))).toContain('SUBJECT:server.mail.grant.subject');
  });

  it('checks the notification phrases the same way', () => {
    expect(codes(check({ 'server.notify.file.uploaded.title': 'Archivo nuevo: {name}' }))).toEqual([]);
    expect(codes(check({ 'server.notify.file.uploaded.title': 'Archivo nuevo' }))).toContain('PLACEHOLDER:server.notify.file.uploaded.title');
  });
});

// Plural forms by CLDR category (Intl.PluralRules) — the Arabic translator's
// request: six forms where the language has six, and a singular that may say
// the number as a word.
describe('plural forms', () => {
  const check = (pack: Record<string, string>, lang: string) => V.checkLanguage(lang, pack, cat);
  const codes = (r: { errors: Array<{ key: string; code: string }> }) => r.errors.map((e) => `${e.code}:${e.key}`);
  const warns = (r: { warnings: Array<{ key: string; code: string }> }) => r.warnings.map((e) => `${e.code}:${e.key}`);
  const explorerPlural = Object.keys(src.explorer).find((k) => `${k}_one` in src.explorer && /\{n\}/.test(src.explorer[k]))!;
  const adminPlural = Object.keys(src.admin).find((k) => src.admin[k].split('|').length === 2 && /\{count\}/.test(src.admin[k]))!;

  it('categories are the integers a language counts with', () => {
    expect(V.pluralCategories('ar')).toEqual(['zero', 'one', 'two', 'few', 'many', 'other']);
    expect(V.pluralCategories('ru')).toEqual(['one', 'few', 'many', 'other']);
    expect(V.pluralCategories('es')).toEqual(['one', 'other']);
    expect(V.pluralCategories('ja')).toEqual(['other']);
  });

  it('an explorer or server key takes a form per category — known, not UNKNOWN', () => {
    const r = check({ [`${explorerPlural}_few`]: '{n} ملفات', [`${explorerPlural}_two`]: 'ملفان', 'server.mail.valid_days_few': 'صالح {count} أيام' }, 'ar');
    expect(warns(r).filter((w: string) => w.startsWith('UNKNOWN'))).toEqual([]);
    expect(r.errors).toEqual([]);
  });

  it('a form for a category the language does not have is never shown', () => {
    const r = check({ [`${explorerPlural}_few`]: 'x {n}' }, 'es');
    expect(warns(r)).toContain(`UNUSED:${explorerPlural}_few`);
    // English's `_one` in a language with no `one` category is not required either.
    const ja = check({}, 'ja');
    expect(ja.coverage.missing).not.toContain(`${explorerPlural}_one`);
  });

  it('the count may be a word where the category IS one number — not where it is many', () => {
    // Arabic `one` is exactly 1: "يوم واحد" needs no digit (it was a warning).
    expect(warns(check({ [`${explorerPlural}_one`]: 'ملف واحد' }, 'ar'))).not.toContain(`PLACEHOLDER:${explorerPlural}_one`);
    expect(codes(check({ 'server.mail.valid_days_one': 'صالح ليوم واحد.' }, 'ar'))).toEqual([]);
    // Russian `one` is also 21, 31, 101…: without the number it would lie.
    expect(warns(check({ [`${explorerPlural}_one`]: 'один файл' }, 'ru'))).toContain(`PLACEHOLDER:${explorerPlural}_one`);
    expect(codes(check({ 'server.mail.valid_days_one': 'Действует один день.' }, 'ru'))).toContain('PLACEHOLDER:server.mail.valid_days_one');
  });

  it("the admin panel takes one form per category of the language, in CLDR order — or the classic 1, 2, 3", () => {
    const six = 'لا شيء | واحد | اثنان | {count} قليل | {count} كثير | {count} آخر';
    expect(codes(check({ [adminPlural]: six }, 'ar'))).toEqual([]);
    expect(codes(check({ [adminPlural]: 'a | b | {count} c | {count} d' }, 'ar'))).toContain(`PLURAL:${adminPlural}`);
    expect(codes(check({ [adminPlural]: 'a | b | {count} c | {count} d' }, 'ru'))).toEqual([]);
    expect(codes(check({ [adminPlural]: six }, 'es'))).toContain(`PLURAL:${adminPlural}`);
    expect(codes(check({ [adminPlural]: 'ninguna | una | {count} cosas' }, 'es'))).toEqual([]);
  });

  it('says which plural keys still lack a form for one of the categories', () => {
    const r = check({ [explorerPlural]: '{n} ملف' }, 'ar');
    const gap = r.coverage.plural.gaps.find((g: { key: string }) => g.key === explorerPlural);
    expect(gap?.lacks).toContain(`${explorerPlural}_few`);
  });
});

describe('the command', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-i18n-'));
  const run = (...args: string[]) => spawnSync(process.execPath, [SCRIPT, ...args], { encoding: 'utf8' });

  it('exits 0 on a good pack and 1 on a bad one, reading a filex checkout', () => {
    fs.writeFileSync(path.join(dir, 'es.json'), JSON.stringify({ 'ctx.download': 'Descargar' }));
    fs.writeFileSync(path.join(dir, 'xx.json'), JSON.stringify({ 'appPlugins.title': 'a @ b' }));
    const good = run(path.join(dir, 'es.json'), '--src', ROOT);
    expect(good.status, good.stdout + good.stderr).toBe(0);
    expect(good.stdout).toMatch(/\[es\] 0% translated — 1 of \d+ strings/);
    const bad = run(path.join(dir, 'xx.json'), '--src', ROOT);
    expect(bad.status).toBe(1);
    expect(bad.stdout).toContain('SYNTAX');
  });

  it('checks a filex-app.json as a language pack — a module or a permission makes it not one', () => {
    const m = {
      manifest_version: 1, name: 'lang-es', version: '1.0.0', label: { en: 'Spanish' },
      permissions: ['state'], ui_locales: { es: { 'ctx.download': 'Descargar' } },
    };
    fs.writeFileSync(path.join(dir, 'filex-app.json'), JSON.stringify(m));
    const r = run(path.join(dir, 'filex-app.json'), '--src', ROOT);
    expect(r.status).toBe(1);
    expect(r.stdout).toContain('NOT_A_PACK');
  });
});
