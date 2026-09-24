#!/usr/bin/env node
/**
 * i18n-validate — check a filex language pack against the catalogue.
 *
 *   node i18n-validate.mjs <pack> [options]
 *
 *   <pack>            translations/<lang>.json — ONE flat object
 *                     { "<key>": "<text>" } (the shape of `ui_locales[<lang>]`)
 *                     — or a filex-app.json, whose every `ui_locales` language
 *                     is checked, and whose manifest is checked as a language
 *                     pack (no module, nothing that runs).
 *   --lang <tag>      the language of a flat file (default: its file name)
 *   --catalogue <dir> a directory holding filex-catalogue-en.json and
 *                     filex-catalogue-context.json (a release asset, or what
 *                     `node scripts/i18n-export.mjs` writes)
 *   --src <dir>       …or a filex checkout, read directly
 *   --complete        a missing key is an ERROR (default: it is coverage)
 *   --missing         list the keys still to translate
 *   --plurals         list the plural keys that lack a form for one of the
 *                     language's categories
 *   --no-compiler     skip vue-i18n's own parser even when it is installed
 *   --json            the report as JSON on stdout
 *
 * Without --catalogue/--src it looks for ../catalogue/ (the layout of the
 * filex-lang-template repository) and then for a filex checkout around it.
 *
 * Exit: 0 — no errors (warnings allowed); 1 — errors; 2 — could not run.
 *
 * ⚠⚠ THIS FILE IS COPIED VERBATIM into BRF-Tech/filex-lang-template
 * (scripts/validate.mjs). It imports nothing but Node's standard library, and
 * `@intlify/message-compiler` — vue-i18n's OWN parser, the one the admin panel
 * compiles every string with — only when it is installed (the template's
 * package.json pins it; a filex checkout has it under web/). Without it a
 * built-in checker covers the same rules; web/tests/i18n/langPackValidator
 * .test.ts runs both over every real string and fails when they disagree.
 *
 * The rules — and why a key's TABLE decides its grammar — are in
 * docs/PLUGIN-KIT.md → "Writing a language pack".
 */
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

/* ── the host's limits (backend/pkg/pluginkit/wire/langpack.go) ────────
 * ⚠ Mirrors of the Go constants; web/tests/i18n/langPackContract.test.ts
 * reads both files and fails when they disagree. */
export const LIMITS = {
  MaxUILocaleBytes: 1048576,
  MaxUILocalesBytes: 4194304,
  MaxUILocaleKeyBytes: 128,
  MaxUILocaleValueBytes: 4096,
};

/** Primary language subtags written right to left (wire.IsRTL). */
export const RTL_LANGUAGES = ['ar', 'arc', 'ckb', 'dv', 'fa', 'he', 'iw', 'khw', 'ks', 'nqo', 'pnb', 'prs', 'ps', 'sd', 'syr', 'ug', 'ur', 'yi'];
/** Script subtags written right to left. */
export const RTL_SCRIPTS = ['adlm', 'arab', 'hebr', 'nkoo', 'rohg', 'syrc', 'thaa'];

export function isRTL(tag) {
  const parts = String(tag).trim().toLowerCase().split('-');
  for (const p of parts.slice(1)) if (p.length === 4) return RTL_SCRIPTS.includes(p);
  return RTL_LANGUAGES.includes(parts[0]);
}

const TAG = /^[a-z]{2,3}(-[a-z0-9]{2,8})*$/;
const KEY_SEG = /^[A-Za-z0-9_-]+$/;

export function keyOK(k) {
  if (!k || Buffer.byteLength(k) > LIMITS.MaxUILocaleKeyBytes) return false;
  return k.split('.').every((s) => KEY_SEG.test(s) && s !== '__proto__' && s !== 'constructor' && s !== 'prototype');
}

/* ── the explorer's and the server's grammar: `{name}` replaced verbatim ── */

export function plainTokens(text) {
  return [...String(text).matchAll(/\{([A-Za-z0-9_]+)\}/g)].map((m) => m[1]).sort();
}

const uniq = (xs) => [...new Set(xs)].sort();

/* ── plurals: CLDR categories ─────────────────────────────────────────────
 *
 * Every table picks a plural form by the CLDR category Intl.PluralRules
 * gives the count in the reader's language — zero, one, two, few, many,
 * other. The explorer and the server table take a form as its own key,
 * `<key>_<category>` (the plain key is `other`); the admin panel takes `|`
 * forms, one per category in CLDR order. docs/PLUGIN-KIT.md → "Plural forms".
 */

