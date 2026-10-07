#!/usr/bin/env node
// Writes the note a release train runs from: the train rule, the cut, what
// is on the train (main since the last tag, up to the cut), what arrived after
// it, the merge queue, the steps and the closing line.
//
//   node scripts/train/new-train.mjs X.Y.Z [options]
//
//   --date YYYY-MM-DD   the train's day in Istanbul (default: today there)
//   --onto BRANCH       the release branch (default: main)
//   --out FILE          where to write it (default: <git dir>/filex-train/RELEASE-X.Y.Z.md,
//                       beside the release record - never in the working tree,
//                       whose first gate is "clean")
//   --stdout            print it instead
//   --repo DIR          the checkout (default: this repository)
//
// Run it at the cut, or again after it: before 10:00 the list is main as it
// is now, and the note says so. The merge queue reads the note's ```queue
// block (merge-queue.mjs --queue <the note>).
//
// ⚠ Why this exists (#179, #169): the rule - one train a day, cut at 10:00,
// what comes after the cut waits, an exploitable hole ships alone - was
// written down on 2026-10-06 after 0.51 and 0.52 had each taken on work after
// they left (0.51: three hours; 0.52: four restarts in a day). A rule only in
// a document is a rule nobody reads at 09:55. The note puts it, and the exact
// list of what is on board, in front of whoever cuts the release. The rule is
// read from docs/CONTRIBUTING.md each time, never copied.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { SEMVER, bumpKind, newestTag } from '../release/checks.mjs';
import { git, gitOut, revParse } from '../release/engine.mjs';
import { usageOf } from './cli.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, '..', '..');
const TEMPLATE = path.join(HERE, 'RELEASE-TEMPLATE.md');

/** The cut, in Istanbul time; Istanbul is UTC+3 all year (no daylight saving since 2016). */
export const CUT_LOCAL = '10:00';
export const ISTANBUL_OFFSET_H = 3;

/** The train's day in Istanbul for a moment (default now): YYYY-MM-DD. */
export function istanbulDate(now = new Date()) {
  return new Date(now.getTime() + ISTANBUL_OFFSET_H * 3600_000).toISOString().slice(0, 10);
}

/** The cut of a day as a Date (10:00 Istanbul = 07:00 UTC). */
export function cutOf(date) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) throw new Error(`--date ${date}: YYYY-MM-DD`);
  const [h, m] = CUT_LOCAL.split(':').map(Number);
  return new Date(`${date}T${String(h - ISTANBUL_OFFSET_H).padStart(2, '0')}:${String(m).padStart(2, '0')}:00Z`);
}

/**
 * The train rule as docs/CONTRIBUTING.md writes it: from "**When:" up to the
 * paragraph that starts "**Cut it with". Throws when it is not there - a note
 * without its rule is the failure this note exists to prevent.
 */
export function trainRule(contributing) {
  const s = String(contributing).replace(/\r\n/g, '\n');
  const start = s.indexOf('**When:');
  const end = s.indexOf('**Cut it with', start);
  if (start < 0 || end < 0) throw new Error('docs/CONTRIBUTING.md no longer has the train rule ("**When:" ... "**Cut it with"), Release process');
  return s.slice(start, end).trim();
}

