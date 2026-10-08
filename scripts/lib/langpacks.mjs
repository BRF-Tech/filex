/**
 * langpacks - the language packs kept in step with this tree, between
 * releases instead of on release day.
 *
 * A language pack is its own repository (BRF-Tech/filex-lang-template and the
 * packs made from it): `translations/<tag>.json` is the translation,
 * `catalogue/filex-catalogue-en.json` the English it was translated against,
 * `filex-app.json` the manifest a server installs, and `scripts/pack.mjs`
 * (sync, build) plus the `validate` script of its package.json the pack's own
 * mechanics. This module never re-implements those: it reads a pack, compares
 * it with a catalogue, prepares the translator's worklist and folds the
 * answers back in. scripts/langpacks.mjs is the command that runs the pack's
 * own scripts, the validators and git around it.
 *
 * ⚠ The pack's `catalogue/` is the BASELINE: the English its translation was
 * written for. A key whose English in this tree differs from it was
 * translated for words that are gone, so it is reported as `changed` until a
 * translator answers it - `pack.mjs sync` alone would keep the old
 * translation without a word. The catalogue in a pack therefore only moves
 * forward together with the answers (scripts/langpacks.mjs apply).
 *
 * ⚠ A key filex no longer has leaves the translation in `apply` and
 * `release`, right after the pack's `pack.mjs sync` (staleKeys,
 * withoutStale). Through 0.53 every pack's sync kept such a key in
 * `translations/<tag>.json`, and the packs' own validators (validate-de,
 * validate-fr, style-check) refuse it as UNKNOWN: 0.53 dropped
 * `tenants.modeOff`, `apply` went red on it and put every pack back, and the
 * key was deleted by hand in each pack before the translations could land.
 * filex drops keys every release, so this never waits for a pack's script.
 *
 * Pure apart from reading files; web/tests/i18n/langPacksSync.test.ts holds
 * it. docs/CONTRIBUTING.md -> Translations and language packs.
 */
import fs from 'node:fs';
import path from 'node:path';
import { CLDR_ORDER, pluralCategories } from './i18n-catalogue.mjs';
import { checkLanguage } from '../i18n-validate.mjs';

/** The two files of a catalogue: a release asset, a pack's `catalogue/`, a worklist's. */
export const CATALOGUE_FILES = ['filex-catalogue-en.json', 'filex-catalogue-context.json'];

/** Where a release's catalogue is attached (the same address `pack.mjs sync --from vX.Y.Z` reads). */
export const RELEASE_ASSETS = 'https://github.com/BRF-Tech/filex/releases/download';

/** Pack checkouts are directories named filex-lang-<something>; the template is not a pack. */
export const PACK_PREFIX = 'filex-lang-';
export const TEMPLATE_DIR = 'filex-lang-template';

/**
 * The remote a maintainer's pack checkout names the build host's checkout
 * by: the nightly translation commits there (scripts/langpacks-nightly.mjs),
 * `langpacks.mjs pull` fast-forwards from it, and release day pushes the
 * release commit back to it. It is never where a pack is published.
 */
export const NIGHTLY_REMOTE = 'nightly';

/** Where a pack is published from: github, else origin, else any remote but the nightly one; '' for none. */
export function publishRemote(remotes) {
  return ['github', 'origin'].find((r) => remotes.includes(r)) ?? remotes.find((r) => r !== NIGHTLY_REMOTE) ?? '';
}

/**
 * What `pull` does with a checkout `ahead` commits ahead of the nightly
 * remote's branch and `behind` commits behind it: nothing (up to date), a
 * fast-forward, or nothing again because both sides moved (diverged: a
 * person merges).
 */
export function pullStep({ ahead, behind }) {
  if (!behind) return 'up to date';
  return ahead ? 'diverged' : 'fast-forward';
}

/** The README block `release` rewrites: everything between these two lines. */
export const STATUS_OPEN = '<!-- langpack:status -->';
export const STATUS_CLOSE = '<!-- /langpack:status -->';

