#!/usr/bin/env node
// Merges CHANGELOG.md's [Unreleased] the way a person does when two branches
// both added entries: by Keep a Changelog section, every entry of both sides,
// each heading once.
//
//   node scripts/train/changelog-merge.mjs --git [--repo DIR] [CHANGELOG.md]
//        during a conflicted merge: reads the three versions git holds (base,
//        ours, theirs), writes the merged file and `git add`s it. Exit 1 when
//        it refuses (it says why; the file is left as git left it).
//   node scripts/train/changelog-merge.mjs CHANGELOG.md
//        a file with conflict markers and no index to read (the markers'
//        sides are rebuilt; with diff3 markers their base too).
//   node scripts/train/changelog-merge.mjs --normalize CHANGELOG.md
//        no conflict: one heading per kind, in order (a clean merge can still
//        leave `### Changed` twice).
//   node scripts/train/changelog-merge.mjs --check CHANGELOG.md
//        exit 1 when [Unreleased] holds a heading twice or a conflict marker.
//
// ⚠ Why this exists (#179): every train merges a dozen branches, and nearly
// every one of them touched [Unreleased]. The 0.52 and 0.53 rounds resolved
// those conflicts by hand or with a scratch script that cut the conflict
// blocks apart by their markers. A block git opens in the MIDDLE of a section
// (both sides appended to the end of it) has no heading of its own; the
// scratch moved the section's heading into the block and left the entries
// above the block behind under the previous heading. This parses whole
// sections of the three versions instead, so where git happened to cut
// changes nothing.
//
// ⚠ And why a clean merge is not enough (lesson #706): at 0.48 five branches
// merged without a single conflict and left [Unreleased] with three
// `### Changed` and two `### Security`; the release notes are built from it
// and published the mix. --normalize is what the merge queue runs after every
// merge, conflict or not.
//
// What it merges, per section, entry by entry (an entry is one list item with
// its wrapped lines, or one paragraph):
//   - every entry of ours, in its order;
//   - minus an entry theirs removed from the base (and ours left alone);
//   - plus every entry theirs added, placed after the entry it followed in
//     theirs (after any entries ours added there, so ours comes first).
// An entry theirs EDITED is one removed and one added, so it lands in place.
// An entry both sides changed (or one changed and the other deleted) is not
// guessed at: the merge is refused, and a person decides.
// Outside [Unreleased] - the released history - a side may change what the
// other left alone; if both changed it, the merge is refused.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** Keep a Changelog's order, with this project's own `Upgrade notes` first (lesson #706). */
export const ORDER = ['Upgrade notes', 'Added', 'Changed', 'Deprecated', 'Removed', 'Fixed', 'Security'];

const UNRELEASED = /^## \[Unreleased\][^\n]*\n/m;
const MARKER_START = /^<{7}(?: |$)/;
const MARKER_BASE = /^\|{7}(?: |$)/;
const MARKER_MID = /^={7}$/;
const MARKER_END = /^>{7}(?: |$)/;

// ── the file ────────────────────────────────────────────────────────────────

/**
 * { head, body, tail }: `head` ends with the `## [Unreleased]` line, `body`
 * runs up to the next `## [` heading, `tail` is the rest. null when the file
 * has no [Unreleased] (the same reading scripts/release/checks.mjs's
 * unreleasedBody makes).
 */