/** A first-parent commit as the note lists it: its branch when it is a merge, and the #numbers it names. */
export function describeCommit(sha, subject) {
  const merge = /^Merge (?:branch '([^']+)'|([^\s(:]+))/.exec(subject);
  const branch = merge ? (merge[1] ?? merge[2]) : null;
  const refs = [...new Set([...subject.matchAll(/#(\d+)/g)].map((m) => `#${m[1]}`))];
  const text = subject.length > 160 ? `${subject.slice(0, 157)}...` : subject;
  return { sha, branch, refs, line: `- \`${sha.slice(0, 10)}\` ${branch ? `**${branch}** - ` : ''}${text}` };
}

/** Fills {{name}} in the template; a name with no value is an error, not a blank. */
export function fill(template, values) {
  const out = template.replace(/\{\{(\w+)\}\}/g, (_, k) => {
    if (!(k in values)) throw new Error(`the template names {{${k}}}, which nothing fills`);
    return String(values[k]);
  });
  return out;
}

function firstParent(repo, range) {
  const r = git(repo, 'log', '--first-parent', '--format=%H%x09%s', range);
  if (r.status !== 0) throw new Error(`git log ${range}: ${(r.stderr || r.stdout).trim()}`);
  return r.stdout
    .split('\n')
    .filter(Boolean)
    .map((l) => {
      const i = l.indexOf('\t');
      return describeCommit(l.slice(0, i), l.slice(i + 1));
    });
}

/** Everything the note says, from the repository. */
export function trainValues({ repo, version, date, onto = 'main', now = new Date() }) {
  if (!SEMVER.test(version)) throw new Error(`"${version}" is not X.Y.Z`);
  const tags = gitOut(repo, 'tag', '--list', 'v*').split('\n').map((t) => t.trim()).filter(Boolean);
  const prevTag = newestTag(tags.filter((t) => t !== `v${version}`));
  if (!prevTag) throw new Error('no earlier release tag (vX.Y.Z) to count from');
  const prevSha = revParse(repo, `${prevTag}^{commit}`);
  const cut = cutOf(date);
  const before = now < cut;
  const tip = revParse(repo, onto);
  if (!tip) throw new Error(`no branch ${onto}`);
  // A date git reads the same everywhere: "YYYY-MM-DD HH:MM:SS +0000".
  const gitDate = `${cut.toISOString().slice(0, 19).replace('T', ' ')} +0000`;
  const atCut = before ? tip : (git(repo, 'rev-list', '-1', '--first-parent', `--before=${gitDate}`, onto).stdout.trim() || prevSha);
  const onTrain = firstParent(repo, `${prevSha}..${atCut}`);
  const afterCut = before ? [] : firstParent(repo, `${atCut}..${tip}`);
  const kind = bumpKind(version, prevTag);
  const refs = [...new Set(onTrain.flatMap((c) => c.refs))];
  return {
    version,
    tag: `v${version}`,
    date,
    cut: `${date} ${CUT_LOCAL}`,
    cutUtc: cut.toISOString().slice(11, 16),
    cutState: before ? `not yet: this is ${onto} now, run this again after the cut` : `${onto} as it stood then`,
    cutSha: `\`${atCut.slice(0, 10)}\``,
    prevTag,
    prevSha: `\`${prevSha.slice(0, 10)}\``,
    profile: kind === 'patch' ? 'patch' : 'minor',
    kind:
      kind === 'patch'
        ? 'a patch (X.Y.Z+1): an exploitable hole in a released version ships on a patch of its own and rides no minor'
        : `a ${kind ?? 'minor'} release, today's train`,
    onTrain: onTrain.length ? onTrain.map((c) => c.line).join('\n') : `_Nothing: ${onto} holds nothing ${prevTag} does not. No train today._`,
    afterCut: before ? '_The cut has not happened yet._' : afterCut.length ? afterCut.map((c) => c.line).join('\n') : '_Nothing._',
    tasks: refs.length ? refs.join(', ') : '(none named in the merges - list them by hand)',
  };
}

function parseArgs(argv) {
  const o = { version: null, date: null, onto: 'main', out: null, stdout: false, repo: REPO };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`${a} needs a value`);
      return v;
    };
    if (a === '--date') o.date = next();
    else if (a === '--onto') o.onto = next();
    else if (a === '--out') o.out = next();
    else if (a === '--stdout') o.stdout = true;
    else if (a === '--repo') o.repo = path.resolve(next());
    else if (a === '-h' || a === '--help') o.help = true;
    else if (a.startsWith('-')) throw new Error(`unknown option ${a}`);
    else if (!o.version) o.version = a.replace(/^v/, '');
    else throw new Error(`one version, not "${a}" too`);
  }
  if (!o.help && !o.version) throw new Error('the release, as X.Y.Z');
  return o;
}

function main(argv) {
  let o;
  try {
    o = parseArgs(argv);
  } catch (e) {
    console.error(`new-train: ${e.message}\n\n${usageOf(import.meta.url)}`);
    return 2;
  }
  if (o.help) {
    console.log(usageOf(import.meta.url));
    return 0;
  }
  const date = o.date ?? istanbulDate();
  const values = trainValues({ repo: o.repo, version: o.version, date, onto: o.onto });
  values.trainRule = trainRule(fs.readFileSync(path.join(o.repo, 'docs', 'CONTRIBUTING.md'), 'utf8'));
  const text = fill(fs.readFileSync(TEMPLATE, 'utf8'), values);
  if (o.stdout) {
    process.stdout.write(text);
    return 0;
  }
  const out = path.resolve(o.out ?? path.join(gitOut(o.repo, 'rev-parse', '--absolute-git-dir'), 'filex-train', `RELEASE-${o.version}.md`));
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, text);
  console.log(`new-train: ${out}`);
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