/**
 * Names a translation writes exactly as the English does. ONLYOFFICE is the
 * product's one spelling (every pack glossary: "never translated"), filex is
 * lower case, and the protocol and product names keep their casing. Measured
 * 2026-10-06 over the four packs on the maintainer's machine (4 x 5,784
 * strings): no string breaks this rule, so a hit is a real slip.
 */
export const FIXED_NAMES = ['filex', 'ONLYOFFICE', 'draw.io', 'GitHub', 'WebDAV', 'SFTP', 'LDAP', 'OIDC', 'MCP', 'WebAssembly'];

/**
 * The rules a translator (a person or an agent) follows for a worklist. They
 * are the template's and the validator's rules in one place; the pack's
 * glossary comes first wherever it says more.
 */
export const TRANSLATOR_RULES = [
  'Write `text` for every item. An item of kind `changed` was translated for the English in `was`; the English is now `en`. Translate `en`, and keep `current` only if it still says exactly that.',
  "Read the pack's glossary first and use its terms, its voice and its typography. When a term does not fit a sentence, rephrase the sentence; do not switch terms.",
  'Names stay as the English writes them: filex (lower case), ONLYOFFICE (that spelling, never translated), draw.io, GitHub, WebDAV, SFTP, LDAP, OIDC, MCP and every other product or protocol name the glossary lists.',
  'A dash is the plain hyphen "-": " - " between two clauses, "3-60" for a range. Never an em dash or an en dash, nor a character that only looks like a hyphen.',
  'Keep every {placeholder} exactly as written. A `server` item must keep all of them; the server refuses a translation that drops one.',
  "Follow the item's `syntax`: plain (the explorer: @ and | are ordinary characters), vue-i18n (the admin panel: write {'@'} for @, plural forms split by |, in the CLDR order of the language's categories), shared (drawn by both: no @, no | and no {'...'} at all), server (emails and public pages: {name} only).",
  "`forms_needed` lists the plural categories of the language that the English has no key for. Write a form in `forms` (\"few\": \"...\") only where the language's words change for it; a form left out shows the plain text. Every form keeps the count placeholder unless the category holds one single number and the language says it as a word.",
  'Keep `code` spans, <...> tokens, paths, environment variables and leading or trailing spaces, a trailing ... or :, as the English has them.',
  "Write the language's own letters and punctuation: every accent, umlaut and cedilla (é, ü, ß, ñ, ç), each language in its own script with its own comma and question mark, and the quotation marks the glossary names. Never fall back to a plain ASCII spelling of a word.",
  "One term per concept: a word the glossary fixes is written that way everywhere. Where the glossary is silent, use the word the pack's translation already uses for the same English term (search it), and never two words for one concept in the pack.",
  'Edit only `text` and `forms` in the worklist. The pack, its catalogue and its manifest are written by `scripts/langpacks.mjs apply`.',
];

const TAG = /^[a-z]{2,3}(-[a-z0-9]{2,8})*$/;
const FORM = /^(.+)_(zero|one|two|few|many)$/;

export const readJSON = (p) => JSON.parse(fs.readFileSync(p, 'utf8'));
/** JSON the way pack.mjs and i18n-export write it: two spaces, one trailing newline. */
export const jsonText = (v) => `${JSON.stringify(v, null, 2)}\n`;
const hasText = (v) => typeof v === 'string' && v.trim() !== '';

/* -- where the packs are ----------------------------------------------- */

/** The main checkout a worktree at <checkout>/.claude/worktrees/<name> belongs to; null for any other directory. */
export function mainCheckout(root) {
  const up = path.dirname(path.resolve(root));
  if (path.basename(up) === 'worktrees' && path.basename(path.dirname(up)) === '.claude') return path.dirname(path.dirname(up));
  return null;
}

/**
 * The directories a pack checkout sits in: the one beside this checkout and,
 * from a worktree at <checkout>/.claude/worktrees/<name>, the one beside the
 * main checkout as well (a worktree has no packs beside it).
 */