export const CLDR_ORDER = ['zero', 'one', 'two', 'few', 'many', 'other'];
/** Explorer variables that carry the count (packages/core useLocale). */
export const COUNT_VARS = ['count', 'n', 'days'];

function rules(lang) {
  try {
    return new Intl.PluralRules(lang);
  } catch {
    return new Intl.PluralRules('en');
  }
}

/**
 * The categories a language's counts fall into: those Intl.PluralRules
 * selects for some integer 0…999, and `other` always. ⚠ Not
 * resolvedOptions().pluralCategories as it stands — modern CLDR lists `many`
 * for es, fr, it, pt, ca for exact millions only; counting it would ask every
 * Spanish plural for a third form and read a classic 3-form admin string in
 * the wrong order. The explorer, the admin panel and the server use this
 * definition too.
 */
export function pluralCategories(lang) {
  const pr = rules(lang);
  const seen = new Set(['other']);
  for (let n = 0; n < 1000; n += 1) seen.add(pr.select(n));
  return CLDR_ORDER.filter((c) => seen.has(c));
}

/**
 * Does `cat` hold exactly one integer in `lang`? Then its form may leave the
 * count out — the word IS the number ("1 day", Arabic "يوم واحد"). Russian
 * `one` is also 21, 31, 101…: there the count must stay.
 */
export function impliesNumber(lang, cat) {
  const pr = rules(lang);
  let hits = 0;
  for (let n = 0; n < 1000 && hits < 2; n += 1) if (pr.select(n) === cat) hits += 1;
  return hits === 1;
}

/** `x_few` → { base: 'x', cat: 'few' } for the five form suffixes. */
export function formOf(key) {
  const m = String(key).match(/^(.+)_(zero|one|two|few|many)$/);
  return m ? { base: m[1], cat: m[2] } : null;
}

/* ── the admin panel's grammar: vue-i18n ─────────────────────────────────
 *
 * A message is plural BRANCHES separated by `|`; each branch is text with
 * `{name}` (named), `{0}` (list), `{'…'}` (literal: the way to write @ { } |)
 * and `@:key` / `@.mod:key` (linked). A bare `@` starts a link, so an email
 * address or a handle in a translation breaks the string; `%` right before
 * `{` is the deprecated modulo form and EATS the percent sign
 * (`%{percent}` renders "97", not "%97" — measured on vue-i18n 9.14).
 */

/** Built-in checker — what vue-i18n's parser decides, for the syntax filex uses. */
export function parseVueI18nFallback(text) {
  const errors = [];
  const branches = [[]];
  const s = String(text);
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (c === '{') {
      if (i > 0 && s[i - 1] === '%') errors.push(`%{…} is vue-i18n's modulo form and swallows the % — write {'%'}{…}`);
      let j = i + 1;
      while (s[j] === ' ') j += 1;
      if (s[j] === "'") {
        // {'literal'} — scanned by hand, because the literal may itself hold
        // a } or a | (`{'}'}` is how a brace is written).
        let k = j + 1;
        let lit = '';
        while (k < s.length && s[k] !== "'" && s[k] !== '\n') {
          if (s[k] === '\\') {
            const n = s[k + 1] ?? '';
            if (n === 'u' && /^[0-9a-fA-F]{4}$/.test(s.slice(k + 2, k + 6))) {
              lit += String.fromCharCode(parseInt(s.slice(k + 2, k + 6), 16));
              k += 6;
            } else if (n === 'U' && /^[0-9a-fA-F]{6}$/.test(s.slice(k + 2, k + 8))) {
              lit += String.fromCodePoint(parseInt(s.slice(k + 2, k + 8), 16));
              k += 8;
            } else {
              lit += n;
              k += 2;
            }
            continue;
          }
          lit += s[k];
          k += 1;
        }
        if (s[k] !== "'") {
          errors.push("an unterminated {'literal'}");
          break;
        }
        k += 1;
        while (s[k] === ' ') k += 1;
        if (s[k] !== '}') {
          errors.push("a {'literal'} must close with }");
          break;
        }
        branches[branches.length - 1].push(`{'${lit}'}`);
        i = k + 1;
        continue;
      }
      const end = s.indexOf('}', j);
      if (end < 0) {
        errors.push("an opening { with no closing }");
        break;
      }
      const inner = s.slice(j, end).trim();
      if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(inner) || /^\d+$/.test(inner)) branches[branches.length - 1].push(`{${inner}}`);
      else errors.push(`{${inner}} is neither a placeholder nor a {'literal'}`);
      i = end + 1;
      continue;
    }
    if (c === '}') {
      errors.push("a closing } with no opening {");
      i += 1;
      continue;
    }
    if (c === '@') {
      if (/^@(\.[a-z]+)?:/.test(s.slice(i))) branches[branches.length - 1].push('@linked');
      else errors.push("a bare @ starts a linked message — write {'@'}");
      i += 1;
      continue;
    }
    if (c === '|') {
      branches.push([]);
      i += 1;
      continue;
    }
    i += 1;
  }
  return { branches: branches.map((b) => b.sort().join(' ')), errors };
}

