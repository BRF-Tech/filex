#!/usr/bin/env node
/**
 * langpacks - keep the filex language packs in step with this tree.
 *
 *   node scripts/langpacks.mjs status  [--packs a,b] [--catalogue <dir|vX.Y.Z>] [--keys] [--json <file|->] [--check]
 *   node scripts/langpacks.mjs todo    [--packs a,b] [--catalogue <dir|vX.Y.Z>] [--out <dir>]
 *   node scripts/langpacks.mjs apply   --worklist <dir> [--packs a,b] [--commit] [--trailer "<line>"]
 *   node scripts/langpacks.mjs pull    [--packs a,b]
 *   node scripts/langpacks.mjs release <X.Y.Z> [--packs a,b] [--template <dir>] [--catalogue <dir>] [--keep-changed] [--dry-run] [--trailer "<line>"]
 *
 * The packs are the filex-lang-* checkouts beside this one (from a worktree:
 * beside the main checkout), FILEX_LANG_PACKS, or --packs. The catalogue they
 * are compared with is this tree's (built the way scripts/i18n-export.mjs
 * builds it), a directory holding filex-catalogue-en.json and
 * filex-catalogue-context.json, or a release's assets (vX.Y.Z).
 *
 *   status   what every pack still needs: keys with no translation, keys
 *            whose English changed since the pack's catalogue, keys filex no
 *            longer has. --check exits 1 while anything is pending.
 *   todo     writes the translator's worklists for every pack that needs
 *            something (one JSON per pack, with the English, the Turkish
 *            reference, where the key is used and the rules), the catalogue
 *            they were made from, status.json and AGENT.md. Nothing in a
 *            pack is touched.
 *   apply    folds the answered worklists into the packs: checks every
 *            answer first (and touches nothing while one is wrong), then the
 *            pack's own `pack.mjs sync`, drops from every translation the
 *            keys filex no longer has, the answers, `pack.mjs build`, its
 *            `validate` script (validate-output.txt), and with --commit a
 *            local commit. A pack whose validators go red is put back as it
 *            was. Nothing is pushed.
 *   pull     brings the nightly translations home: in every pack checkout
 *            with a `nightly` remote (the build host's checkout, where the
 *            nightly translation commits), fetches it and fast-forwards to
 *            it. A checkout with changes of its own, or one both sides moved
 *            on, is left for a person.
 *   release  release day: refreshes every pack from the release's catalogue
 *            (the keys filex no longer has leave its translations, as in
 *            apply), moves its version one patch up, rewrites the README's
 *            status block, validates and commits, then prints the signed tag and the
 *            push commands. It signs, tags and pushes nothing itself, and it
 *            holds back a pack that still lacks a translation, or lacks the
 *            commits its `nightly` remote has (pull first). The template
 *            (filex-lang-template beside the packs, or --template) gets the
 *            release's catalogue too, with no version and no tag.
 *
 * Exit: 0 done, 1 a pack is pending, held back or red, 2 could not run.
 * docs/CONTRIBUTING.md -> Translations and language packs.
 */
import { spawnSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildCatalogue, buildVersion, pluralCategories } from './lib/i18n-catalogue.mjs';
import { checkLanguage, loadCatalogue as loadValidatorCatalogue, useCompiler } from './i18n-validate.mjs';
import {
  CATALOGUE_FILES,
  NIGHTLY_REMOTE,
  RELEASE_ASSETS,
  STATUS_OPEN,
  TRANSLATOR_RULES,
  applyMessage,
  bumpPatch,
  checkoutRoots,
  extraForms,
  findPacks,
  findTemplate,
  isPackDir,
  itemProblems,
  jsonText,
  mergeItems,
  packDiff,
  publishRemote,
  pullStep,
  readJSON,
  readPack,
  releaseMessage,
  replaceStatusBlock,
  staleKeys,
  statusBlock,
  syncedTranslation,
  tableCounts,
  templateReadme,
  withoutStale,
  worklistItems,
  wrap,
} from './lib/langpacks.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** What a pack's package.json runs as `validate` when it has none: the template's own pair. */
export const DEFAULT_VALIDATE = 'node scripts/pack.mjs build --check && node scripts/validate.mjs filex-app.json --complete';

export const USAGE = `usage: node scripts/langpacks.mjs <command> [options]

  status  [--packs a,b] [--catalogue <dir|vX.Y.Z>] [--keys] [--json <file|->] [--check]
          what every language pack still needs against this tree (or the catalogue given)
  todo    [--packs a,b] [--catalogue <dir|vX.Y.Z>] [--out <dir>]
          write the translator's worklists, the catalogue, status.json and AGENT.md
  apply   --worklist <dir> [--packs a,b] [--commit] [--trailer "<line>"]
          fold the answered worklists into the packs (the keys filex dropped leave them), validate, commit locally
  pull    [--packs a,b]
          fast-forward every pack to its nightly remote (the build host's nightly translations)
  release <X.Y.Z> [--packs a,b] [--template <dir>] [--catalogue <dir>] [--keep-changed] [--dry-run] [--trailer "<line>"]
          bring every pack (and the template) to filex X.Y.Z's release catalogue, print the tag and push commands

  The packs: the filex-lang-* checkouts beside this one, FILEX_LANG_PACKS, or --packs.
  Nothing is pushed, signed or tagged. Exit 0 done, 1 pending / held back / red / not pulled, 2 could not run.
  docs/CONTRIBUTING.md -> Translations and language packs.`;