export function packParents(root) {
  const abs = path.resolve(root);
  const parents = [path.dirname(abs)];
  const main = mainCheckout(abs);
  if (main) parents.push(path.dirname(main));
  return [...new Set(parents)];
}

/**
 * Where this tree's installed packages are looked for (vue-i18n's own
 * parser): the tree, then, from a worktree, the main checkout. A worktree
 * made for one run - the nightly translation's - has no node_modules of its
 * own, and without this the answers were checked by the built-in parser only.
 */
export function checkoutRoots(root) {
  const abs = path.resolve(root);
  const main = mainCheckout(abs);
  return main ? [abs, main] : [abs];
}

/** A pack checkout: a manifest, translations and the catalogue they follow. */
export function isPackDir(dir) {
  return ['filex-app.json', 'translations', path.join('catalogue', CATALOGUE_FILES[0])].every((p) => fs.existsSync(path.join(dir, p)));
}

/**
 * The pack checkouts to work on: `packs` when given, else FILEX_LANG_PACKS
 * (directories separated by the platform's path delimiter), else every
 * filex-lang-* pack beside the checkout, the template excepted.
 */
export function findPacks(root, { packs = [], env = process.env } = {}) {
  if (packs.length) return packs.map((p) => path.resolve(p));
  if (env.FILEX_LANG_PACKS) return env.FILEX_LANG_PACKS.split(path.delimiter).filter(Boolean).map((p) => path.resolve(p));
  for (const parent of packParents(root)) {
    let names;
    try {
      names = fs.readdirSync(parent);
    } catch {
      continue;
    }
    const found = names
      .filter((n) => n.startsWith(PACK_PREFIX) && n !== TEMPLATE_DIR)
      .sort()
      .map((n) => path.join(parent, n))
      .filter(isPackDir);
    if (found.length) return found;
  }
  return [];
}

/** The template checkout beside this one (or beside the main checkout), or null. */
export function findTemplate(root) {
  for (const parent of packParents(root)) {
    const d = path.join(parent, TEMPLATE_DIR);
    if (isPackDir(d)) return d;
  }
  return null;
}

/**
 * The template's README for a new release: the version its catalogue is for,
 * and the catalogue's size, written with the separator the README uses (it
 * writes "5 502" with a narrow no-break space).
 */
export function templateReadme(readme, version, total) {
  return String(readme)
    .replace(/The catalogue here is for \*\*filex v\d+\.\d+\.\d+\*\*/, `The catalogue here is for **filex v${version}**`)
    .replace(/(filex's catalogue is )(\d(?:[\d,.]|[^\S\n])*\d|\d)( keys)/, (m, a, n, b) => {
      const sep = (/\d(\D)\d/.exec(n) ?? [])[1] ?? ',';
      return `${a}${String(total).replace(/\B(?=(\d{3})+(?!\d))/g, sep)}${b}`;
    });
}

/** One pack checkout, as files say it is. */
export function readPack(dir) {
  const manifest = readJSON(path.join(dir, 'filex-app.json'));
  const tdir = path.join(dir, 'translations');
  const langs = fs
    .readdirSync(tdir)
    .filter((f) => f.endsWith('.json'))
    .map((f) => f.slice(0, -5))
    .filter((t) => TAG.test(t))
    .sort();
  const translations = {};
  for (const t of langs) translations[t] = readJSON(path.join(tdir, `${t}.json`));
  let catalogueVersion = '';
  try {
    catalogueVersion = String(readJSON(path.join(dir, 'catalogue', CATALOGUE_FILES[1])).filex ?? '');
  } catch {
    catalogueVersion = '';
  }
  let pkg = null;
  try {
    pkg = readJSON(path.join(dir, 'package.json'));
  } catch {
    pkg = null;
  }
  return {
    dir,
    base: path.basename(dir),
    name: String(manifest.name ?? ''),
    version: String(manifest.version ?? ''),
    langs,
    translations,
    catalogue: readJSON(path.join(dir, 'catalogue', CATALOGUE_FILES[0])),
    catalogueText: fs.readFileSync(path.join(dir, 'catalogue', CATALOGUE_FILES[0]), 'utf8'),
    catalogueVersion,
    pkg,
  };
}

