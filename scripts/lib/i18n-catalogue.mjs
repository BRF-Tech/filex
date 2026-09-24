/**
 * i18n-catalogue — filex's strings as ONE flat catalogue: the shape a
 * language pack's `ui_locales[<lang>]` takes.
 *
 * filex draws its words from three tables:
 *
 *   explorer  packages/core/src/locales/en.ts   flat keys, rendered by the
 *             package's own `t()`: `{name}` is replaced verbatim, a plural is
 *             a separate `<key>_<category>` entry, and `@`, `|`, `{'…'}` are
 *             ordinary characters.
 *   admin     web/src/locales/en.json           NESTED, rendered by vue-i18n;
 *             a key is its dotted path (`appPlugins.wizard.title`), `|`
 *             separates plural forms, `@` starts a linked message, and a
 *             literal `@ { } | %{` is written `{'@'}` etc.
 *   server    backend/internal/srvtext/locales/en.json + the notification
 *             phrases of web/src/lib/notificationText.ts — every key under
 *             `server.`: the emails, the no-JavaScript public pages, the
 *             install review's permission sentences and the notifications.
 *             `{name}` and `<key>_<category>` plurals, nothing else.
 *
 * A pack addresses all three with one flat dotted namespace. That works because
 * (a) no key of one table is a dotted PREFIX of a key of another, and (b)
 * the keys explorer and admin SHARE (55 today: `storages.driver.*`,
 * `storages.fields.*`, `storages.fieldHelp.*`, `home.title`) have identical
 * English — one translation serves both screens, and must be written in the
 * syntax both accept (no `@`, `|` or `{'…'}` at all). The server table is
 * alone under `server.`. web/tests/i18n/langPackCatalogue.test.ts fails the
 * build when any of it stops being true.
 *
 * Plurals are CLDR's (`Intl.PluralRules`): zero, one, two, few, many, other.
 * docs/PLUGIN-KIT.md → "Plural forms" is the grammar.
 *
 * Used by scripts/i18n-export.mjs (the translator's catalogue), by the web
 * build (web/vite.config.ts writes it next to the SPA, the binary embeds it,
 * and the Apps screens measure a pack's coverage against it) and by the tests.
 *
 * ⚠ Reads the sources as TEXT — no TypeScript toolchain, no build — so a
 * translator can run the export from a plain checkout with Node alone.
 */
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';

/** The explorer's table: `export const en: Record<string, string> = { … };` */
export function loadCoreTable(file) {
  const src = fs.readFileSync(file, 'utf8');
  const m = src.match(/export\s+const\s+\w+\s*:\s*Record<string,\s*string>\s*=\s*/);
  if (!m) throw new Error(`${file}: no \`export const x: Record<string, string> =\` object literal`);
  const body = src.slice(m.index + m[0].length).replace(/;\s*$/, '');
  // ⚠ Evaluated in an EMPTY context: the file is a plain object literal of
  // string keys and string values (comments allowed); anything else throws.
  return vm.runInNewContext(`(${body})`, Object.create(null), { timeout: 2000 });
}

/** The admin panel's table, flattened to dotted keys. */
export function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object') flatten(v, key, out);
    else out[key] = v;
  }
  return out;
}

export function loadAdminTable(file) {
  return flatten(JSON.parse(fs.readFileSync(file, 'utf8')));
}

/**
 * One `const NAME … = { … };` object literal out of a TypeScript file — the
 * literal only (plain keys, strings, nested objects, comments), evaluated in
 * an empty context. It ends at the first `};` at the start of a line.
 */
function tsObjectLiteral(src, name, file) {
  const m = src.match(new RegExp(`const\\s+${name}\\b[^=]*=\\s*`));
  if (!m) throw new Error(`${file}: no \`const ${name} … =\` object literal`);
  const start = m.index + m[0].length;
  const end = src.indexOf('\n};', start);
  if (end < 0) throw new Error(`${file}: ${name} does not end with a \`};\` line`);
  return vm.runInNewContext(`(${src.slice(start, end + 2)})`, Object.create(null), { timeout: 2000 });
}