let compiler = null;
let compilerNote = 'built-in checker (vue-i18n parser not installed)';

/** vue-i18n's own parser, when reachable — the translator's validator did this first. */
export function useCompiler(searchFrom = []) {
  const tries = [];
  for (const base of searchFrom) {
    tries.push(() => {
      // A filex checkout: web → vue-i18n → @intlify/core-base → message-compiler.
      const webReq = createRequire(path.join(base, 'web', 'package.json'));
      const vi18n = fs.realpathSync(webReq.resolve('vue-i18n/package.json'));
      const coreBase = fs.realpathSync(createRequire(vi18n).resolve('@intlify/core-base/package.json'));
      return createRequire(coreBase).resolve('@intlify/message-compiler/package.json');
    });
  }
  // Installed next to this script (the template's package.json pins it).
  tries.push(() => createRequire(import.meta.url).resolve('@intlify/message-compiler/package.json'));
  for (const t of tries) {
    try {
      const pkg = t();
      const mc = createRequire(pkg)('./dist/message-compiler.cjs');
      const version = JSON.parse(fs.readFileSync(pkg, 'utf8')).version;
      compiler = (text) => {
        const errors = [];
        const warnings = [];
        const p = mc.createParser({ onError: (e) => errors.push(e.message), onWarn: (w) => warnings.push(w.message) });
        const ast = p.parse(String(text));
        const cases = ast.body.type === 1 ? ast.body.cases : [ast.body];
        const branches = cases.map((c) => {
          const toks = [];
          for (const it of c.items ?? []) {
            if (it.type === 4) toks.push(`{${it.key}}`);
            else if (it.type === 5) toks.push(`{${it.index}}`);
            else if (it.type === 9) toks.push(`{'${it.value}'}`);
            else if (it.type === 6) toks.push('@linked');
          }
          return toks.sort().join(' ');
        });
        // Modulo: the parser only WARNS, and the % is gone from the screen.
        for (const w of warnings) if (/modulo/i.test(w)) errors.push(`%{…} is vue-i18n's modulo form and swallows the % — write {'%'}{…}`);
        if (/%\{/.test(String(text)) && !errors.some((e) => /modulo/.test(e))) {
          errors.push(`%{…} is vue-i18n's modulo form and swallows the % — write {'%'}{…}`);
        }
        return { branches, errors };
      };
      compilerNote = `@intlify/message-compiler ${version} (vue-i18n's own parser)`;
      return true;
    } catch {
      /* next */
    }
  }
  return false;
}

export function parseVueI18n(text) {
  return compiler ? compiler(text) : parseVueI18nFallback(text);
}

export function compilerInUse() {
  return compilerNote;
}

/* ── the catalogue ─────────────────────────────────────────────────────── */

function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object') flatten(v, key, out);
    else out[key] = v;
  }
  return out;
}

function loadCoreTable(file) {
  const src = fs.readFileSync(file, 'utf8');
  const m = src.match(/export\s+const\s+\w+\s*:\s*Record<string,\s*string>\s*=\s*/);
  if (!m) throw new Error(`${file}: no object literal`);
  return vm.runInNewContext(`(${src.slice(m.index + m[0].length).replace(/;\s*$/, '')})`, Object.create(null), { timeout: 2000 });
}