/* -- what a language still needs --------------------------------------- */

/**
 * What one language still needs against `next`, the catalogue it is to
 * follow, when `prev` is the catalogue its translation was written for:
 *
 *   missing  keys of `next` with no text (new keys, and keys never translated)
 *   changed  keys translated for English that `next` words differently
 *   removed  keys of `prev` that `next` no longer has (nothing to translate:
 *            `apply` and `release` drop them from the translation)
 */
export function packDiff({ prev, next, translation }) {
  const missing = [];
  const changed = [];
  for (const k of Object.keys(next)) {
    if (!hasText(translation[k])) missing.push(k);
    else if (k in prev && prev[k] !== next[k]) changed.push(k);
  }
  const removed = Object.keys(prev).filter((k) => !(k in next));
  return { missing: missing.sort(), changed: changed.sort(), removed: removed.sort() };
}

/**
 * The plural categories of `lang` a translation of `key` writes as extra keys
 * (`<key>_few` ...): the explorer and the server take one key per category,
 * the plain key is `other`, and a category the English already has a key for
 * (`<key>_one`) is an item of its own. The admin panel writes its forms
 * inside one string, so it has none.
 */
export function formsNeeded(lang, key, row, next) {
  if (!row?.plural || !(row.in === 'explorer' || row.in === 'server')) return [];
  const f = FORM.exec(key);
  if (f && f[1] in next) return [];
  return pluralCategories(lang).filter((c) => c !== 'other' && !(`${key}_${c}` in next));
}

/**
 * The worklist items of one language: what is missing and what changed, with
 * everything a translator needs beside it.
 *
 * ⚠ The answer comes right after its key - `key`, `text`, then `forms` when
 * the item takes any - and everything about the string after them. In the
 * written worklist the line `"key": "<key>",` is therefore followed by
 * `"text": ""` exactly once, so a person's editor or an agent's single-edit
 * tool finds the place to answer without reading the item's other lines
 * (the nightly translation answers hundreds of items this way).
 */
export function worklistItems({ lang, diff, prev, next, context, translation }) {
  const items = [];
  const add = (kind, key) => {
    const row = context?.keys?.[key] ?? {};
    const need = formsNeeded(lang, key, row, next);
    const item = { key, text: '' };
    if (need.length) item.forms = {};
    item.kind = kind;
    item.en = next[key];
    if (kind === 'changed') {
      item.was = prev[key];
      item.current = translation[key];
    }
    for (const f of ['in', 'syntax', 'tr', 'about', 'vars']) if (row[f] !== undefined) item[f] = row[f];
    if (row.where?.length) item.where = row.where.slice(0, 3);
    if (row.plural) item.plural = true;
    if (need.length) {
      item.forms_needed = need;
      const cur = {};
      for (const c of need) if (hasText(translation[`${key}_${c}`])) cur[c] = translation[`${key}_${c}`];
      if (kind === 'changed' && Object.keys(cur).length) item.current_forms = cur;
    }
    items.push(item);
  };
  for (const k of diff.missing) add('missing', k);
  for (const k of diff.changed) add('changed', k);
  return items.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
}

/* -- the answers ------------------------------------------------------- */

const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * Does `text` write a fixed name differently from `en`? A name the English
 * has must be there as often, and a spelling of it that the English does not
 * use (OnlyOffice, Filex) is a slip; `FILEX_PUBLIC_URL` in both is not.
 */
export function nameProblem(en, text, names = FIXED_NAMES) {
  for (const n of names) {
    const count = (s) => String(s).split(n).length - 1;
    if (count(text) < count(en)) return `"${n}" is missing or spelled differently: write it as the English does`;
    const any = new RegExp(escapeRe(n), 'gi');
    const inEnglish = new Set(String(en).match(any) ?? []);
    const odd = (String(text).match(any) ?? []).filter((x) => x !== n && !inEnglish.has(x));
    if (odd.length) return `"${odd[0]}": the name is written "${n}"`;
  }
  return '';
}