/** A value a translator could only copy: the whole string is one
 *  `{placeholder}`. There is no word in it, no order to choose and no
 *  punctuation — ten notification BODIES are just `{path}`, `{reason}` or
 *  `{body}`. Exporting them made every pack carry ten entries it had to
 *  reproduce byte for byte, and one typo lost the path off a bell row. They
 *  are left out; the renderer falls back to the same template. */
const BARE_PLACEHOLDER = /^\s*\{[A-Za-z0-9_]+\}\s*$/;

/**
 * The notification phrases as server keys, per language.
 *
 * ⚠ Their source stays web/src/lib/notificationText.ts: the bell, the
 * browser pop-up and the desktop app render them from there (one renderer,
 * three surfaces — see that file). They are keys of the SERVER table
 * (`server.notify.<event>.title|body`, a phrase's `one` → `title_one` /
 * `body_one`) because a notification is something the server sends; a pack
 * translates them beside the mails, and the renderer reads the pack's
 * `server.notify.*` strings.
 *
 * ⚠⚠ `WORDS` is NOT read here. The four words a notification falls back on
 * live in the server catalogue itself (`server.notify.word.*` in
 * backend/internal/srvtext/locales/*.json), because two of them — "Someone"
 * and "a file" — are also what a MAIL says when the uploader typed no name
 * or the file has none, and Go cannot read a TypeScript table. They were
 * written twice (`server.mail.drop_received.someone`,
 * `server.mail.share.unnamed_file`) until v0.43.0, so a translator saw one
 * word as two keys and could answer them differently. The table in
 * notificationText.ts stays as the desktop shell's offline fallback and is
 * held to the catalogue by web/tests/lib/notificationText.test.ts.
 */
export function loadNotifyTables(file) {
  const src = fs.readFileSync(file, 'utf8');
  const phrases = tsObjectLiteral(src, 'NOTIFICATION_PHRASES', file);
  const out = { en: {}, tr: {} };
  const say = (lang, key, value) => {
    if (typeof value === 'string' && value && !BARE_PLACEHOLDER.test(value)) out[lang][key] = value;
  };
  for (const lang of ['en', 'tr']) {
    for (const [event, byLang] of Object.entries(phrases)) {
      const p = byLang[lang];
      if (!p) continue;
      const base = `server.notify.${event}`;
      say(lang, `${base}.title`, p.title);
      say(lang, `${base}.body`, p.body);
      for (const [f, v] of Object.entries(p.one ?? {})) say(lang, `${base}.${f}_one`, v);
    }
  }
  return out;
}

/** The words notificationText.ts falls back on, as the catalogue holds them. */
export function notifyWords(file) {
  return tsObjectLiteral(fs.readFileSync(file, 'utf8'), 'WORDS', file);
}

/** All three tables of a filex checkout, English and Turkish. */
export function loadCatalogue(root) {
  const core = path.join(root, 'packages', 'core', 'src', 'locales');
  const web = path.join(root, 'web', 'src', 'locales');
  const srv = path.join(root, 'backend', 'internal', 'srvtext', 'locales');
  const notify = loadNotifyTables(path.join(root, 'web', 'src', 'lib', 'notificationText.ts'));
  return {
    explorer: loadCoreTable(path.join(core, 'en.ts')),
    explorerTr: loadCoreTable(path.join(core, 'tr.ts')),
    admin: loadAdminTable(path.join(web, 'en.json')),
    adminTr: loadAdminTable(path.join(web, 'tr.json')),
    server: { ...JSON.parse(fs.readFileSync(path.join(srv, 'en.json'), 'utf8')), ...notify.en },
    serverTr: { ...JSON.parse(fs.readFileSync(path.join(srv, 'tr.json'), 'utf8')), ...notify.tr },
  };
}