function tsObjectLiteral(src, name, file) {
  const m = src.match(new RegExp(`const\\s+${name}\\b[^=]*=\\s*`));
  if (!m) throw new Error(`${file}: no ${name}`);
  const start = m.index + m[0].length;
  const end = src.indexOf('\n};', start);
  return vm.runInNewContext(`(${src.slice(start, end + 2)})`, Object.create(null), { timeout: 2000 });
}

/** The server table of a checkout: the server's catalogue and the notification phrases. */
function loadServerTable(src) {
  const out = JSON.parse(fs.readFileSync(path.join(src, 'backend', 'internal', 'srvtext', 'locales', 'en.json'), 'utf8'));
  const file = path.join(src, 'web', 'src', 'lib', 'notificationText.ts');
  const ts = fs.readFileSync(file, 'utf8');
  /* ⚠ MUST match scripts/lib/i18n-catalogue.mjs (loadNotifyTables): a value
     that is one bare `{placeholder}` is not exported — there is no word in it
     to translate — and the four fallback WORDS now live in the server's own
     en.json, beside the mail that shares two of them. This file is copied
     verbatim into filex-lang-template and cannot import the other one, so the
     two say the same thing twice on purpose; when one changes, change both or
     the validator reports keys the catalogue never shipped. */
  const bare = /^\s*\{[A-Za-z0-9_]+\}\s*$/;
  const say = (key, value) => {
    if (typeof value === 'string' && value && !bare.test(value)) out[key] = value;
  };
  for (const [event, byLang] of Object.entries(tsObjectLiteral(ts, 'NOTIFICATION_PHRASES', file))) {
    const p = byLang.en;
    say(`server.notify.${event}.title`, p.title);
    say(`server.notify.${event}.body`, p.body);
    for (const [f, v] of Object.entries(p.one ?? {})) say(`server.notify.${event}.${f}_one`, v);
  }
  return out;
}