/**
 * Fold a worklist's answers into a translation. Returns the new translation
 * (the order of `translation`, each key's extra plural forms right after it
 * in CLDR order) and the problems that keep it from being written:
 * UNTRANSLATED, UNKNOWN (not in the worklist's catalogue), NAME, FORM.
 *
 * An item with `forms_needed` owns those extra keys: they become exactly the
 * forms it gives (none given: none kept, and the plain text shows).
 */
export function mergeItems(translation, items, { next, names = FIXED_NAMES } = {}) {
  const out = { ...translation };
  const problems = [];
  const owned = new Map();
  for (const it of items) {
    const key = it.key;
    if (!(key in next)) {
      problems.push({ key, code: 'UNKNOWN', msg: 'not in the catalogue this worklist was made from' });
      continue;
    }
    const text = typeof it.text === 'string' ? it.text : '';
    if (!hasText(text)) {
      problems.push({ key, code: 'UNTRANSLATED', msg: 'no text' });
      continue;
    }
    const np = nameProblem(next[key], text, names);
    if (np) problems.push({ key, code: 'NAME', msg: np });
    out[key] = text;
    const need = Array.isArray(it.forms_needed) ? it.forms_needed : [];
    if (!need.length) continue;
    const given = it.forms && typeof it.forms === 'object' ? it.forms : {};
    const forms = {};
    for (const [c, v] of Object.entries(given)) {
      if (!need.includes(c)) {
        problems.push({ key, code: 'FORM', msg: `forms.${c}: this key takes forms for ${need.join(', ')} only` });
        continue;
      }
      if (!hasText(v)) continue;
      const fp = nameProblem(next[key], v, names);
      if (fp) problems.push({ key: `${key}_${c}`, code: 'NAME', msg: fp });
      forms[c] = v;
    }
    owned.set(key, { need, forms });
  }
  const ownedForm = (k) => {
    const m = FORM.exec(k);
    return !!m && owned.has(m[1]) && owned.get(m[1]).need.includes(m[2]);
  };
  const result = {};
  for (const k of Object.keys(out)) {
    if (ownedForm(k)) continue;
    result[k] = out[k];
    const o = owned.get(k);
    if (o) for (const c of CLDR_ORDER) if (o.forms[c] !== undefined) result[`${k}_${c}`] = o.forms[c];
  }
  return { translation: result, problems };
}

/**
 * The platform validator's verdict on the answered keys alone (and their
 * forms): what `node scripts/validate.mjs` in the pack would say about them,
 * before a file of the pack is touched. Errors and warnings both count: a
 * pack is kept at 0 errors and 0 warnings.
 */
export function itemProblems(lang, merged, items, validatorCatalogue) {
  const subset = {};
  for (const it of items) {
    if (typeof merged[it.key] === 'string') subset[it.key] = merged[it.key];
    for (const c of it.forms_needed ?? []) {
      const k = `${it.key}_${c}`;
      if (typeof merged[k] === 'string') subset[k] = merged[k];
    }
  }
  const r = checkLanguage(lang, subset, validatorCatalogue, { complete: false });
  return [...r.errors, ...r.warnings].filter((p) => !p.key.startsWith('('));
}

/** The translation as `pack.mjs sync` leaves it: every key of `next` in its order (empty when new), then what the translation has beyond them. */
export function syncedTranslation(translation, next) {
  const out = {};
  for (const k of Object.keys(next)) out[k] = typeof translation[k] === 'string' ? translation[k] : '';
  for (const [k, v] of Object.entries(translation)) if (!(k in next)) out[k] = v;
  return out;
}

