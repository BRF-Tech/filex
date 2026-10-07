#!/usr/bin/env node
// The published screenshots: what is on filex.sh/shots, and every page that
// shows one. e2e/shots/README.md is the whole story; in short:
//
//   pnpm shots                                  take what changed, compare pixels, stage
//   node scripts/shots-site.mjs upload          send the staged files to the site (maintainers)
//   node scripts/shots-site.mjs accept --looked the reviewed run becomes the published set:
//                                               manifest + every README/docs/site link
//   node scripts/shots-site.mjs verify [--live] every link is the current file [and answers]
//   node scripts/shots-site.mjs relink [--write] point every link at the current file
//   node scripts/shots-site.mjs adopt <dir> [--rev <rev>] [--rewrite] [--platform <os>]
//                                               stage a folder of pictures as published
//                                               (how the 0.52.0 set moved out of the repo;
//                                               --platform records where it was taken)
//
// ⚠⚠ The order is the point: a picture is PUBLISHED before any page links
// it. `accept` and `relink --write` read every new URL back from the site and
// refuse while one does not answer with exactly the bytes the manifest names -
// the public repository's README must never show a broken image, and it is
// exported from whatever the private one says.
//
// ⚠ `--looked` is a person's word, not a flag to make the command pass: the
// owner's rule is that no picture is committed that nobody looked at. `pnpm
// shots` prints a contact sheet with only the changed and new pictures, each
// beside the published one and a map of what moved; that is what to look at.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { pngSize } from './lib/png.mjs';
import { findShotScripts } from './lib/shot-scripts.mjs';
import {
  ARTIFACTS_REL,
  MANIFEST_REL,
  PUBLISH_REL,
  REVIEW_REL,
  acceptRefusal,
  environmentNote,
  isPictureName,
  manifestProblems,
  nextManifest,
  publishedName,
  publishedReferences,
  publishedUrl,
  readManifest,
  relinkRepo,
  sceneOfName,
  sha256,
  shotSets,
  verifyPublished,
  writeManifest,
} from './lib/shots-site.mjs';
import { findBash } from './release/engine.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SHOTS_DIR = path.join(REPO, 'e2e', 'shots');
const MANIFEST = path.join(REPO, MANIFEST_REL);
const abs = (rel) => path.join(REPO, ...rel.split('/'));

const argv = process.argv.slice(2);
const cmd = argv[0];
const has = (name) => argv.includes(`--${name}`);
const value = (name) => {
  const i = argv.indexOf(`--${name}`);
  return i > -1 && argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[i + 1] : undefined;
};
const positional = argv.slice(1).filter((a, i, all) => !a.startsWith('--') && !(i > 0 && ['--rev', '--platform'].includes(all[i - 1])));

const say = (msg) => console.log(`[shots-site] ${msg}`);
class Refusal extends Error {}

function git(args, opts = {}) {
  const r = spawnSync('git', ['-C', REPO, '-c', 'core.quotepath=off', ...args], { maxBuffer: 512 * 1024 * 1024, ...opts });
  if (r.status !== 0) throw new Refusal(`git ${args.join(' ')} failed: ${String(r.stderr || r.error?.message || '').trim()}`);
  return r.stdout;
}

function listFiles(dir, keep) {
  const out = [];
  const walk = (d, rel) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const r = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) walk(path.join(d, e.name), r);
      else if (keep(e.name)) out.push(r);
    }
  };
  if (fs.existsSync(dir)) walk(dir, '');
  return out.sort();
}

function readReview() {
  const file = abs(REVIEW_REL);
  return fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : null;
}

function printList(title, lines, max = 40) {
  if (!lines.length) return;
  console.log(`\n${title}`);
  for (const l of lines.slice(0, max)) console.log(`  ${l}`);
  if (lines.length > max) console.log(`  … and ${lines.length - max} more`);
}