class UsageError extends Error {}

/** argv -> { _: positional, packs, trailers, flags, catalogue, out, worklist, json }. */
export function parseArgs(argv) {
  const out = { _: [], packs: [], trailers: [], flags: new Set() };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    const value = () => {
      const v = argv[i + 1];
      if (v === undefined || v.startsWith('--')) throw new UsageError(`${a} needs a value`);
      i += 1;
      return v;
    };
    switch (a) {
      case '--packs':
        out.packs.push(...value().split(',').filter(Boolean));
        break;
      case '--catalogue':
        out.catalogue = value();
        break;
      case '--out':
        out.out = value();
        break;
      case '--worklist':
        out.worklist = value();
        break;
      case '--template':
        out.template = value();
        break;
      case '--json':
        out.json = value();
        break;
      case '--trailer':
        out.trailers.push(value());
        break;
      case '--keys':
      case '--check':
      case '--commit':
      case '--dry-run':
      case '--keep-changed':
      case '--help':
      case '-h':
        out.flags.add(a);
        break;
      default:
        if (a.startsWith('-') && a !== '-') throw new UsageError(`unknown option ${a}`);
        out._.push(a);
    }
  }
  return out;
}

/* -- small helpers ------------------------------------------------------ */

const sha256 = (text) => crypto.createHash('sha256').update(text).digest('hex');
const num = (n) => Number(n).toLocaleString('en-US');
const slash = (p) => p.split(path.sep).join('/');