/** Is `key` of `table` a sentence about a count? (i18n-catalogue.mjs isPluralKey) */
function pluralOf(strings, table, key) {
  const t = table[key];
  if (t === 'admin' || t === 'both') return /\|/.test(String(strings[key]).replace(/\{'[^']*'\}/g, ''));
  if (formOf(key) && formOf(key).base in strings) return false;
  if (`${key}_one` in strings) return true;
  const toks = plainTokens(strings[key]);
  return t === 'server' ? toks.includes('count') : COUNT_VARS.some((v) => toks.includes(v));
}

/**
 * `{ strings: {key: English}, table: {key: 'explorer'|'admin'|'both'|'server'},
 * plural: {key: true}, source }` from an exported catalogue directory or a
 * filex checkout.
 */
export function loadCatalogue({ catalogue, src }) {
  if (catalogue) {
    const strings = JSON.parse(fs.readFileSync(path.join(catalogue, 'filex-catalogue-en.json'), 'utf8'));
    const ctx = JSON.parse(fs.readFileSync(path.join(catalogue, 'filex-catalogue-context.json'), 'utf8'));
    const table = {};
    const plural = {};
    for (const k of Object.keys(strings)) {
      table[k] = ctx.keys?.[k]?.in || (k.startsWith('server.') ? 'server' : 'explorer');
    }
    for (const k of Object.keys(strings)) {
      // An older catalogue has no `plural` flags: decide from the strings.
      plural[k] = ctx.keys?.[k]?.plural ?? pluralOf(strings, table, k);
    }
    return { strings, table, plural, source: `${catalogue} (filex ${ctx.filex || '?'})` };
  }
  const explorer = loadCoreTable(path.join(src, 'packages', 'core', 'src', 'locales', 'en.ts'));
  const admin = flatten(JSON.parse(fs.readFileSync(path.join(src, 'web', 'src', 'locales', 'en.json'), 'utf8')));
  const server = loadServerTable(src);
  const strings = {};
  const table = {};
  for (const [k, v] of Object.entries(admin)) {
    strings[k] = v;
    table[k] = 'admin';
  }
  for (const [k, v] of Object.entries(explorer)) {
    table[k] = k in admin ? 'both' : 'explorer';
    if (!(k in admin)) strings[k] = v;
  }
  for (const [k, v] of Object.entries(server)) {
    strings[k] = v;
    table[k] = 'server';
  }
  const plural = {};
  for (const k of Object.keys(strings)) plural[k] = pluralOf(strings, table, k);
  return { strings, table, plural, source: `${src} (source checkout)` };
}

/* ── the checks ────────────────────────────────────────────────────────── */

const codeSpans = (s) => (String(s).match(/`[^`]*`/g) ?? []).sort().join('\u0000');

/**
 * Check one language. Returns `{ errors, warnings, coverage }`, each problem
 * `{ key, code, msg }`.
 */
export function checkLanguage(lang, pack, cat, { complete = false } = {}) {
  const errors = [];
  const warnings = [];
  const err = (key, code, msg) => errors.push({ key, code, msg });
  const warn = (key, code, msg) => warnings.push({ key, code, msg });

  if (!TAG.test(lang)) err('(language)', 'TAG', `${JSON.stringify(lang)} is not a language tag (es, pt-br, zh-hant…)`);
  if (!pack || typeof pack !== 'object' || Array.isArray(pack)) {
    err('(pack)', 'SHAPE', 'a language is ONE flat object { "<key>": "<text>" }');
    return { errors, warnings, coverage: null };
  }
  const cats = pluralCategories(lang);
  const k = cats.length;
  // A catalogue key that is a FORM for a category this language does not
  // have (English's `_one` in Japanese) is never shown, so it is neither
  // required nor coverage.
  const unusedForm = (key) => {
    const f = formOf(key);
    return !!f && cat.plural[f.base] && (cat.table[f.base] === 'explorer' || cat.table[f.base] === 'server') && !cats.includes(f.cat);
  };

  let bytes = 0;
  let translated = 0;
  let identical = 0;
  const missing = [];
  for (const [key, value] of Object.entries(pack)) {
    bytes += Buffer.byteLength(key) + (typeof value === 'string' ? Buffer.byteLength(value) : 0);
    if (!keyOK(key)) {
      err(key, 'KEY', 'not a filex key (dotted segments of letters, digits, _ and -, at most 128 bytes) — the server refuses the pack');
      continue;
    }
    if (typeof value !== 'string') {
      err(key, 'TYPE', 'the value must be a string');
      continue;
    }
    if (Buffer.byteLength(value) > LIMITS.MaxUILocaleValueBytes) {
      err(key, 'SIZE', `${Buffer.byteLength(value)} bytes, the server takes at most ${LIMITS.MaxUILocaleValueBytes}`);
    }
    // What the key IS: a catalogue key, or a plural form of one — `<key>_few`
    // for a key that counts, in the explorer or the server table (the admin
    // panel writes its forms inside one string, with |).
    const f = formOf(key);
    const formBase = f && f.base in cat.strings && cat.plural[f.base] && (cat.table[f.base] === 'explorer' || cat.table[f.base] === 'server') ? f.base : '';
    const inCatalogue = key in cat.strings;
    if (!inCatalogue && !formBase) {
      warn(key, 'UNKNOWN', 'not in this catalogue — a typo, or a string filex no longer has; it is ignored');
      continue;
    }
    // An EMPTY value is simply "not translated yet" — the host skips it and
    // shows the English — so it counts toward what is missing, not as a
    // problem: a translation file may start with every key empty.
    if (!value.trim()) continue;
    if (formBase && !cats.includes(f.cat)) {
      warn(key, 'UNUSED', `${lang} has no "${f.cat}" plural category (its categories: ${cats.join(', ')}) — this form is never shown`);
      continue;
    }
    if (inCatalogue) translated += 1;
    const table = cat.table[formBase || key];
    const en = inCatalogue ? cat.strings[key] : cat.strings[formBase];
    if (inCatalogue && value === en) identical += 1;

    if (table === 'admin' || table === 'both') {
      const a = parseVueI18n(en);
      const b = parseVueI18n(value);
      // Tokens that are VALUES (named / list / linked), literals aside: every
      // form of a plural is handed the same values, so a form may use any of
      // them — "one file | {count} files" translates to "{count} archivo |
      // {count} archivos" legitimately.
      const vals = (branches) => new Set(branches.flatMap((x) => x.split(' ')).filter((x) => x && !/^\{'/.test(x)));
      const want = vals(a.branches);
      const got = vals(b.branches);
      const n = b.branches.length;
      if (b.errors.length && !a.errors.length) {
        err(key, 'SYNTAX', `the admin panel (vue-i18n) cannot render it: ${[...new Set(b.errors)].join('; ')}`);
      } else if (a.branches.length === 1 && n > 1) {
        // Not a plural in English, so the call passes no count: vue-i18n
        // would show only the text before the first |.
        err(key, 'PLURAL', "the English is not a plural, and a bare | splits it (only the first part would show) — write {'|'} for a literal bar");
      } else if (n > 1 && ![2, 3, k].includes(n)) {
        err(key, 'PLURAL', `${n} forms — write ${k} (${lang}'s categories in this order: ${cats.join(' | ')}), or 2 (one | other) or 3 (zero | one | other)`);
      } else {
        // 1 form (a language that does not inflect), 2 (one | other), 3
        // (zero | one | other), or one per category of the language.
        const bad = [...got].filter((x) => !want.has(x));
        if (bad.length) err(key, 'PLACEHOLDER', `uses ${bad.join(' ')}, which the English does not — it would print wrong`);
        const gone = [...want].filter((x) => !got.has(x));
        if (gone.length) warn(key, 'PLACEHOLDER', `leaves out ${gone.join(' ')} — that value will not appear`);
      }
    }
    if (table === 'explorer' || table === 'both' || table === 'server') {
      // A form is checked against its plain key's English (and the English
      // form when there is one): every value of the sentence is handed to it.
      // The count may be left out where the category holds a single number
      // in this language — "1 day", Arabic "يوم واحد" — and must stay where
      // it does not (Russian `one` is also 21, 31…).
      const base = formBase || (f && f.base in cat.strings && cat.plural[f.base] ? f.base : '');
      const cat1 = base ? f.cat : '';
      const enBase = base ? cat.strings[base] : en;
      const allowed = uniq([...plainTokens(enBase), ...plainTokens(inCatalogue ? cat.strings[key] : '')]);
      const countVars = table === 'server' ? ['count'] : COUNT_VARS;
      const implied = cat1 ? impliesNumber(lang, cat1) : false;
      const required = cat1
        ? uniq(plainTokens(enBase)).filter((x) => !(implied && countVars.includes(x)))
        : uniq(plainTokens(en));
      const got = uniq(plainTokens(value));
      const bad = got.filter((x) => !allowed.includes(x));
      const gone = required.filter((x) => !got.includes(x));
      const why = cat1 && gone.some((x) => countVars.includes(x)) ? ` (in ${lang} the "${cat1}" form is used for more than one number)` : '';
      if (table === 'server') {
        // ⚠ Errors both ways: a mail line that lost {pin} or {url} would be
        // delivered without it — the server refuses such a translation at run
        // time and sends the English line instead (internal/srvtext).
        if (bad.length) err(key, 'PLACEHOLDER', `uses {${bad.join('} {')}}, which the English does not — it would print as written`);
        if (gone.length) err(key, 'PLACEHOLDER', `leaves out {${gone.join('} {')}}${why} — the server refuses this translation and sends the English`);
        if (/\{\{|\}\}|%[sdvqf]\b/.test(value)) err(key, 'SYNTAX', 'the server text knows only {name} placeholders — {{…}} and %s print as written');
        if (/\{\s*'/.test(value)) err(key, 'LITERAL', "{'…'} prints as written in server text — write the character itself");
        if (/\.subject(_[a-z]+)?$/.test(key) && /[\r\n]/.test(value)) err(key, 'SUBJECT', 'an email subject is one line');
      } else {
        if (bad.length) err(key, 'PLACEHOLDER', `uses {${bad.join('} {')}}, which the English does not — it would print as written`);
        if (gone.length) warn(key, 'PLACEHOLDER', `leaves out {${gone.join('} {')}}${why} — that value will not appear`);
        if (/\{\s*'/.test(value)) {
          err(key, 'LITERAL', `the explorer prints {'…'} as written — write a bare @ | { here${table === 'both' ? ' (and this key is shared with the admin panel, so write none of them: rephrase)' : ''}`);
        }
      }
    }
    if (table === 'both' && /[@|]/.test(value.replace(/\{[A-Za-z0-9_]+\}/g, ''))) {
      err(key, 'SHARED', 'this key is drawn by BOTH the explorer and the admin panel: a bare @ or | breaks the admin panel and {\'…\'} prints as written in the explorer — rephrase without them');
    }
    if (inCatalogue) {
      if (codeSpans(value) !== codeSpans(en)) warn(key, 'CODE', '`code` spans differ from the English — they are usually names to keep as they are');
      const lead = (x) => x.match(/^\s*/)[0];
      const trail = (x) => x.match(/\s*$/)[0];
      if (lead(value) !== lead(en) || trail(value) !== trail(en)) warn(key, 'SPACE', 'leading/trailing whitespace differs from the English');
    }
  }
  let total = 0;
  const has = (key) => key in pack && typeof pack[key] === 'string' && !!pack[key].trim();
  for (const key of Object.keys(cat.strings)) {
    if (unusedForm(key)) continue;
    total += 1;
    if (!has(key)) missing.push(key);
  }
  // Plural keys that lack a form for one of the language's categories: they
  // show the plain (`other`) form for those numbers — correct words, maybe
  // not the natural ones.
  const pluralGaps = [];
  for (const key of Object.keys(cat.strings)) {
    if (!cat.plural[key] || !has(key)) continue;
    const t = cat.table[key];
    if (t === 'admin' || t === 'both') {
      const n = parseVueI18n(pack[key]).branches.length;
      if (k > 3 && n < k) pluralGaps.push({ key, lacks: `${k - n} of ${lang}'s ${k} forms` });
      continue;
    }
    const lacks = cats.filter((c) => c !== 'other' && !has(`${key}_${c}`));
    if (lacks.length) pluralGaps.push({ key, lacks: lacks.map((c) => `${key}_${c}`).join(' ') });
  }
  if (bytes > LIMITS.MaxUILocaleBytes) err('(pack)', 'SIZE', `${bytes} bytes of keys and values, the server takes at most ${LIMITS.MaxUILocaleBytes} per language`);
  if (complete) for (const key of missing) err(key, 'MISSING', 'not translated');

  return {
    errors,
    warnings,
    coverage: {
      translated,
      total,
      percent: total ? Math.floor((translated * 100) / total) : 0,
      missing,
      identical,
      rtl: isRTL(lang),
      bytes,
      plural: { categories: cats, gaps: pluralGaps },
    },
  };
}

/** A filex-app.json that is meant to be a language pack. */
export function checkManifest(m) {
  const problems = [];
  const bad = (code, msg) => problems.push({ key: '(manifest)', code, msg });
  if (m.manifest_version !== 1) bad('MANIFEST', 'manifest_version must be 1');
  if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(m.name ?? '')) bad('MANIFEST', 'name must match [a-z0-9][a-z0-9_-]{0,31}');
  if (!String(m.version ?? '').trim()) bad('MANIFEST', 'version is required');
  if (!m.label?.en) bad('MANIFEST', 'label.en is required');
  for (const f of ['actions', 'views', 'public_pages', 'settings', 'permissions']) {
    if (Array.isArray(m[f]) && m[f].length) bad('NOT_A_PACK', `${f} makes this an app with a module, not a language pack — a pack carries languages and nothing else`);
  }
  if (m.wasm) bad('NOT_A_PACK', '`wasm` makes this an app with a module; a language pack has none — remove it');
  if (!m.ui_locales || typeof m.ui_locales !== 'object' || !Object.keys(m.ui_locales).length) bad('MANIFEST', 'ui_locales is empty — a pack must carry at least one language');
  let total = 0;
  for (const strs of Object.values(m.ui_locales ?? {})) {
    for (const [k, v] of Object.entries(strs ?? {})) total += Buffer.byteLength(k) + Buffer.byteLength(String(v));
  }
  if (total > LIMITS.MaxUILocalesBytes) bad('SIZE', `${total} bytes of strings across its languages, the server takes at most ${LIMITS.MaxUILocalesBytes}`);
  return problems;
}

/* ── the command ───────────────────────────────────────────────────────── */

function main(argv) {
  const flag = (n) => argv.includes(n);
  const opt = (n) => {
    const i = argv.indexOf(n);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const file = argv.find((a, i) => !a.startsWith('--') && !['--lang', '--catalogue', '--src'].includes(argv[i - 1]));
  const die = (msg) => {
    console.error(`i18n-validate: ${msg}`);
    process.exit(2);
  };
  if (!file) die('usage: i18n-validate.mjs <translations/xx.json | filex-app.json> [--lang xx] [--catalogue dir | --src filex-checkout] [--complete] [--missing] [--plurals] [--json]');

  const here = path.dirname(fileURLToPath(import.meta.url));
  let catalogueDir = opt('--catalogue');
  let src = opt('--src');
  if (!catalogueDir && !src) {
    const templateCat = path.resolve(here, '..', 'catalogue');
    const checkout = path.resolve(here, '..');
    if (fs.existsSync(path.join(templateCat, 'filex-catalogue-en.json'))) catalogueDir = templateCat;
    else if (fs.existsSync(path.join(checkout, 'web', 'src', 'locales', 'en.json'))) src = checkout;
    else die('no catalogue: pass --catalogue <dir> (a release asset) or --src <filex checkout>');
  }
  let cat;
  try {
    cat = loadCatalogue({ catalogue: catalogueDir && path.resolve(catalogueDir), src: src && path.resolve(src) });
  } catch (e) {
    die(`cannot read the catalogue: ${e.message}`);
  }
  if (!flag('--no-compiler')) useCompiler([src, path.resolve(here, '..')].filter(Boolean));

  let doc;
  try {
    doc = JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch (e) {
    die(`${file}: ${e.message}`);
  }
  const isManifest = doc && typeof doc === 'object' && 'manifest_version' in doc;
  const langs = isManifest
    ? Object.entries(doc.ui_locales ?? {})
    : [[String(opt('--lang') || path.basename(file).replace(/\.json$/i, '')).toLowerCase(), doc]];

  const report = { catalogue: cat.source, syntax: compilerInUse(), file, manifest: isManifest ? checkManifest(doc) : [], languages: {} };
  let failed = report.manifest.length > 0;
  for (const [lang, pack] of langs) {
    const r = checkLanguage(lang, pack, cat, { complete: flag('--complete') });
    report.languages[lang] = r;
    if (r.errors.length) failed = true;
  }

  if (flag('--json')) {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
    process.exit(failed ? 1 : 0);
  }
  const serverKeys = Object.keys(cat.strings).filter((key) => cat.table[key] === 'server').length;
  console.log('filex language pack validator');
  console.log(`  catalogue : ${report.catalogue} — ${Object.keys(cat.strings).length} strings (${serverKeys} of them the server's: emails, public pages, notifications)`);
  console.log(`  syntax    : ${report.syntax}`);
  console.log(`  pack      : ${file}${isManifest ? ' (filex-app.json)' : ''}`);
  for (const p of report.manifest) console.log(`  ERROR ${p.code.padEnd(11)} ${p.msg}`);
  for (const [lang, r] of Object.entries(report.languages)) {
    const c = r.coverage;
    console.log('');
    if (c) {
      console.log(`[${lang}] ${c.percent}% translated — ${c.translated} of ${c.total} strings; the other ${c.missing.length} show in English`);
      if (c.identical) console.log(`  ${c.identical} value(s) are identical to the English (fine for names and codes; otherwise still to translate)`);
      console.log(`  plural categories: ${c.plural.categories.join(', ')} — explorer/server keys take <key>_${c.plural.categories.filter((x) => x !== 'other').join(' / <key>_') || '…'} beside the plain key (other); admin strings take ${c.plural.categories.length} | forms in that order`);
      if (c.plural.gaps.length) console.log(`  ${c.plural.gaps.length} plural key(s) lack a form for one of those categories and show the plain form for it${flag('--plurals') ? '' : ' (--plurals lists them)'}`);
    }
    const group = (list) => {
      const m = new Map();
      for (const x of list) m.set(x.code, (m.get(x.code) ?? 0) + 1);
      return [...m].map(([k, n]) => `${k}=${n}`).join(' ') || 'none';
    };
    console.log(`  errors: ${r.errors.length} (${group(r.errors)}) · warnings: ${r.warnings.length} (${group(r.warnings)})`);
    for (const e of r.errors) console.log(`  ERROR ${e.code.padEnd(11)} ${e.key}: ${e.msg}`);
    if (!flag('--quiet')) for (const w of r.warnings) console.log(`  warn  ${w.code.padEnd(11)} ${w.key}: ${w.msg}`);
    if (flag('--missing') && c) for (const k of c.missing) console.log(`  missing     ${k}: ${JSON.stringify(cat.strings[k])}`);
    if (flag('--plurals') && c) for (const g of c.plural.gaps) console.log(`  plural      ${g.key}: lacks ${g.lacks}`);
  }
  process.exit(failed ? 1 : 0);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