export function splitUnreleased(text) {
  const s = String(text).replace(/\r\n/g, '\n');
  const m = UNRELEASED.exec(s);
  if (!m) return null;
  const start = m.index + m[0].length;
  const rest = s.slice(start);
  const end = rest.search(/^## \[/m);
  const body = end < 0 ? rest : rest.slice(0, end);
  return { head: s.slice(0, start), body, tail: end < 0 ? '' : rest.slice(end) };
}

const isMarker = (line) => /^[-*+] /.test(line) || /^\d+\. /.test(line);

/**
 * The entries of a run of lines: a list item at column 0 with everything
 * indented under it, or a paragraph. Each is { text, key, gap } - `gap` says
 * a blank line stood before it (kept when written back), `key` is what two
 * entries are compared by (trailing spaces ignored).
 */
export function blocksOf(lines) {
  const out = [];
  let cur = null;
  let blank = false;
  for (const line of lines) {
    if (line.trim() === '') {
      if (cur) cur.lines.push(line);
      blank = true;
      continue;
    }
    const col0 = !/^\s/.test(line);
    if (!cur || (col0 && isMarker(line)) || (col0 && blank)) {
      cur = { lines: [line], gap: blank && out.length > 0 };
      out.push(cur);
    } else {
      cur.lines.push(line);
    }
    blank = false;
  }
  return out.map((b) => {
    while (b.lines.length && b.lines.at(-1).trim() === '') b.lines.pop();
    const text = b.lines.join('\n');
    return { text, key: b.lines.map((l) => l.replace(/\s+$/, '')).join('\n'), gap: b.gap };
  });
}

/**
 * [Unreleased]'s body as { lead, sections }: `lead` the entries before the
 * first `###`, `sections` [{ title, blocks }] - a heading that appears twice
 * becomes one section, its entries in the order they came.
 */
export function parseBody(body) {
  const lines = String(body).replace(/\r\n/g, '\n').split('\n');
  const lead = [];
  const sections = [];
  const byTitle = new Map();
  let target = lead;
  for (const line of lines) {
    const h = /^### (.+?)\s*$/.exec(line);
    if (h) {
      const title = h[1];
      if (!byTitle.has(title)) {
        const sec = { title, parts: [] };
        byTitle.set(title, sec);
        sections.push(sec);
      }
      // A heading seen again opens another part of the same section; its
      // first entry follows the last one before it as a list item would,
      // not as a new paragraph (blocksOf gives a part's first entry no gap).
      target = [];
      byTitle.get(title).parts.push(target);
      continue;
    }
    target.push(line);
  }
  return {
    lead: blocksOf(lead),
    sections: sections.map((s) => ({ title: s.title, blocks: s.parts.flatMap((p) => blocksOf(p)) })),
  };
}

const rank = (title) => {
  const i = ORDER.indexOf(title);
  return i < 0 ? ORDER.length : i;
};

/** Sections in Keep a Changelog order; unknown headings after, as they came. */
export function orderSections(sections) {
  return sections
    .map((s, i) => ({ s, i }))
    .sort((a, b) => rank(a.s.title) - rank(b.s.title) || a.i - b.i)
    .map((x) => x.s);
}

// A paragraph always stands apart from what comes before it; a list item does
// when it did in the file (a loose list stays loose, a tight one tight).
function renderBlocks(blocks) {
  return blocks.map((b, i) => (i === 0 ? b.text : `${b.gap || !isMarker(b.text) ? '\n' : ''}\n${b.text}`)).join('');
}

/** A body written back: a blank line after the heading, one between sections. */
export function renderBody({ lead, sections }) {
  const parts = [];
  if (lead.length) parts.push(renderBlocks(lead));
  for (const s of orderSections(sections)) if (s.blocks.length) parts.push(`### ${s.title}\n\n${renderBlocks(s.blocks)}`);
  return parts.length ? `\n${parts.join('\n\n')}\n\n` : '\n';
}

// ── the merge ───────────────────────────────────────────────────────────────

const firstLine = (b) => b.text.split('\n')[0].slice(0, 100);

/**
 * Entry-wise three-way merge of one section. `base` is null when it is not
 * known (markers without diff3): then nothing counts as removed. Returns
 * { blocks, conflicts }.
 */
export function mergeBlocks(base, ours, theirs) {
  const baseKeys = new Set((base ?? []).map((b) => b.key));
  const oursKeys = new Set(ours.map((b) => b.key));
  const theirsKeys = new Set(theirs.map((b) => b.key));
  const conflicts = [];

  // An entry of the base that neither side kept: fine when both simply
  // dropped it, a conflict when either side put something new in its place.
  if (base) {
    const newAfter = (side, sideKeys, prevKey) => {
      const start = prevKey === null ? 0 : side.findIndex((b) => b.key === prevKey) + 1;
      if (prevKey !== null && start === 0) return false;
      const b = side[start];
      return !!b && !baseKeys.has(b.key) && (sideKeys === oursKeys ? !theirsKeys.has(b.key) : !oursKeys.has(b.key));
    };
    base.forEach((b, i) => {
      if (oursKeys.has(b.key) || theirsKeys.has(b.key)) return;
      const prev = i === 0 ? null : base[i - 1].key;
      if (newAfter(ours, oursKeys, prev) || newAfter(theirs, theirsKeys, prev)) {
        conflicts.push(`both sides changed the entry "${firstLine(b)}"`);
      }
    });
  }

  const result = ours.filter((b) => !(baseKeys.has(b.key) && !theirsKeys.has(b.key)));
  theirs.forEach((t, i) => {
    if (baseKeys.has(t.key) || oursKeys.has(t.key)) return;
    let pos = 0;
    for (let j = i - 1; j >= 0; j--) {
      const at = result.findIndex((r) => r.key === theirs[j].key);
      if (at >= 0) {
        pos = at + 1;
        break;
      }
    }
    // After what ours added at the same place: ours first, then theirs.
    while (pos < result.length && !baseKeys.has(result[pos].key) && !theirsKeys.has(result[pos].key)) pos++;
    result.splice(pos, 0, t);
  });
  return { blocks: result, conflicts };
}

/** The same part of the file on both sides, or the side that changed it; null when both did. */
function pick(base, ours, theirs) {
  if (ours === theirs) return ours;
  if (base === null) return null;
  if (theirs === base) return ours;
  if (ours === base) return theirs;
  return null;
}

/**
 * Merges three versions of a changelog. `base` may be null (unknown).
 * Returns { ok: true, text } or { ok: false, why }.
 */
export function mergeChangelog({ base = null, ours, theirs }) {
  const o = splitUnreleased(ours);
  const t = splitUnreleased(theirs);
  if (!o || !t) return { ok: false, why: 'a side has no ## [Unreleased] heading' };
  const b = base === null ? null : splitUnreleased(base);
  if (base !== null && !b) return { ok: false, why: 'the base has no ## [Unreleased] heading' };

  const head = pick(b?.head ?? null, o.head, t.head);
  if (head === null) return { ok: false, why: 'both sides changed the text above [Unreleased]' };
  const tail = pick(b?.tail ?? null, o.tail, t.tail);
  if (tail === null) return { ok: false, why: 'both sides changed the released history below [Unreleased]' };

  const pb = b ? parseBody(b.body) : null;
  const po = parseBody(o.body);
  const pt = parseBody(t.body);
  const conflicts = [];
  const lead = mergeBlocks(pb ? pb.lead : null, po.lead, pt.lead);
  conflicts.push(...lead.conflicts.map((c) => `before the first heading: ${c}`));

  const titles = [];
  for (const s of [...po.sections, ...pt.sections, ...(pb?.sections ?? [])]) if (!titles.includes(s.title)) titles.push(s.title);
  const of = (p, title) => p?.sections.find((s) => s.title === title)?.blocks;
  const sections = [];
  for (const title of titles) {
    const m = mergeBlocks(pb ? (of(pb, title) ?? []) : null, of(po, title) ?? [], of(pt, title) ?? []);
    conflicts.push(...m.conflicts.map((c) => `### ${title}: ${c}`));
    sections.push({ title, blocks: m.blocks });
  }
  if (conflicts.length) return { ok: false, why: conflicts.join('\n') };
  return { ok: true, text: head + renderBody({ lead: lead.blocks, sections }) + tail };
}

/** One heading per kind, in order: the merge of a file with itself. */
export function normalizeChangelog(text) {
  const s = String(text).replace(/\r\n/g, '\n');
  const r = mergeChangelog({ base: s, ours: s, theirs: s });
  if (!r.ok) return { text: s, changed: false };
  return { text: r.text, changed: r.text !== s };
}

/** What is wrong with a changelog's [Unreleased]: conflict markers, a heading twice. */
export function unreleasedProblems(text) {
  const s = String(text).replace(/\r\n/g, '\n');
  const problems = [];
  if (s.split('\n').some((l) => MARKER_START.test(l) || MARKER_END.test(l))) problems.push('it still holds conflict markers');
  const parts = splitUnreleased(s);
  if (!parts) return [...problems, 'it has no ## [Unreleased] heading'];
  const seen = new Map();
  for (const m of parts.body.matchAll(/^### (.+?)\s*$/gm)) seen.set(m[1], (seen.get(m[1]) ?? 0) + 1);
  for (const [title, n] of seen) if (n > 1) problems.push(`[Unreleased] has "### ${title}" ${n} times`);
  return problems;
}

/**
 * The sides of a file holding conflict markers: { ours, theirs, base } where
 * base is null unless every block carried a diff3 base part.
 */
export function sidesOf(text) {
  const lines = String(text).replace(/\r\n/g, '\n').split('\n');
  const ours = [];
  const theirs = [];
  const base = [];
  let state = 'out';
  let blocks = 0;
  let withBase = 0;
  for (const line of lines) {
    if (state === 'out' && MARKER_START.test(line)) {
      state = 'ours';
      blocks++;
      continue;
    }
    if (state === 'ours' && MARKER_BASE.test(line)) {
      state = 'base';
      withBase++;
      continue;
    }
    if ((state === 'ours' || state === 'base') && MARKER_MID.test(line)) {
      state = 'theirs';
      continue;
    }
    if (state === 'theirs' && MARKER_END.test(line)) {
      state = 'out';
      continue;
    }
    if (state === 'out') {
      ours.push(line);
      theirs.push(line);
      base.push(line);
    } else if (state === 'ours') ours.push(line);
    else if (state === 'base') base.push(line);
    else theirs.push(line);
  }
  if (state !== 'out') throw new Error('a conflict block is not closed');
  return { ours: ours.join('\n'), theirs: theirs.join('\n'), base: blocks > 0 && withBase === blocks ? base.join('\n') : null, blocks };
}

/** Resolves a file that holds conflict markers (no index to read). */
export function resolveMarkers(text) {
  const sides = sidesOf(text);
  if (sides.blocks === 0) return { ok: true, text: String(text), blocks: 0 };
  const r = mergeChangelog(sides);
  return r.ok ? { ...r, blocks: sides.blocks } : r;
}

// ── git ─────────────────────────────────────────────────────────────────────

function gitShow(repo, spec) {
  const r = spawnSync('git', ['-C', repo, 'show', spec], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, windowsHide: true });
  return r.status === 0 ? r.stdout : null;
}

/**
 * Resolves `file` (relative to the repository) in a conflicted merge from the
 * index's three stages, writes it and stages it. Returns { ok, why? }.
 */
export function resolveInGit(repo, file = 'CHANGELOG.md') {
  const rel = file.split(path.sep).join('/');
  const ours = gitShow(repo, `:2:${rel}`);
  const theirs = gitShow(repo, `:3:${rel}`);
  if (ours === null || theirs === null) return { ok: false, why: `${rel} has no "ours" and "theirs" in the index - is it in conflict?` };
  const base = gitShow(repo, `:1:${rel}`);
  const r = mergeChangelog({ base, ours, theirs });
  if (!r.ok) return r;
  const n = normalizeChangelog(r.text);
  fs.writeFileSync(path.join(repo, rel), n.text);
  const add = spawnSync('git', ['-C', repo, 'add', '--', rel], { encoding: 'utf8', windowsHide: true });
  if (add.status !== 0) return { ok: false, why: `git add ${rel}: ${(add.stderr || add.stdout).trim()}` };
  return { ok: true };
}

// ── command line ────────────────────────────────────────────────────────────

function main(argv) {
  const args = argv.slice(2);
  const flag = (f) => {
    const i = args.indexOf(f);
    if (i < 0) return false;
    args.splice(i, 1);
    return true;
  };
  const value = (f) => {
    const i = args.indexOf(f);
    if (i < 0) return null;
    const v = args[i + 1];
    args.splice(i, 2);
    return v;
  };
  const useGit = flag('--git');
  const normalize = flag('--normalize');
  const check = flag('--check');
  const repo = value('--repo') ?? process.cwd();
  const file = args[0] ?? 'CHANGELOG.md';
  if (useGit) {
    const r = resolveInGit(repo, file);
    if (!r.ok) {
      console.error(`changelog-merge: not merged:\n${r.why}`);
      return 1;
    }
    console.log(`changelog-merge: ${file} merged by section and staged`);
    return 0;
  }
  const full = path.resolve(repo, file);
  const text = fs.readFileSync(full, 'utf8');
  if (check) {
    const p = unreleasedProblems(text);
    for (const l of p) console.error(`changelog-merge: ${l}`);
    return p.length ? 1 : 0;
  }
  if (normalize) {
    const n = normalizeChangelog(text);
    if (n.changed) fs.writeFileSync(full, n.text);
    console.log(`changelog-merge: ${n.changed ? 'one heading per kind now' : 'already one heading per kind'}`);
    return 0;
  }
  const r = resolveMarkers(text);
  if (!r.ok) {
    console.error(`changelog-merge: not merged:\n${r.why}`);
    return 1;
  }
  fs.writeFileSync(full, normalizeChangelog(r.text).text);
  console.log(`changelog-merge: ${r.blocks} conflict block(s) merged by section`);
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv));
}