function git(dir, args) {
  const r = spawnSync('git', ['-C', dir, ...args], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')} in ${dir}: ${(r.stderr || r.stdout || String(r.error ?? '')).trim()}`);
  return r.stdout;
}

/** true / false, or null when `dir` is not a git checkout. */
function gitDirty(dir) {
  const r = spawnSync('git', ['-C', dir, 'status', '--porcelain'], { encoding: 'utf8' });
  if (r.status !== 0) return null;
  return r.stdout.trim() !== '';
}

function gitShort(dir) {
  const r = spawnSync('git', ['-C', dir, 'rev-parse', '--short=8', 'HEAD'], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : '';
}

/** Run one of the pack's own scripts with this Node. */
function packScript(dir, args) {
  const r = spawnSync(process.execPath, args, { cwd: dir, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  if (r.status !== 0) throw new Error(`node ${args.join(' ')} in ${dir} exited ${r.status}: ${`${r.stdout ?? ''}${r.stderr ?? ''}`.trim().slice(-2000)}`);
  return r.stdout ?? '';
}

/** The pack's `validate` script (its package.json), as `npm run validate` would run it. */
function runValidate(pack) {
  const script = pack.pkg?.scripts?.validate ?? pack.pkg?.scripts?.check ?? DEFAULT_VALIDATE;
  const r = spawnSync(script, { cwd: pack.dir, shell: true, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  return { script, ok: r.status === 0, stdout: (r.stdout ?? '').replace(/\r\n/g, '\n'), stderr: r.stderr ?? '' };
}

/** The files `apply` and `release` may write, as they are now (null: absent). */
function snapshot(dir, langs) {
  const rel = [
    'filex-app.json',
    'package.json',
    'README.md',
    'validate-output.txt',
    ...CATALOGUE_FILES.map((f) => `catalogue/${f}`),
    ...langs.map((t) => `translations/${t}.json`),
  ];
  const snap = new Map();
  for (const r of rel) {
    const p = path.join(dir, r);
    snap.set(r, fs.existsSync(p) ? fs.readFileSync(p) : null);
  }
  return snap;
}

function restore(dir, snap) {
  for (const [r, buf] of snap) {
    const p = path.join(dir, r);
    if (buf === null) fs.rmSync(p, { force: true });
    else fs.writeFileSync(p, buf);
  }
}

function changedFiles(dir, snap) {
  const out = [];
  for (const [r, buf] of snap) {
    const p = path.join(dir, r);
    const now = fs.existsSync(p) ? fs.readFileSync(p) : null;
    const same = buf === null ? now === null : now !== null && buf.equals(now);
    if (!same) out.push(r);
  }
  return out;
}

function commit(dir, files, message) {
  const msgFile = path.join(os.tmpdir(), `filex-langpacks-msg-${process.pid}-${Date.now()}.txt`);
  fs.writeFileSync(msgFile, message);
  try {
    git(dir, ['add', '--', ...files]);
    git(dir, ['commit', '-q', '-F', msgFile]);
    return git(dir, ['rev-parse', '--short=8', 'HEAD']).trim();
  } finally {
    fs.rmSync(msgFile, { force: true });
  }
}

/**
 * Right after the pack's `pack.mjs sync`: every translation of the pack loses
 * the keys filex no longer has (`next` is the catalogue sync just wrote).
 * Through 0.53 every pack's sync kept them, and the packs' own validators
 * refuse them as UNKNOWN, so `apply` and `release` went red and put the pack
 * back on the first key filex dropped (0.53: `tenants.modeOff`, deleted by
 * hand in every pack). Done here, it holds whatever the pack's script does.
 * Returns { <tag>: [dropped keys] }; a file with none is not rewritten.
 */
function dropStale(dir, langs, next) {
  const dropped = {};
  for (const tag of langs) {
    const file = path.join(dir, 'translations', `${tag}.json`);
    const t = readJSON(file);
    dropped[tag] = staleKeys(t, next);
    if (dropped[tag].length) fs.writeFileSync(file, jsonText(withoutStale(t, next)));
  }
  return dropped;
}

/** "dropped 2 key(s) filex no longer has: de a.x, b.y" for what dropStale did, or '' when it dropped nothing. */
function droppedNote(name, dropped) {
  const all = Object.entries(dropped).flatMap(([tag, keys]) => keys.map((k) => `${tag} ${k}`));
  if (!all.length) return '';
  return `${name}: dropped ${all.length} key(s) filex no longer has: ${all.slice(0, 8).join(', ')}${all.length > 8 ? ', ...' : ''}`;
}

/* -- the catalogue a run compares with ---------------------------------- */

function catalogueFromDir(dir, source) {
  const strings = readJSON(path.join(dir, CATALOGUE_FILES[0]));
  const context = readJSON(path.join(dir, CATALOGUE_FILES[1]));
  const version = String(context.filex ?? '');
  const m = /\+([0-9a-f]{7,40})$/.exec(version);
  return { strings, context, version, commit: m ? m[1] : version || 'catalogue', source, dir };
}

async function fetchRelease(version) {
  const v = version.replace(/^v/, '');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `filex-catalogue-${v}-`));
  for (const name of CATALOGUE_FILES) {
    const url = `${RELEASE_ASSETS}/v${v}/${name}`;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`${url}: HTTP ${res.status} - is v${v} published with its catalogue?`);
    const body = await res.text();
    JSON.parse(body);
    fs.writeFileSync(path.join(dir, name), body);
  }
  return dir;
}

/**
 * This tree's catalogue, exactly as scripts/i18n-export.mjs writes it, but
 * naming the commit it was built from: `0.52.0+21e28593` rather than the last
 * release's number, which is all web/package.json knows between releases.
 */
function treeCatalogue() {
  const built = buildCatalogue(ROOT, { where: true });
  const commit = gitShort(ROOT);
  const version = commit ? `${buildVersion(ROOT)}+${commit}` : buildVersion(ROOT);
  return { strings: built.strings, context: { ...built.context, filex: version }, version, commit: commit || 'tree', source: `this tree${commit ? ` at ${commit}` : ''}`, dir: null };
}

async function loadTarget(catalogue) {
  if (!catalogue) return treeCatalogue();
  if (/^v?\d+\.\d+\.\d+$/.test(catalogue)) return catalogueFromDir(await fetchRelease(catalogue), `the ${catalogue.replace(/^v?/, 'v')} release`);
  const dir = path.resolve(catalogue);
  if (!fs.existsSync(path.join(dir, CATALOGUE_FILES[0]))) throw new UsageError(`${catalogue}: no ${CATALOGUE_FILES[0]} there`);
  return catalogueFromDir(dir, slash(dir));
}

function writeCatalogue(dir, target) {
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, CATALOGUE_FILES[0]), jsonText(target.strings));
  fs.writeFileSync(path.join(dir, CATALOGUE_FILES[1]), jsonText(target.context));
}

function loadPacks(opts) {
  const dirs = findPacks(ROOT, { packs: opts.packs });
  if (!dirs.length) {
    throw new UsageError('no language pack found: clone them beside this checkout (filex-lang-<tag>), or pass --packs / FILEX_LANG_PACKS');
  }
  return dirs.map((dir) => {
    if (!isPackDir(dir)) throw new UsageError(`${dir}: not a language pack checkout (filex-app.json, translations/, catalogue/)`);
    return readPack(dir);
  });
}

/* -- status ---------------------------------------------------------------- */

function packStatus(pack, target) {
  const languages = {};
  let pending = 0;
  for (const tag of pack.langs) {
    const d = packDiff({ prev: pack.catalogue, next: target.strings, translation: pack.translations[tag] });
    languages[tag] = { missing: d.missing.length, changed: d.changed.length, removed: d.removed.length, keys: d };
    pending += d.missing.length + d.changed.length;
  }
  return { dir: pack.dir, name: pack.name, version: pack.version, catalogue: pack.catalogueVersion, languages, pending };
}

function statusReport(packs, target) {
  const rows = packs.map((p) => packStatus(p, target));
  return {
    filex: { source: target.source, version: target.version, commit: target.commit, strings: Object.keys(target.strings).length },
    packs: rows,
    pending: rows.reduce((n, r) => n + r.pending, 0),
  };
}

function printStatus(report, target, { keys = false } = {}) {
  console.log(`filex language packs against ${report.filex.source}: ${num(report.filex.strings)} strings (filex ${report.filex.version})`);
  for (const r of report.packs) {
    for (const [tag, l] of Object.entries(r.languages)) {
      const state = l.missing + l.changed === 0 ? 'up to date' : `missing ${l.missing} · changed ${l.changed}`;
      console.log(`  ${r.name.padEnd(10)} ${tag.padEnd(6)} ${String(r.version).padEnd(8)} ${state.padEnd(28)} removed ${String(l.removed).padEnd(4)} catalogue ${r.catalogue || '?'}  ${slash(r.dir)}`);
      if (!keys) continue;
      for (const k of l.keys.missing) console.log(`      missing  ${k}  ${JSON.stringify(target.strings[k])}`);
      for (const k of l.keys.changed) console.log(`      changed  ${k}  ${JSON.stringify(target.strings[k])}`);
      for (const k of l.keys.removed) console.log(`      removed  ${k}`);
    }
  }
  console.log(report.pending ? `${num(report.pending)} string(s) to translate.${keys ? '' : ' --keys lists them.'}` : 'Every pack is up to date.');
}

function writeJsonOut(where, value) {
  if (where === '-') process.stdout.write(jsonText(value));
  else fs.writeFileSync(path.resolve(where), jsonText(value));
}

async function cmdStatus(opts) {
  const target = await loadTarget(opts.catalogue);
  const report = statusReport(loadPacks(opts), target);
  if (opts.json) writeJsonOut(opts.json, report);
  if (opts.json !== '-') printStatus(report, target, { keys: opts.flags.has('--keys') });
  return opts.flags.has('--check') && report.pending ? 1 : 0;
}

/* -- todo ------------------------------------------------------------------ */

function agentGuide({ target, dir, worklists }) {
  const rows = worklists.map((w) => `| ${w.name} | ${w.tag} | ${w.missing} | ${w.changed} | \`${slash(w.file)}\` | ${w.glossary ? `\`${slash(w.glossary)}\`` : '-'} |`);
  return `# Language pack worklist - filex ${target.commit}

The catalogue of filex ${target.version} (${target.source}) has strings these
packs do not translate yet. Translate them here; \`apply\` writes the packs.

| Pack | Language | Missing | Changed | Worklist | Glossary |
|---|---|---|---|---|---|
${rows.join('\n')}

For every worklist:

1. Read the pack's glossary first: its terms, voice and typography decide.
2. Fill \`text\` for every item under \`languages.<tag>.items\` - it is the
   line right after the item's \`key\` - and \`forms\` (just after \`text\`)
   where an item lists \`forms_needed\`. Change nothing else in the file.
3. The rules:
${TRANSLATOR_RULES.map((r) => `   - ${r}`).join('\n')}

Then fold the answers into the packs, validate and commit (locally; nothing is
pushed):

    node "${slash(path.join(ROOT, 'scripts', 'langpacks.mjs'))}" apply --worklist "${slash(dir)}" --commit

\`apply\` checks every answer before it touches a pack - the fixed names, the
dash, the placeholders, the syntax of the item's table - and names the items
to fix; run it again once they are. A pack whose own validators go red is put
back as it was.
`;
}

async function cmdTodo(opts) {
  const target = await loadTarget(opts.catalogue);
  const packs = loadPacks(opts);
  const dir = path.resolve(opts.out ?? path.join(os.tmpdir(), `filex-langpacks-${target.commit}`));
  if (fs.existsSync(dir)) {
    // Answers not applied yet are somebody's work: never written over. A
    // worklist whose pack has moved on (applied, or synced since) is spent.
    const answered = fs
      .readdirSync(dir)
      .filter((f) => f.endsWith('.json') && f !== 'status.json')
      .filter((f) => {
        try {
          const wl = readJSON(path.join(dir, f));
          const has = Object.values(wl.languages ?? {}).some((l) => (l.items ?? []).some((it) => String(it.text ?? '').trim()));
          const now = fs.readFileSync(path.join(wl.pack.dir, 'catalogue', CATALOGUE_FILES[0]), 'utf8');
          return has && sha256(now) === wl.pack.catalogue_sha256;
        } catch {
          return false;
        }
      });
    if (answered.length) throw new UsageError(`${slash(dir)} holds answered worklists (${answered.join(', ')}): apply them, or choose another --out`);
  }
  writeCatalogue(path.join(dir, 'catalogue'), target);
  const report = statusReport(packs, target);
  fs.writeFileSync(path.join(dir, 'status.json'), jsonText(report));
  const worklists = [];
  for (const pack of packs) {
    const languages = {};
    for (const tag of pack.langs) {
      const d = packDiff({ prev: pack.catalogue, next: target.strings, translation: pack.translations[tag] });
      if (!d.missing.length && !d.changed.length) continue;
      const items = worklistItems({ lang: tag, diff: d, prev: pack.catalogue, next: target.strings, context: target.context, translation: pack.translations[tag] });
      languages[tag] = { plural_categories: pluralCategories(tag), missing: d.missing.length, changed: d.changed.length, removed: d.removed, items };
    }
    const file = path.join(dir, `${pack.base}.json`);
    if (!Object.keys(languages).length) {
      fs.rmSync(file, { force: true });
      continue;
    }
    const glossary = fs.existsSync(path.join(pack.dir, 'glossary.md')) ? path.join(pack.dir, 'glossary.md') : null;
    fs.writeFileSync(
      file,
      jsonText({
        worklist: 1,
        filex: { commit: target.commit, version: target.version, strings: Object.keys(target.strings).length },
        pack: { dir: slash(pack.dir), name: pack.name, version: pack.version, catalogue_sha256: sha256(pack.catalogueText) },
        glossary: glossary && slash(glossary),
        rules: TRANSLATOR_RULES,
        languages,
      }),
    );
    for (const [tag, l] of Object.entries(languages)) worklists.push({ name: pack.name, tag, missing: l.missing, changed: l.changed, file, glossary });
  }
  if (worklists.length) fs.writeFileSync(path.join(dir, 'AGENT.md'), agentGuide({ target, dir, worklists }));
  else fs.rmSync(path.join(dir, 'AGENT.md'), { force: true });
  printStatus(report, target);
  if (worklists.length) {
    console.log(`\nWorklists: ${slash(dir)} (AGENT.md says what to do). Then:`);
    console.log(`  node scripts/langpacks.mjs apply --worklist "${slash(dir)}" --commit`);
  }
  return 0;
}

/* -- apply ----------------------------------------------------------------- */

function readWorklists(dir) {
  return fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.json') && f !== 'status.json')
    .sort()
    .map((f) => ({ file: path.join(dir, f), wl: readJSON(path.join(dir, f)) }))
    .filter(({ wl }) => wl && wl.worklist === 1 && wl.pack && wl.languages);
}

function applyOne({ packDir, wl, wlDir, target, vcat, opts }) {
  const say = [];
  if (!isPackDir(packDir)) return { ok: false, say: [`${slash(packDir)}: not a language pack checkout`] };
  const pack = readPack(packDir);
  if (sha256(pack.catalogueText) !== wl.pack.catalogue_sha256) {
    return { ok: false, say: [`${pack.name}: its catalogue moved since this worklist was made - run todo again`] };
  }
  const dirty = gitDirty(pack.dir);
  if (dirty) return { ok: false, say: [`${pack.name}: ${slash(pack.dir)} has uncommitted changes - commit or stash them first`] };
  if (dirty === null && opts.flags.has('--commit')) return { ok: false, say: [`${pack.name}: ${slash(pack.dir)} is not a git checkout, so --commit cannot commit`] };

  // Every answer is checked before a file of the pack is written.
  const problems = [];
  for (const [tag, l] of Object.entries(wl.languages)) {
    if (!pack.translations[tag]) {
      problems.push({ tag, key: '(language)', code: 'LANGUAGE', msg: `the pack has no translations/${tag}.json` });
      continue;
    }
    const merged = mergeItems(withoutStale(syncedTranslation(pack.translations[tag], target.strings), target.strings), l.items ?? [], { next: target.strings });
    const found = merged.problems.length ? merged.problems : itemProblems(tag, merged.translation, l.items ?? [], vcat);
    for (const p of found) problems.push({ tag, ...p });
  }
  if (problems.length) {
    return { ok: false, say: [`${pack.name}: ${problems.length} answer(s) to fix, nothing written:`, ...problems.map((p) => `    ${p.tag} ${p.code.padEnd(12)} ${p.key}: ${p.msg}`)] };
  }

  const snap = snapshot(pack.dir, pack.langs);
  try {
    packScript(pack.dir, ['scripts/pack.mjs', 'sync', '--from', path.join(wlDir, 'catalogue')]);
    // Every language of the pack, not only the worklist's: sync went
    // through all of them.
    const dropped = dropStale(pack.dir, pack.langs, target.strings);
    const note = droppedNote(pack.name, dropped);
    if (note) say.push(note);
    const counts = { translated: 0, total: 0 };
    for (const [tag, l] of Object.entries(wl.languages)) {
      const file = path.join(pack.dir, 'translations', `${tag}.json`);
      const { translation } = mergeItems(readJSON(file), l.items ?? [], { next: target.strings });
      fs.writeFileSync(file, jsonText(translation));
      const cov = checkLanguage(tag, translation, vcat, { complete: false }).coverage;
      counts.translated += cov?.translated ?? 0;
      counts.total += cov?.total ?? 0;
    }
    packScript(pack.dir, ['scripts/pack.mjs', 'build']);
    const v = runValidate(pack);
    if (!v.ok) throw new Error(`\`${v.script}\` failed:\n${`${v.stdout}${v.stderr}`.trim().split('\n').slice(-40).join('\n')}`);
    if (snap.get('validate-output.txt') !== null) fs.writeFileSync(path.join(pack.dir, 'validate-output.txt'), v.stdout);
    const files = changedFiles(pack.dir, snap);
    if (!opts.flags.has('--commit')) {
      say.push(`${pack.name}: written and validated, not committed (--commit): ${files.join(', ')}`);
      return { ok: true, say };
    }
    // What the commit says it did: the worklist's own diff, read again from
    // the pack as it was (its first language - a pack carries one), and the
    // keys it dropped from that translation.
    const first = Object.keys(wl.languages)[0];
    const diff = { ...packDiff({ prev: pack.catalogue, next: target.strings, translation: pack.translations[first] }), removed: dropped[first] ?? [] };
    const sha = commit(
      pack.dir,
      files,
      applyMessage({
        commit: target.commit,
        version: target.version,
        translated: counts.translated,
        total: counts.total,
        diff,
        worklist: path.basename(wlDir),
        validate: v.script,
        trailers: opts.trailers,
      }),
    );
    say.push(`${pack.name}: committed ${sha} (${num(counts.translated)} of ${num(counts.total)}), not pushed`);
    return { ok: true, say };
  } catch (e) {
    restore(pack.dir, snap);
    return { ok: false, say: [`${pack.name}: put back as it was - ${e.message}`] };
  }
}

async function cmdApply(opts) {
  if (!opts.worklist) throw new UsageError('apply needs --worklist <dir> (what `todo` wrote)');
  const wlDir = path.resolve(opts.worklist);
  const catDir = path.join(wlDir, 'catalogue');
  if (!fs.existsSync(path.join(catDir, CATALOGUE_FILES[0]))) throw new UsageError(`${slash(wlDir)}: no catalogue/ - not a worklist directory`);
  const target = catalogueFromDir(catDir, slash(catDir));
  useCompiler(checkoutRoots(ROOT));
  const vcat = loadValidatorCatalogue({ catalogue: catDir });
  const lists = readWorklists(wlDir);
  if (!lists.length) {
    console.log(`${slash(wlDir)}: no worklist - every pack was up to date.`);
    return 0;
  }
  const override = new Map(opts.packs.map((p) => [path.basename(path.resolve(p)), path.resolve(p)]));
  let ok = true;
  for (const { wl } of lists) {
    const packDir = override.get(path.basename(wl.pack.dir)) ?? path.resolve(wl.pack.dir);
    const r = applyOne({ packDir, wl, wlDir, target, vcat, opts });
    for (const line of r.say) console.log(line);
    ok &&= r.ok;
  }
  return ok ? 0 : 1;
}

/* -- release --------------------------------------------------------------- */

/** The remotes of a checkout; none when it is not one. */
function remotesOf(dir) {
  const r = spawnSync('git', ['-C', dir, 'remote'], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.split('\n').map((s) => s.trim()).filter(Boolean) : [];
}

function pushCommands(pack, tag, message) {
  const remotes = remotesOf(pack.dir);
  const remote = publishRemote(remotes);
  const branch = git(pack.dir, ['rev-parse', '--abbrev-ref', 'HEAD']).trim();
  const at = `git -C "${slash(pack.dir)}"`;
  // The build host's checkout takes the release commit too: the next
  // nightly translation goes on from it, and the next `pull` stays a
  // fast-forward.
  const back = remotes.includes(NIGHTLY_REMOTE) ? [`  ${at} push ${NIGHTLY_REMOTE} ${branch}`] : [];
  if (!remote) return [`  ${pack.name} has no remote to publish to: a local pack, committed and not tagged.`, ...back];
  return [`  ${at} tag -s ${tag} -m "${message}"`, `  ${at} push ${remote} ${branch}`, `  ${at} push ${remote} ${tag}`, ...back];
}

/**
 * Where a pack stands against its `nightly` remote, fetched first:
 * { branch, ref, ahead, behind }, { error } when it cannot be read, null when
 * the pack has no such remote.
 */
function nightlyState(pack) {
  if (!remotesOf(pack.dir).includes(NIGHTLY_REMOTE)) return null;
  const branch = git(pack.dir, ['rev-parse', '--abbrev-ref', 'HEAD']).trim();
  const f = spawnSync('git', ['-C', pack.dir, 'fetch', '--quiet', NIGHTLY_REMOTE], { encoding: 'utf8' });
  if (f.status !== 0) return { branch, error: `git fetch ${NIGHTLY_REMOTE} failed: ${(f.stderr || '').trim().split('\n').pop()}` };
  const ref = `${NIGHTLY_REMOTE}/${branch}`;
  const c = spawnSync('git', ['-C', pack.dir, 'rev-list', '--left-right', '--count', `HEAD...${ref}`], { encoding: 'utf8' });
  if (c.status !== 0) return { branch, ref, error: `${ref} is not there` };
  const [ahead, behind] = c.stdout.trim().split(/\s+/).map(Number);
  return { branch, ref, ahead, behind };
}

/**
 * The commit an earlier `release` made for this filex version, if any: run
 * twice, the step would otherwise move the version a second time.
 */
function releasedFor(pack, version) {
  const r = spawnSync('git', ['-C', pack.dir, 'log', '-50', '--format=%h %s'], { encoding: 'utf8' });
  if (r.status !== 0) return '';
  return r.stdout.split('\n').find((l) => l.includes(`: for filex ${version}, the catalogue from the release tag`)) ?? '';
}

function releaseOne({ pack, target, vcat, version, opts }) {
  const dirty = gitDirty(pack.dir);
  if (dirty) return { ok: false, say: [`${pack.name}: held back - ${slash(pack.dir)} has uncommitted changes`] };
  if (dirty === null && !opts.flags.has('--dry-run')) return { ok: false, say: [`${pack.name}: held back - not a git checkout`] };
  const done = releasedFor(pack, version);
  if (done) return { ok: true, say: [`${pack.name}: already released for filex ${version} (${done.trim()}) - nothing to do`] };
  // The nightly translations, made on the build host, come first: a pack
  // released without them would ship strings left untranslated, and its
  // next pull would no longer be a fast-forward.
  const nightly = dirty === null ? null : nightlyState(pack);
  const note = [];
  if (nightly?.error) note.push(`${pack.name}: the ${NIGHTLY_REMOTE} remote could not be read (${nightly.error}) - released from what is here`);
  else if (nightly?.behind) {
    return {
      ok: false,
      say: [
        `${pack.name}: held back at ${pack.version} - ${nightly.ref} has ${nightly.behind} nightly commit(s) this checkout lacks. Bring them home first:`,
        '    node scripts/langpacks.mjs pull',
      ],
    };
  }
  const diffs = {};
  const held = [];
  for (const tag of pack.langs) {
    const d = packDiff({ prev: pack.catalogue, next: target.strings, translation: pack.translations[tag] });
    diffs[tag] = d;
    if (d.missing.length) held.push(`${tag}: ${d.missing.length} key(s) not translated (${d.missing.slice(0, 6).join(', ')}${d.missing.length > 6 ? ', ...' : ''})`);
    if (d.changed.length && !opts.flags.has('--keep-changed')) {
      held.push(`${tag}: ${d.changed.length} key(s) translated for earlier English (${d.changed.slice(0, 6).join(', ')}${d.changed.length > 6 ? ', ...' : ''})`);
    }
  }
  if (held.length) {
    return {
      ok: false,
      say: [
        `${pack.name}: held back at ${pack.version} - translate first (todo --catalogue v${version}, then apply), or --keep-changed for changed English:`,
        ...held.map((h) => `    ${h}`),
      ],
    };
  }

  const snap = snapshot(pack.dir, pack.langs);
  try {
    packScript(pack.dir, ['scripts/pack.mjs', 'sync', '--from', target.dir]);
    // The keys the release dropped leave the translations here too: a
    // release can drop a key the nightly apply never saw (it lands after the
    // last night, or a night with nothing to translate makes no worklist).
    const dropped = dropStale(pack.dir, pack.langs, target.strings);
    const manifestPath = path.join(pack.dir, 'filex-app.json');
    const manifest = readJSON(manifestPath);
    const next = bumpPatch(manifest.version);
    manifest.version = next;
    fs.writeFileSync(manifestPath, jsonText(manifest));
    if (pack.pkg && typeof pack.pkg.version === 'string') {
      fs.writeFileSync(path.join(pack.dir, 'package.json'), jsonText({ ...pack.pkg, version: next }));
    }
    packScript(pack.dir, ['scripts/pack.mjs', 'build']);

    const say = [...note];
    const droppedSay = droppedNote(pack.name, dropped);
    if (droppedSay) say.push(droppedSay);
    const readmePath = path.join(pack.dir, 'README.md');
    if (fs.existsSync(readmePath)) {
      // Measured on the manifest, as `validate.mjs filex-app.json` measures
      // it: only the non-empty values of keys filex has are there.
      const built = readJSON(manifestPath).ui_locales ?? {};
      const validators = { errors: 0, warnings: 0 };
      const languages = pack.langs.map((tag) => {
        const strings = built[tag] ?? {};
        const r = checkLanguage(tag, strings, vcat, { complete: true });
        validators.errors += r.errors.length;
        validators.warnings += r.warnings.length;
        return { tag, translated: r.coverage.translated, total: r.coverage.total, percent: r.coverage.percent, extra: extraForms(tag, strings, target.strings) };
      });
      const block = statusBlock({ version: next, filex: version, total: Object.keys(target.strings).length, counts: tableCounts(target.context), languages, validators });
      const readme = replaceStatusBlock(fs.readFileSync(readmePath, 'utf8'), block);
      if (readme === null) say.push(`${pack.name}: README.md has no ${STATUS_OPEN} block - its version line is yours to update`);
      else fs.writeFileSync(readmePath, readme);
    }

    const v = runValidate(pack);
    if (!v.ok) throw new Error(`\`${v.script}\` failed:\n${`${v.stdout}${v.stderr}`.trim().split('\n').slice(-40).join('\n')}`);
    if (snap.get('validate-output.txt') !== null) fs.writeFileSync(path.join(pack.dir, 'validate-output.txt'), v.stdout);
    const files = changedFiles(pack.dir, snap);
    const pkgName = pack.pkg?.name ?? pack.base;
    const tag = `v${next}`;
    if (opts.flags.has('--dry-run')) {
      restore(pack.dir, snap);
      say.push(`${pack.name}: ${pack.version} -> ${next} would be committed (--dry-run, nothing kept): ${files.join(', ')}`);
      return { ok: true, say };
    }
    const first = pack.langs[0];
    const sha = commit(
      pack.dir,
      files,
      releaseMessage({
        pkgName,
        version: next,
        filex: version,
        diff: { ...diffs[first], removed: dropped[first] ?? [] },
        kept: diffs[first].changed,
        validate: v.script,
        trailers: opts.trailers,
      }),
    );
    say.push(`${pack.name}: ${pack.version} -> ${next}, committed ${sha}. To publish:`);
    say.push(...pushCommands(pack, tag, `${pkgName} ${next} (for filex ${version})`));
    return { ok: true, say };
  } catch (e) {
    restore(pack.dir, snap);
    return { ok: false, say: [`${pack.name}: put back as it was - ${e.message}`] };
  }
}

/**
 * The template follows the release too: its catalogue is what a new
 * translator starts from, and it went unrefreshed through 0.52 because no
 * checklist named it. Its example language keeps exactly the catalogue's
 * keys and their plural forms: a key filex dropped leaves it, filled or
 * empty, the same rule as a pack's (dropStale); no version, no tag.
 */
function releaseTemplate({ dir, target, version, opts }) {
  const pack = readPack(dir);
  if (pack.catalogueVersion === version) return { ok: true, say: [`template: already follows filex ${version}`] };
  const dirty = gitDirty(dir);
  if (dirty) return { ok: false, say: [`template: held back - ${slash(dir)} has uncommitted changes`] };
  if (dirty === null && !opts.flags.has('--dry-run')) return { ok: false, say: ['template: held back - not a git checkout'] };
  const snap = snapshot(dir, pack.langs);
  try {
    packScript(dir, ['scripts/pack.mjs', 'sync', '--from', target.dir]);
    dropStale(dir, pack.langs, target.strings);
    packScript(dir, ['scripts/pack.mjs', 'build']);
    const readmePath = path.join(dir, 'README.md');
    if (fs.existsSync(readmePath)) {
      fs.writeFileSync(readmePath, templateReadme(fs.readFileSync(readmePath, 'utf8'), version, Object.keys(target.strings).length));
    }
    const v = runValidate(pack);
    if (!v.ok) throw new Error(`\`${v.script}\` failed:\n${`${v.stdout}${v.stderr}`.trim().split('\n').slice(-40).join('\n')}`);
    const files = changedFiles(dir, snap);
    if (opts.flags.has('--dry-run')) {
      restore(dir, snap);
      return { ok: true, say: [`template: would follow filex ${version} (--dry-run, nothing kept): ${files.join(', ')}`] };
    }
    const lines = [
      `The catalogue follows filex v${version}`,
      '',
      wrap(
        `The catalogue is filex ${version}'s release asset (scripts/langpacks.mjs release), the example language keeps exactly its keys, and the README names v${version}. \`${v.script}\` passed.`,
      ),
    ];
    if (opts.trailers.length) lines.push('', ...opts.trailers);
    const sha = commit(dir, files, `${lines.join('\n')}\n`);
    const remote = publishRemote(remotesOf(dir));
    const branch = git(dir, ['rev-parse', '--abbrev-ref', 'HEAD']).trim();
    return {
      ok: true,
      say: [`template: follows filex ${version}, committed ${sha}.${remote ? ' To publish (no tag):' : ''}`, ...(remote ? [`  git -C "${slash(dir)}" push ${remote} ${branch}`] : [])],
    };
  } catch (e) {
    restore(dir, snap);
    return { ok: false, say: [`template: put back as it was - ${e.message}`] };
  }
}

async function cmdRelease(opts) {
  const version = String(opts._[1] ?? '').replace(/^v/, '');
  if (!/^\d+\.\d+\.\d+$/.test(version)) throw new UsageError('release needs the filex version: release X.Y.Z');
  const target = await loadTarget(opts.catalogue ?? `v${version}`);
  if (target.version !== version) {
    throw new UsageError(`the catalogue says filex ${target.version || '?'}, not ${version}: give the release's own (its assets, or --catalogue <dir>)`);
  }
  useCompiler(checkoutRoots(ROOT));
  const vcat = loadValidatorCatalogue({ catalogue: target.dir });
  console.log(`filex ${version}: ${num(Object.keys(target.strings).length)} strings (${target.source})`);
  let ok = true;
  for (const pack of loadPacks(opts)) {
    const r = releaseOne({ pack, target, vcat, version, opts });
    for (const line of r.say) console.log(line);
    ok &&= r.ok;
  }
  // The template: --template, or the one beside the checkout when the packs
  // were found there too (an explicit --packs list names no template).
  const template = opts.template ? path.resolve(opts.template) : opts.packs.length ? null : findTemplate(ROOT);
  if (template) {
    if (!isPackDir(template)) throw new UsageError(`${opts.template}: not a language pack template checkout`);
    const r = releaseTemplate({ dir: template, target, version, opts });
    for (const line of r.say) console.log(line);
    ok &&= r.ok;
  }
  return ok ? 0 : 1;
}

/* -- pull ------------------------------------------------------------------ */

/**
 * The nightly translations, home: every pack with a `nightly` remote is
 * fetched from it and fast-forwarded. Never a merge commit, never over
 * changes of the checkout's own: those packs are named, with the command.
 */
function cmdPull(opts) {
  let ok = true;
  for (const pack of loadPacks(opts)) {
    const at = `git -C "${slash(pack.dir)}"`;
    const s = nightlyState(pack);
    if (!s) {
      console.log(`${pack.name}: no ${NIGHTLY_REMOTE} remote - nothing to pull (${at} remote add ${NIGHTLY_REMOTE} <the build host's checkout>)`);
      continue;
    }
    if (s.error) {
      console.log(`${pack.name}: ${s.error}`);
      ok = false;
      continue;
    }
    const step = pullStep(s);
    if (step === 'up to date') {
      console.log(`${pack.name}: up to date with ${s.ref}${s.ahead ? ` (${s.ahead} commit(s) of its own, for ${at} push ${NIGHTLY_REMOTE} ${s.branch})` : ''}`);
      continue;
    }
    if (step === 'diverged') {
      console.log(`${pack.name}: ${s.ahead} commit(s) here and ${s.behind} on ${s.ref} - both moved, merge it by hand: ${at} merge ${s.ref}`);
      ok = false;
      continue;
    }
    if (gitDirty(pack.dir)) {
      console.log(`${pack.name}: ${slash(pack.dir)} has uncommitted changes - commit or stash them, then pull again`);
      ok = false;
      continue;
    }
    const m = spawnSync('git', ['-C', pack.dir, 'merge', '--ff-only', '--quiet', s.ref], { encoding: 'utf8' });
    if (m.status !== 0) {
      console.log(`${pack.name}: ${at} merge --ff-only ${s.ref} failed: ${(m.stderr || m.stdout || '').trim().split('\n').pop()}`);
      ok = false;
      continue;
    }
    console.log(`${pack.name}: ${s.behind} nightly commit(s) from ${s.ref}, now at ${gitShort(pack.dir)}`);
  }
  return ok ? 0 : 1;
}

/* -- main ------------------------------------------------------------------ */

export async function main(argv) {
  let opts;
  try {
    opts = parseArgs(argv);
  } catch (e) {
    console.error(`langpacks: ${e.message}\n\n${USAGE}`);
    return 2;
  }
  const cmd = opts._[0];
  if (!cmd || opts.flags.has('--help') || opts.flags.has('-h')) {
    console.log(USAGE);
    return cmd || opts.flags.size ? 0 : 2;
  }
  const commands = { status: cmdStatus, todo: cmdTodo, apply: cmdApply, pull: cmdPull, release: cmdRelease };
  if (!commands[cmd]) {
    console.error(`langpacks: unknown command ${cmd}\n\n${USAGE}`);
    return 2;
  }
  try {
    return await commands[cmd](opts);
  } catch (e) {
    console.error(`langpacks: ${e.message}`);
    return 2;
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await main(process.argv.slice(2));
}