/** The translator's notes for the server table (context.json beside it). */
export function loadServerNotes(root) {
  try {
    return JSON.parse(fs.readFileSync(path.join(root, 'backend', 'internal', 'srvtext', 'locales', 'context.json'), 'utf8'));
  } catch {
    return { groups: {}, keys: {}, vars: {}, key_vars: {} };
  }
}

/**
 * The note for one server key (`english` is its English text): where it
 * appears — its group's note (the longest matching prefix) and its own — and
 * what each of its placeholders holds.
 */
export function serverNote(notes, key, english, isKey = () => false) {
  let group = '';
  for (const p of Object.keys(notes.groups ?? {})) {
    if (key.startsWith(p) && p.length > group.length) group = p;
  }
  const own = notes.keys?.[key];
  const about = [group ? notes.groups[group] : '', own ?? ''].filter(Boolean).join(' ');
  const base = pluralBaseOf(key, isKey) ?? key;
  const vars = {};
  for (const v of plainTokens(english)) {
    vars[v] = notes.key_vars?.[key]?.[v] ?? notes.key_vars?.[base]?.[v] ?? notes.vars?.[v] ?? '';
  }
  return { about, vars };
}

/** Which table a key lives in: `explorer`, `admin`, `both` or `server`. */
export function tableOf(cat, key) {
  if (cat.server && key in cat.server) return 'server';
  const e = key in cat.explorer;
  const a = key in cat.admin;
  return e && a ? 'both' : e ? 'explorer' : a ? 'admin' : '';
}

/** The grammar a translation of the key must follow. */
export const SYNTAX_OF = { explorer: 'plain', admin: 'vue-i18n', both: 'shared', server: 'server' };

/* ── plurals (CLDR) ────────────────────────────────────────────────────── */

/** CLDR's category order — the order admin-panel forms are written in. */
export const CLDR_ORDER = ['zero', 'one', 'two', 'few', 'many', 'other'];
/** The variables that carry the count in the explorer's `t()`. */
export const COUNT_VARS = ['count', 'n', 'days'];

export function plainTokens(text) {
  return [...new Set([...String(text).matchAll(/\{([A-Za-z0-9_]+)\}/g)].map((m) => m[1]))].sort();
}

/** `x_few` → `x` when `x` is a key (`has`), else undefined. */
export function pluralBaseOf(key, has) {
  const m = String(key).match(/^(.+)_(zero|one|two|few|many)$/);
  return m && has(m[1]) ? m[1] : undefined;
}

/**
 * The plural categories a language's COUNTS fall into, in CLDR order: the
 * categories Intl.PluralRules selects for some integer 0…999, and `other`
 * always (the plain form every language has).
 *
 * ⚠ Not `resolvedOptions().pluralCategories` as it stands: modern CLDR lists
 * `many` for Spanish, French, Italian, Portuguese and Catalan, and it only
 * ever means an exact million ("1 millón de archivos"). Counted in, every
 * one of those languages would be told it needs a third form for every
 * plural, and a Spanish admin-panel string written the classic way, "zero |
 * one | other", would be read in CLDR order (one | many | other) — 1 would
 * pick the "zero" text. The explorer, the admin panel, the server and this
 * file all use this definition.
 */
export function pluralCategories(lang) {
  let pr;
  try {
    pr = new Intl.PluralRules(lang);
  } catch {
    return ['one', 'other'];
  }
  const seen = new Set(['other']);
  for (let n = 0; n < 1000; n += 1) seen.add(pr.select(n));
  return CLDR_ORDER.filter((c) => seen.has(c));
}

/**
 * Is `key` a sentence about a count in its table — may a pack give it
 * category forms (`key_few`…) or, in the admin panel, `|`-separated forms?
 */