/**
 * Does filex still have `key`? A key of the catalogue `next`, or a plural
 * form of one (`<key>_few` ...: a language writes the categories the English
 * has no key for). The template's `pack.mjs` draws the same line (known()).
 */
export function knownKey(key, next) {
  if (key in next) return true;
  const f = FORM.exec(key);
  return !!f && f[1] in next;
}

/** The keys of a translation filex no longer has, in the translation's order: filled or empty, they go. */
export function staleKeys(translation, next) {
  return Object.keys(translation).filter((k) => !knownKey(k, next));
}

/**
 * The translation without the keys filex no longer has, everything else in
 * its order: what `apply` and `release` write right after the pack's
 * `pack.mjs sync`, which kept them through 0.53 (and the packs' validators
 * refuse them). A wording worth keeping is in the pack's git history.
 */
export function withoutStale(translation, next) {
  return Object.fromEntries(Object.entries(translation).filter(([k]) => knownKey(k, next)));
}

/* -- release day ------------------------------------------------------- */

/** 0.1.9 -> 0.1.10. A pack moves one patch version per filex release. */
export function bumpPatch(v) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(String(v).trim());
  if (!m) throw new Error(`${JSON.stringify(v)} is not a plain X.Y.Z version`);
  return `${m[1]}.${m[2]}.${Number(m[3]) + 1}`;
}

/** Strings per table of a catalogue context: admin, explorer, server and both (a key drawn by both counts once, there). */
export function tableCounts(context) {
  const n = { admin: 0, explorer: 0, server: 0, both: 0 };
  for (const row of Object.values(context?.keys ?? {})) if (row.in in n) n[row.in] += 1;
  return n;
}

/** Extra plural forms: keys of the translation that are a form of a catalogue key, for a category the language has. */
export function extraForms(lang, translation, next) {
  const cats = pluralCategories(lang);
  return Object.keys(translation).filter((k) => {
    if (k in next || !hasText(translation[k])) return false;
    const m = FORM.exec(k);
    return !!m && m[1] in next && cats.includes(m[2]);
  }).length;
}

const num = (n) => Number(n).toLocaleString('en-US');

/**
 * The README's status block: version, catalogue, coverage, validators.
 * `languages`: [{ tag, translated, total, percent, extra }], measured on the
 * manifest the way `validate.mjs filex-app.json` measures it;
 * `validators`: { errors, warnings } of that validator. The block is only
 * written after the pack's own `validate` script passed.
 */
export function statusBlock({ version, filex, total, counts, languages, validators = { errors: 0, warnings: 0 } }) {
  const many = languages.length > 1;
  const cov = languages
    .map((l) => `${many ? `${l.tag}: ` : ''}${l.percent} % - ${num(l.translated)} of ${num(l.total)} strings${l.extra ? `, plus ${num(l.extra)} extra plural form${l.extra === 1 ? '' : 's'}` : ''}`)
    .join('; ');
  const plural = (n, word) => `${num(n)} ${word}${n === 1 ? '' : 's'}`;
  return [
    STATUS_OPEN,
    '| | |',
    '|---|---|',
    `| Version | ${version}, for filex ${filex} |`,
    `| Catalogue | filex **${filex}** (\`catalogue/\`): ${num(total)} strings - ${num(counts.admin)} admin, ${num(counts.explorer)} explorer, ${num(counts.server)} the server's, ${num(counts.both)} drawn by both |`,
    `| Coverage | ${cov} |`,
    `| Validators | platform: ${plural(validators.errors, 'error')}, ${plural(validators.warnings, 'warning')}; the pack's own check passes - the last run is [\`validate-output.txt\`](validate-output.txt) |`,
    STATUS_CLOSE,
  ].join('\n');
}

/** The README with its status block replaced, or null when it has none (the two marker lines, in order). */
export function replaceStatusBlock(readme, block) {
  const text = String(readme);
  const a = text.indexOf(STATUS_OPEN);
  const b = text.indexOf(STATUS_CLOSE);
  if (a < 0 || b < a) return null;
  return text.slice(0, a) + block + text.slice(b + STATUS_CLOSE.length);
}