// ── verify ───────────────────────────────────────────────────────────────────
async function verify() {
  const m = readManifest(MANIFEST);
  const problems = manifestProblems(m, { scripts: findShotScripts(SHOTS_DIR).scripts });
  const files = relinkRepo(REPO, m);
  const stale = files.flatMap((f) => f.changes.map((c) => `${f.file}:${c.line}  ${c.from}  ->  ${c.to}`));
  const unresolved = files.flatMap((f) => f.unresolved.map((u) => `${f.file}:${u.line}  ${u.ref} - ${u.why}`));
  printList(`${MANIFEST_REL} is not well-formed:`, problems);
  printList('Links that do not show the current published file (node scripts/shots-site.mjs relink --write):', stale);
  printList('Links to a picture the manifest does not hold:', unresolved);
  if (problems.length || stale.length || unresolved.length) throw new Refusal('the published pictures and the pages that show them disagree');

  const refs = publishedReferences(REPO, m);
  say(`${refs.length} picture link(s) in ${new Set(refs.map((r) => r.file)).size} file(s), each the current published file`);
  if (!has('live')) return;
  const unique = [...new Map(refs.map((r) => [r.url, { url: r.url, sha256: m.pictures[r.name].sha256 }])).values()];
  const res = await verifyPublished(unique);
  const bad = res.filter((r) => !r.ok).map((r) => `${r.url} - ${r.why}`);
  printList('Not published as the manifest says:', bad);
  if (bad.length) throw new Refusal(`${bad.length} of ${unique.length} linked picture(s) are not live - publish first: node scripts/shots-site.mjs upload`);
  say(`all ${unique.length} answer 200 with the bytes the manifest names`);
}

// ── relink ───────────────────────────────────────────────────────────────────
async function relink() {
  const m = readManifest(MANIFEST);
  const files = relinkRepo(REPO, m);
  const unresolved = files.flatMap((f) => f.unresolved.map((u) => `${f.file}:${u.line}  ${u.ref} - ${u.why}`));
  printList('Links to a picture the manifest does not hold (fix the page; nothing was written):', unresolved);
  if (unresolved.length) throw new Refusal(`${unresolved.length} link(s) cannot be pointed anywhere`);
  const changed = files.filter((f) => f.changes.length);
  for (const f of changed) printList(`${f.file}:`, f.changes.map((c) => `${c.line}: ${c.from}  ->  ${c.to}`), 12);
  if (!changed.length) {
    say('every link already shows the current published file');
    return;
  }
  if (!has('write')) throw new Refusal(`${changed.length} file(s) would change - run with --write`);
  if (!has('offline')) {
    const targets = [...new Set(changed.flatMap((f) => f.changes.map((c) => c.to)))].map((url) => {
      const name = Object.keys(m.pictures).find((n) => publishedUrl(m.base, n, m.pictures[n].sha256) === url);
      return { url, sha256: m.pictures[name].sha256 };
    });
    const bad = (await verifyPublished(targets)).filter((r) => !r.ok).map((r) => `${r.url} - ${r.why}`);
    printList('Not published yet:', bad);
    if (bad.length) throw new Refusal('a page may only link a picture that is already published - node scripts/shots-site.mjs upload, then this again');
  }
  for (const f of changed) fs.writeFileSync(path.join(REPO, f.file), f.text);
  say(`${changed.length} file(s) rewritten: ${changed.map((f) => f.file).join(', ')}`);
}

// ── adopt ────────────────────────────────────────────────────────────────────
function picturesIn(src, rev) {
  if (rev) {
    const names = git(['ls-tree', '-r', '-z', '--name-only', rev, '--', src], { encoding: 'utf8' })
      .split('\0')
      .filter((n) => n.endsWith('.png'));
    const prefix = `${src.replace(/\/+$/, '')}/`;
    return names.map((full) => ({
      name: full.slice(prefix.length),
      read: () => git(['cat-file', 'blob', `${rev}:${full}`]),
      taken: () => git(['log', '-1', '--format=%cI', rev, '--', full], { encoding: 'utf8' }).trim(),
    }));
  }
  const dir = path.resolve(REPO, src);
  if (!fs.existsSync(dir)) throw new Refusal(`${src} does not exist`);
  return listFiles(dir, (n) => n.endsWith('.png')).map((name) => {
    const file = path.join(dir, name);
    return {
      name,
      read: () => fs.readFileSync(file),
      taken: () => {
        const r = spawnSync('git', ['-C', REPO, 'log', '-1', '--format=%cI', '--', file], { encoding: 'utf8' });
        return (r.status === 0 && r.stdout.trim()) || fs.statSync(file).mtime.toISOString();
      },
    };
  });
}