export function isPluralKey(cat, key) {
  const t = tableOf(cat, key);
  if (t === 'admin' || t === 'both') return /\|/.test(String(cat.admin[key]).replace(/\{'[^']*'\}/g, ''));
  const table = t === 'server' ? cat.server : cat.explorer;
  if (!table || !(key in table) || pluralBaseOf(key, (k) => k in table)) return false;
  if (`${key}_one` in table) return true;
  const tokens = plainTokens(table[key]);
  return t === 'server' ? tokens.includes('count') : COUNT_VARS.some((v) => tokens.includes(v));
}

/**
 * The plural categories of the languages filex is most likely translated
 * into — written into the catalogue's context so a translator sees which
 * forms their language needs without running anything. Any other tag:
 * `node scripts/i18n-export.mjs --lang <tag>`.
 */
export const COMMON_LANGUAGES = [
  'ar', 'az', 'bg', 'bn', 'ca', 'cs', 'cy', 'da', 'de', 'el', 'en', 'es', 'et', 'eu', 'fa', 'fi', 'fil', 'fr',
  'ga', 'gl', 'he', 'hi', 'hr', 'hu', 'hy', 'id', 'is', 'it', 'ja', 'ka', 'kk', 'ko', 'lt', 'lv', 'mk', 'ms',
  'nb', 'nl', 'pl', 'pt', 'pt-br', 'ro', 'ru', 'sk', 'sl', 'sq', 'sr', 'sv', 'sw', 'th', 'tr', 'uk', 'ur',
  'uz', 'vi', 'zh', 'zh-hant',
];

/* ── where a key appears (cheap: literal mentions in the source) ─────── */

const SKIP_DIRS = new Set(['node_modules', 'dist', 'locales', 'tests', '.vite']);

function walk(dir, out) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const e of entries) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (!SKIP_DIRS.has(e.name)) walk(p, out);
    } else if (/\.(vue|ts)$/.test(e.name) && !/\.d\.ts$/.test(e.name)) out.push(p);
  }
  return out;
}

/**
 * key → files that name it as a string literal. ⚠ A key built at run time
 * (`t(\`queue.status.${s}\`)`) is not found; the context then says nothing
 * rather than something wrong. `_one` keys inherit their plain key's places.
 * Server keys are described by their notes instead (most are assembled at
 * run time: `server.perm.` + a permission, `server.public.` + a page key).
 */