/* -- commit messages --------------------------------------------------- */

/** `authProviders.* (12), users.newTitle` - keys grouped by their first segment, for a commit body. */
export function groupKeys(keys, limit = 8) {
  const groups = new Map();
  for (const k of keys) {
    const g = k.split('.')[0];
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g).push(k);
  }
  const parts = [...groups.entries()]
    .sort((a, b) => b[1].length - a[1].length || (a[0] < b[0] ? -1 : 1))
    .map(([g, ks]) => (ks.length === 1 ? ks[0] : `${g}.* (${ks.length})`));
  return parts.length > limit ? `${parts.slice(0, limit).join(', ')} and ${parts.length - limit} more` : parts.join(', ');
}

/** Text wrapped at `width` columns, for a commit body. */
export const wrap = (text, width = 72) => {
  const out = [];
  let line = '';
  for (const w of String(text).split(/\s+/).filter(Boolean)) {
    if (line && line.length + 1 + w.length > width) {
      out.push(line);
      line = w;
    } else line = line ? `${line} ${w}` : w;
  }
  if (line) out.push(line);
  return out.join('\n');
};

const bullet = (text) => wrap(text, 70).replace(/\n/g, '\n  ').replace(/^/, '- ');

/**
 * The message of the commit `apply` makes in a pack. `diff.removed`: the keys
 * filex no longer has that the commit drops from the translation.
 */
export function applyMessage({ commit, version, translated, total, diff, worklist, validate, trailers = [] }) {
  const lines = [`Sync to filex ${commit}: ${num(translated)} of ${num(total)}`, ''];
  lines.push(
    wrap(
      `Catalogue refreshed from filex ${commit} (${version}) by scripts/langpacks.mjs apply, from the worklist ${worklist}. ` +
        `${diff.missing.length} string(s) translated, ${diff.changed.length} whose English changed translated again, ` +
        `${diff.removed.length} key(s) filex no longer has dropped from the translation; the pack version stays where it is.`,
    ),
  );
  lines.push('');
  if (diff.missing.length) lines.push(bullet(`Translated: ${groupKeys(diff.missing)}.`));
  if (diff.changed.length) lines.push(bullet(`English changed, translated again: ${groupKeys(diff.changed)}.`));
  if (diff.removed.length) lines.push(bullet(`Removed with filex, dropped from the translation: ${groupKeys(diff.removed)}.`));
  lines.push('', wrap(`Validators: \`${validate}\` passed; validate-output.txt is this run's.`));
  if (trailers.length) lines.push('', ...trailers);
  return `${lines.join('\n')}\n`;
}

/**
 * The message of the commit `release` makes in a pack. `diff.removed`: the
 * keys filex no longer has that the commit drops from the translation.
 */
export function releaseMessage({ pkgName, version, filex, diff, kept = [], validate, trailers = [] }) {
  const lines = [`${pkgName} ${version}: for filex ${filex}, the catalogue from the release tag`, ''];
  const what = [
    'no new key since the last sync',
    diff.changed.length ? `${diff.changed.length} whose English changed (kept, below)` : 'no English changed',
    diff.removed.length ? `${diff.removed.length} key(s) filex ${filex} no longer has, dropped from the translation (${groupKeys(diff.removed, 4)})` : '',
  ]
    .filter(Boolean)
    .join(', ');
  lines.push(
    wrap(
      `The catalogue is filex ${filex}'s release asset (scripts/langpacks.mjs release): ${what}. ` +
        `The version moves to ${version} for filex ${filex} and the README's status block follows.`,
    ),
  );
  if (kept.length) {
    lines.push('', bullet(`Kept as translated for the earlier English (--keep-changed): ${groupKeys(kept)}.`));
  }
  lines.push('', wrap(`Validators: \`${validate}\` passed; validate-output.txt is this run's.`));
  if (trailers.length) lines.push('', ...trailers);
  return `${lines.join('\n')}\n`;
}