async function adopt() {
  const src = positional[0];
  if (!src) throw new Refusal('adopt <folder> [--rev <rev>] [--rewrite] [--platform <os>] - which folder of pictures?');
  const rev = value('rev');
  // Where the folder's pictures were taken, as process.platform names it: the
  // accept rule reads it (fonts are the system's). Said by the person who
  // knows - a picture does not carry it.
  const platform = value('platform');
  if (has('platform') && !/^[a-z0-9]+$/.test(platform ?? '')) throw new Refusal('--platform <os>: as process.platform names it (linux, win32, darwin)');
  const items = picturesIn(src, rev);
  if (!items.length) throw new Refusal(`no .png under ${rev ? `${rev}:` : ''}${src}`);
  const bad = items.filter((i) => !isPictureName(i.name)).map((i) => i.name);
  if (bad.length) throw new Refusal(`not usable as picture names: ${bad.join(', ')}`);

  let m = readManifest(MANIFEST, { missingOk: has('rewrite') });
  if (has('rewrite')) {
    const scripts = findShotScripts(SHOTS_DIR).scripts;
    const sets = shotSets(SHOTS_DIR, scripts);
    const pictures = {};
    const orphans = [];
    for (const i of items) {
      const buf = i.read();
      const size = pngSize(buf);
      const scene = sceneOfName(sets, i.name);
      if (!size) throw new Refusal(`${i.name} is not a PNG`);
      if (!scene) {
        orphans.push(i.name);
        continue;
      }
      pictures[i.name] = { sha256: sha256(buf), width: size.width, height: size.height, bytes: buf.length, scene, taken: i.taken() };
    }
    printList('No shot script writes these any more - left out of the manifest:', orphans);
    m = { ...m, pictures, scenes: Object.fromEntries(scripts.map((s) => [s, { digest: null }])), ...(platform ? { platform } : {}) };
    writeManifest(MANIFEST, m);
    say(`${MANIFEST_REL}: ${Object.keys(pictures).length} picture(s) from ${rev ? `${rev}:` : ''}${src}${platform ? `, taken on ${platform}` : ''}`);
  } else if (platform && m.platform !== platform) {
    m = { ...m, platform };
    writeManifest(MANIFEST, m);
    say(`${MANIFEST_REL}: the published set was taken on ${platform}`);
  }

  const byName = new Map(items.map((i) => [i.name, i]));
  const missing = [];
  const mismatched = [];
  fs.rmSync(abs(PUBLISH_REL), { recursive: true, force: true });
  let staged = 0;
  for (const [name, p] of Object.entries(m.pictures)) {
    const item = byName.get(name);
    if (!item) {
      missing.push(name);
      continue;
    }
    const buf = item.read();
    if (sha256(buf) !== p.sha256) {
      mismatched.push(name);
      continue;
    }
    const out = abs(`${PUBLISH_REL}/${publishedName(name, p.sha256)}`);
    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.writeFileSync(out, buf);
    staged++;
  }
  printList(`In ${MANIFEST_REL}, not in ${src}:`, missing);
  printList(`Different bytes than ${MANIFEST_REL} names (rerun with --rewrite to adopt the folder as it is):`, mismatched);
  printList(`In ${src}, not in ${MANIFEST_REL} (not staged):`, [...byName.keys()].filter((n) => !m.pictures[n]));
  if (missing.length || mismatched.length) throw new Refusal('the folder is not the set the manifest names');
  say(`${staged} picture(s) staged in ${PUBLISH_REL} - next: node scripts/shots-site.mjs upload`);
}