export function whereKeysAppear(root, keys) {
  const want = new Set(keys);
  const found = new Map();
  const files = [
    ...walk(path.join(root, 'packages', 'core', 'src'), []),
    ...walk(path.join(root, 'web', 'src'), []),
  ];
  const lit = /(['"`])([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+)\1/g;
  for (const f of files) {
    const src = fs.readFileSync(f, 'utf8');
    const rel = path.relative(root, f).split(path.sep).join('/');
    let m;
    while ((m = lit.exec(src))) {
      const k = m[2];
      for (const cand of [k, `${k}_one`]) {
        if (!want.has(cand)) continue;
        if (!found.has(cand)) found.set(cand, new Set());
        found.get(cand).add(rel);
      }
    }
  }
  return found;
}

/**
 * The version the catalogue describes — the BUILD's.
 *
 * ⚠ It said "filex 0.42.2" on the v0.43.0 branch: web/package.json is
 * bumped by the release step, so between releases it still names the LAST
 * release. In order: FILEX_VERSION (set it for a one-off build), the CI tag
 * (CI_COMMIT_TAG — the release pipeline builds the web on the tag), and only
 * then the package's field. A running server overrides it with its own
 * binary's version when it serves the file (backend/internal/api/
 * catalogue_version.go), which is where a translator is told to take it.
 *
 * ⚠ Not `git describe`: the release tags live on the public export's
 * history, not on the branch a build runs from — on this tree it answered
 * "0.41.4-96-g…", two releases stale.
 */
export function buildVersion(root) {
  const strip = (v) => String(v ?? '').trim().replace(/^v(?=\d)/, '');
  if (process.env.FILEX_VERSION) return strip(process.env.FILEX_VERSION);
  if (process.env.CI_COMMIT_TAG && /^v?\d+\.\d+\.\d+/.test(process.env.CI_COMMIT_TAG)) return strip(process.env.CI_COMMIT_TAG);
  try {
    return JSON.parse(fs.readFileSync(path.join(root, 'web', 'package.json'), 'utf8')).version ?? '';
  } catch {
    return '';
  }
}

/**
 * The catalogue as a translator receives it.
 *
 *   strings  { key: English }  — EXACTLY the shape of `ui_locales[<lang>]`:
 *            copy it, translate the values, and it is a language pack.
 *   context  { filex, about, plural_categories, keys: { key: { in, syntax,
 *            tr, where, about, vars, plural } } } — which table a key belongs
 *            to (and so which grammar applies), the Turkish reference, the
 *            files that use it (interface keys) or where it appears and what
 *            each placeholder holds (server keys), and whether it takes
 *            plural forms.
 *
 * Deterministic for one tree (sorted, no timestamp): the web build embeds it,
 * and two builds of the same commit must produce the same bytes.
 */
export function buildCatalogue(root, { where = true, langs = [] } = {}) {
  const cat = loadCatalogue(root);
  const notes = loadServerNotes(root);
  const keys = [...new Set([...Object.keys(cat.admin), ...Object.keys(cat.explorer), ...Object.keys(cat.server)])].sort();
  const places = where ? whereKeysAppear(root, keys.filter((k) => !(k in cat.server))) : new Map();
  const strings = {};
  const ctx = {};
  for (const k of keys) {
    const t = tableOf(cat, k);
    strings[k] = t === 'server' ? cat.server[k] : t === 'explorer' ? cat.explorer[k] : cat.admin[k];
    const row = { in: t, syntax: SYNTAX_OF[t] };
    const tr = t === 'server' ? cat.serverTr[k] : t === 'explorer' ? cat.explorerTr[k] : cat.adminTr[k];
    if (tr !== undefined) row.tr = tr;
    if (t === 'server') {
      const { about, vars } = serverNote(notes, k, strings[k], (x) => x in cat.server);
      if (about) row.about = about;
      if (Object.keys(vars).length) row.vars = vars;
    } else {
      const w = places.get(k);
      if (w?.size) row.where = [...w].sort();
    }
    if (isPluralKey(cat, k)) row.plural = true;
    ctx[k] = row;
  }
  const plural = {};
  for (const l of [...new Set([...COMMON_LANGUAGES, ...langs.map((x) => String(x).toLowerCase())])].sort()) {
    plural[l] = pluralCategories(l);
  }
  return {
    strings,
    context: {
      filex: buildVersion(root),
      about:
        "Every key filex has — the interface and the text the server writes. `in`: explorer (plain {name} placeholders, @ | literal) · admin (vue-i18n: write {'@'} for @) · both (no @, | or {'…'} at all) · server (emails, public pages, notifications: {name} only, and exactly the placeholders of the English). `plural`: the key takes plural forms — explorer/server: extra keys <key>_zero|_one|_two|_few|_many for the categories your language has (the plain key is `other`); admin: `|`-separated forms, one per category, in CLDR order. `plural_categories`: which categories a language has. `tr`: the Turkish reference. docs: https://docs.filex.sh/PLUGIN-KIT#writing-a-language-pack",
      plural_categories: plural,
      keys: ctx,
    },
    cat,
  };
}

/** The two files of a catalogue, as `{ name: text }`. */
export function catalogueFiles(root, opts) {
  const { strings, context } = buildCatalogue(root, opts);
  return {
    'filex-catalogue-en.json': `${JSON.stringify(strings, null, 2)}\n`,
    'filex-catalogue-context.json': `${JSON.stringify(context, null, 2)}\n`,
  };
}