// ── upload ───────────────────────────────────────────────────────────────────
async function upload() {
  const m = readManifest(MANIFEST, { missingOk: true });
  const dir = abs(PUBLISH_REL);
  const rels = listFiles(dir, (n) => n.endsWith('.png'));
  if (!rels.length) throw new Refusal(`nothing is staged in ${PUBLISH_REL} - pnpm shots (or adopt) first`);
  const files = rels.map((rel) => {
    const sha = sha256(fs.readFileSync(path.join(dir, rel)));
    const short = /\.([0-9a-f]{12})\.png$/.exec(rel)?.[1];
    if (!short || !sha.startsWith(short)) throw new Refusal(`${PUBLISH_REL}/${rel}: its name does not carry its content's hash - stage it again`);
    return { rel, sha };
  });

  const script = abs('scripts/shots-upload.sh');
  if (!fs.existsSync(script)) {
    throw new Refusal(
      'uploading is the maintainers\' step and scripts/shots-upload.sh is not in this checkout (the public tree does not carry ' +
        'it). Everything up to here - the pictures, the review, the staged files - is yours to look at.',
    );
  }
  const bash = findBash();
  if (!bash) throw new Refusal("no bash to run scripts/shots-upload.sh with (Git for Windows' bash.exe was not found)");
  // ⚠ A path relative to the repository: GNU tar reads `G:/...` as a remote
  // host named G, and Git Bash would hand it one.
  const r = spawnSync(bash, ['scripts/shots-upload.sh', PUBLISH_REL, ...(has('dry-run') ? ['--dry-run'] : [])], {
    cwd: REPO,
    stdio: 'inherit',
    env: { ...process.env, FILEX_SHOTS_BASE: m.base },
  });
  if (r.status !== 0) throw new Refusal(`scripts/shots-upload.sh exited ${r.status ?? r.error?.message}`);
  if (has('dry-run')) return;

  const res = await verifyPublished(files.map((f) => ({ url: `${m.base}${f.rel}`, sha256: f.sha })));
  const bad = res.filter((x) => !x.ok).map((x) => `${x.url} - ${x.why}`);
  printList('Uploaded, but not answering as it should:', bad);
  if (bad.length) throw new Refusal(`${bad.length} of ${files.length} file(s) are not live`);
  say(`${files.length} file(s) live under ${m.base} - next: node scripts/shots-site.mjs accept --looked (after looking)`);
}

// ── accept ───────────────────────────────────────────────────────────────────
async function accept() {
  const review = readReview();
  const m = readManifest(MANIFEST, { missingOk: true });
  const why = acceptRefusal(m, review);
  if (why) throw new Refusal(why);
  const note = environmentNote(m, review);
  if (note) say(`⚠ ${note}`);

  const pics = Object.entries(review.pictures ?? {});
  const fresh = pics.filter(([, p]) => p.status === 'new' || p.status === 'changed');
  const toLook = [...fresh.map(([n, p]) => `${p.status.padEnd(8)} ${n}`), ...(review.removed ?? []).map((n) => `removed  ${n}`)];
  if (!has('looked')) {
    printList('To look at:', toLook);
    throw new Refusal(
      `${toLook.length} picture(s) to look at first: ${ARTIFACTS_REL}/contact-sheet.html - every one in English, current, ` +
        'nothing covering it. Then: node scripts/shots-site.mjs accept --looked',
    );
  }
  const head = spawnSync('git', ['-C', REPO, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).stdout.trim();
  if (review.head && head && review.head !== head) say(`⚠ the run was taken at ${review.head.slice(0, 10)}, the tree is at ${head.slice(0, 10)} now`);

  const next = nextManifest(m, review);
  const targets = fresh.map(([name, p]) => ({ url: publishedUrl(next.base, name, p.sha256), sha256: p.sha256 }));
  const bad = (await verifyPublished(targets)).filter((r) => !r.ok).map((r) => `${r.url} - ${r.why}`);
  printList('Not published yet:', bad);
  if (bad.length) throw new Refusal('a picture becomes current only once it is published - node scripts/shots-site.mjs upload, then this again');

  const files = relinkRepo(REPO, next);
  const unresolved = files.flatMap((f) => f.unresolved.map((u) => `${f.file}:${u.line}  ${u.ref} - ${u.why}`));
  printList('Still shown, but no script takes them any more (change these pages; nothing was written):', unresolved);
  if (unresolved.length) throw new Refusal(`${unresolved.length} link(s) to a picture that is gone`);

  writeManifest(MANIFEST, next);
  const changed = files.filter((f) => f.changes.length);
  for (const f of changed) fs.writeFileSync(path.join(REPO, f.file), f.text);
  say(
    `published set: ${fresh.length} new or changed, ${(review.removed ?? []).length} removed, ` +
      `${pics.filter(([, p]) => p.status === 'same').length} unchanged; ${changed.length} page(s) relinked`,
  );
  say(`commit: git add ${[MANIFEST_REL, ...changed.map((f) => f.file)].join(' ')}`);
}

// ── main ─────────────────────────────────────────────────────────────────────
const COMMANDS = { verify, relink, adopt, upload, accept };
try {
  if (!COMMANDS[cmd]) {
    console.log(fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1, 15).join('\n').replace(/^\/\/ ?/gm, ''));
    process.exit(cmd && cmd !== '--help' ? 2 : 0);
  }
  await COMMANDS[cmd]();
} catch (err) {
  console.error(`\n[shots-site] ✗ ${err instanceof Refusal ? err.message : err.stack}`);
  process.exitCode = 1;
}
