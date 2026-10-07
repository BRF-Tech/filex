// The pure half of `pnpm release`: every judgement the release makes that can
// be made from text alone. Nothing in this file runs a process, reads the
// disk or touches the network, so each rule is unit-tested on its own
// (web/tests/deploy/releaseChecks.test.ts) and the CLI around it
// (scripts/release.mjs) is only plumbing.
//
// Each rule below exists because a release went out without it. The incident
// is written next to the rule, so nobody deletes one as "too strict" without
// reading what it cost the last time.

import { createHash } from 'node:crypto';

import { headingLines } from '../../docs-site/scripts/markdown-headings.mjs';

// ── versions ────────────────────────────────────────────────────────────────

export const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

/** "0.45.0" → { major, minor, patch }, or null for anything else. */
export function parseVersion(v) {
  const m = SEMVER.exec(String(v ?? '').replace(/^v/, ''));
  return m ? { major: Number(m[1]), minor: Number(m[2]), patch: Number(m[3]) } : null;
}

/** Negative, zero or positive, like a sort comparator. */
export function compareVersions(a, b) {
  const x = parseVersion(a);
  const y = parseVersion(b);
  if (!x || !y) throw new Error(`not a version: ${!x ? a : b}`);
  return x.major - y.major || x.minor - y.minor || x.patch - y.patch;
}

/** The newest `vX.Y.Z` among tag names, or null. Other tags are ignored. */
export function newestTag(tags) {
  const releases = tags.filter((t) => /^v\d+\.\d+\.\d+$/.test(t) && parseVersion(t));
  releases.sort((a, b) => compareVersions(b, a));
  return releases[0] ?? null;
}

/** What kind of step `version` is from `previous`. */
export function bumpKind(version, previous) {
  const v = parseVersion(version);
  const p = parseVersion(previous);
  if (!v || !p) return null;
  if (v.major !== p.major) return 'major';
  if (v.minor !== p.minor) return 'minor';
  return 'patch';
}

/**
 * Everything wrong with releasing `version` after `previous` (a tag name or
 * null for the first release). Empty means the number is acceptable.
 */
export function versionProblems(version, previous) {
  const v = parseVersion(version);
  if (!v || String(version).startsWith('v')) {
    return [`"${version}" is not a release number: write it as X.Y.Z (no "v", no suffix)`];
  }
  const out = [];
  // ⚠ Lesson #468. The Microsoft Store package carries 0.x as
  // 1.0.(minor*100+patch).0, so a 1.0.x release would sort BELOW every 0.x the
  // Store has already accepted, and the Store only takes a higher number.
  // 0.x goes straight to 1.1.0 (the maintainer's decision, 2026-09-24).
  if (v.major === 1 && v.minor === 0) {
    out.push(`${version}: there is no 1.0.x — the Store version of 1.0.x sorts below every 0.x already published. The step after 0.x is 1.1.0.`);
  }
  if (v.major === 0 && v.patch > 99) {
    out.push(`${version}: a 0.x patch number cannot pass 99 — the Store version packs it as minor*100+patch.`);
  }
  if (previous) {
    if (!parseVersion(previous)) {
      out.push(`the previous release "${previous}" is not a version tag`);
    } else if (compareVersions(version, previous) <= 0) {
      out.push(`${version} is not newer than the last release ${previous}`);
    }
  }
  return out;
}

// ── CHANGELOG.md ────────────────────────────────────────────────────────────

const UNRELEASED = /^## \[Unreleased\][^\n]*\n/m;

/**
 * The body under `## [Unreleased]` (up to the next `## [`), or null when the
 * heading is missing. HTML comments do not count as content.
 */
export function unreleasedBody(changelog) {
  const m = UNRELEASED.exec(changelog);
  if (!m) return null;
  const rest = changelog.slice(m.index + m[0].length);
  const end = rest.search(/^## \[/m);
  return end < 0 ? rest : rest.slice(0, end);
}

/** True when the body says something (not only blank lines and comments). */
export function hasContent(body) {
  return String(body ?? '').replace(/<!--[\s\S]*?-->/g, '').trim().length > 0;
}

/** True when CHANGELOG.md already has a `## [version]` section. */
export function hasSection(changelog, version) {
  return new RegExp(`^## \\[${String(version).replace(/\./g, '\\.')}\\]`, 'm').test(changelog);
}

/** The `### ` group names under one version's section. */
export function sectionGroups(changelog, version) {
  const start = changelog.search(new RegExp(`^## \\[${String(version).replace(/\./g, '\\.')}\\]`, 'm'));
  if (start < 0) return [];
  const rest = changelog.slice(start).split('\n').slice(1).join('\n');
  const end = rest.search(/^## \[/m);
  const body = end < 0 ? rest : rest.slice(0, end);
  return [...body.matchAll(/^### (.+)$/gm)].map((m) => m[1].trim());
}

/**
 * Moves the `[Unreleased]` body under a new dated `## [version] - date`
 * heading and leaves an EMPTY `## [Unreleased]` above it (lesson #392: the
 * version sync reads the dated heading, so this is the first write of a
 * release, before the package bump).
 */
export function dateChangelog(changelog, version, date) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) throw new Error(`not a date: ${date}`);
  if (hasSection(changelog, version)) throw new Error(`CHANGELOG.md already has a [${version}] section`);
  const m = UNRELEASED.exec(changelog);
  if (!m) throw new Error('CHANGELOG.md has no "## [Unreleased]" heading');
  if (!hasContent(unreleasedBody(changelog))) {
    throw new Error('the [Unreleased] section is empty — a release with nothing to say has no reason to exist');
  }
  const at = m.index + m[0].length;
  return `${changelog.slice(0, at)}\n## [${version}] - ${date}\n${changelog.slice(at)}`;
}

/** Local calendar date, YYYY-MM-DD — the date the maintainer is living in. */
export function localDate(d = new Date()) {
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// ── package.json ────────────────────────────────────────────────────────────

/** The top-level "version" of a package.json text, or null. */
export function packageVersion(jsonText) {
  try {
    const v = JSON.parse(jsonText).version;
    return typeof v === 'string' ? v : null;
  } catch {
    return null;
  }
}

/**
 * Rewrites the top-level "version" field and nothing else — not the
 * indentation, not the key order, not a trailing newline. Throws when the
 * field is not exactly one two-space-indented line, rather than guess.
 */
export function setPackageVersion(jsonText, version) {
  const re = /^( {2}"version":\s*)"[^"\n]*"/gm;
  const hits = jsonText.match(re) ?? [];
  if (hits.length !== 1) {
    throw new Error(`expected one top-level "version" line, found ${hits.length}`);
  }
  const out = jsonText.replace(re, (_m, lead) => `${lead}"${version}"`);
  if (packageVersion(out) !== version) throw new Error('rewriting "version" did not produce valid JSON');
  return out;
}

/**
 * The workspace package directories named by pnpm-workspace.yaml, expanded
 * against a list of directories that hold a package.json. Only `dir` and
 * `dir/*` patterns are understood; anything else is an error, not a guess —
 * a pattern this cannot read is a package whose version would silently stay
 * behind.
 */
export function workspacePackages(workspaceYaml, packageDirs) {
  const block = /^packages:\s*\n((?:\s+-\s+.*\n?)+)/m.exec(workspaceYaml);
  if (!block) throw new Error('pnpm-workspace.yaml has no packages: list');
  const patterns = [...block[1].matchAll(/^\s+-\s+["']?([^"'\n]+?)["']?\s*$/gm)].map((m) => m[1]);
  const out = new Set();
  for (const p of patterns) {
    if (p.startsWith('!')) throw new Error(`pnpm-workspace.yaml pattern "${p}" is not supported here`);
    if (/^[^*?[\]{}]+$/.test(p)) {
      if (packageDirs.includes(p)) out.add(p);
      else throw new Error(`pnpm-workspace.yaml names "${p}", which has no package.json`);
    } else if (/^[^*?[\]{}]+\/\*$/.test(p)) {
      const base = p.slice(0, -2);
      for (const d of packageDirs) if (d.startsWith(`${base}/`) && !d.slice(base.length + 1).includes('/')) out.add(d);
    } else {
      throw new Error(`pnpm-workspace.yaml pattern "${p}" is not supported here`);
    }
  }
  return [...out].sort();
}

// ── the public export ───────────────────────────────────────────────────────

/**
 * Sorts the files the export is about to DELETE from the public tree.
 *
 *   deletedInSource — removed from the private tree during this release: the
 *                     export is only following suit.
 *   withheld        — still in the private tree, but the export no longer
 *                     publishes it (a file newly added to the private list).
 *                     Intentional at most once, so a person confirms it.
 *   unexplained     — neither. The export is removing something for a reason
 *                     nobody wrote down; on 2026-09-24 that was 3,117 files.
 */
export function classifyDeletions(exportDeleted, { sourceDeleted, sourceFiles }) {
  const del = new Set(sourceDeleted);
  const has = new Set(sourceFiles);
  const out = { deletedInSource: [], withheld: [], unexplained: [] };
  for (const p of exportDeleted) {
    if (del.has(p)) out.deletedInSource.push(p);
    else if (has.has(p)) out.withheld.push(p);
    else out.unexplained.push(p);
  }
  return out;
}

/**
 * Lines of `git grep -n` output that name a private host after the allowed
 * contact addresses are taken out. `forbid` is a list of RegExps.
 *
 * ⚠ The two contact addresses must stay REACHABLE in the public tree (a
 * security report sent to example.com goes nowhere), which is why the export
 * keeps them and why they are the only allowed spelling.
 */
export function privateHostLines(grepLines, { allow = [], forbid = [] }) {
  return grepLines.filter((line) => {
    let rest = line;
    for (const a of allow) rest = rest.split(a).join('');
    return forbid.some((re) => new RegExp(re.source, re.flags.replace('g', '')).test(rest));
  });
}

const NUL = String.fromCharCode(0);
const REGULAR = new Set(['100644', '100755']);

/**
 * `git ls-tree -r -z` or `git ls-files -s -z` output as a Map of path → mode.
 * Both put the mode first and the path after a tab; -z means no quoting.
 */
export function gitModes(z) {
  const out = new Map();
  for (const rec of String(z ?? '').split(NUL)) {
    const tab = rec.indexOf('\t');
    if (tab < 0) continue;
    out.set(rec.slice(tab + 1), rec.slice(0, tab).split(' ')[0]);
  }
  return out;
}

/**
 * The files whose executable bit the public tree lost (`lost`: 100755 in the
 * private tree, 100644 in the public index) or gained (`gained`). Only regular
 * files both trees hold are compared: .github/workflows is the public
 * checkout's own, a withheld file is not in the public tree, a symlink is
 * neither mode.
 *
 * ⚠ Lesson #1108: on Windows, where git cannot see the bit, the export's
 * `git add -A` staged every new file 100644, so e2e/realenv/run.sh - a script
 * docs/CONTRIBUTING.md tells a reader to run as it is - reached the public
 * repository unrunnable, and nothing said so.
 */
export function execBitDrift(privateTreeZ, publicIndexZ) {
  const priv = gitModes(privateTreeZ);
  const lost = [];
  const gained = [];
  let checked = 0;
  for (const [p, mode] of gitModes(publicIndexZ)) {
    const want = priv.get(p);
    if (!want || !REGULAR.has(mode) || !REGULAR.has(want)) continue;
    checked++;
    if (want === '100755' && mode !== '100755') lost.push(p);
    else if (want !== '100755' && mode === '100755') gained.push(p);
  }
  return { lost: lost.sort(), gained: gained.sort(), checked };
}

/**
 * GitHub closing keywords in a commit message. On the public repo a commit
 * that says "Fixes #26" closes issue 26 the moment it reaches main — lesson
 * #163: v0.41.2's export commit closed an issue its reporter had not yet
 * confirmed. "(#26)" or "issue 26" do not close anything.
 */
export function closingKeywords(message) {
  return [...String(message).matchAll(/\b(close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?\s+(?:[\w.-]+\/[\w.-]+)?#\d+/gi)].map((m) => m[0]);
}

// ── vitest ──────────────────────────────────────────────────────────────────

/**
 * Judges a vitest JSON report: nothing failed, and every test whose title
 * matches one of `mustPass` actually PASSED — not skipped.
 *
 * ⚠ Why the second half: the workflow guards (releaseGatesImages,
 * goreleaserTemplates) read the public workflows, which live only in the
 * export checkout. Without FILEX_WORKFLOWS_DIR they skip, and a skipped guard
 * reports as a green file. v0.43.1's tag round was green that way while the
 * very guard it needed had not run (lesson #455).
 */
export function vitestVerdict(report, { mustPass = [] } = {}) {
  const problems = [];
  if (!report || !Array.isArray(report.testResults)) {
    return { ok: false, problems: ['no vitest JSON report to read'], passed: 0 };
  }
  const tests = report.testResults.flatMap((f) =>
    (f.assertionResults ?? []).map((a) => ({ file: f.name, title: a.title, full: a.fullName ?? a.title, status: a.status })),
  );
  const failed = tests.filter((t) => t.status === 'failed');
  for (const t of failed) problems.push(`failed: ${t.full}`);
  for (const f of report.testResults) {
    if (f.status === 'failed' && !(f.assertionResults ?? []).some((a) => a.status === 'failed')) {
      problems.push(`file failed before its tests ran: ${f.name}${f.message ? ` — ${String(f.message).split('\n')[0]}` : ''}`);
    }
  }
  if ((report.numFailedTestSuites ?? 0) > 0 && problems.length === 0) problems.push(`${report.numFailedTestSuites} test file(s) failed`);
  for (const want of mustPass) {
    const match = tests.filter((t) => t.title.includes(want) || t.full.includes(want));
    if (match.length === 0) problems.push(`no test named "${want}" ran — was it renamed or is the file missing?`);
    else if (match.some((t) => t.status === 'failed')) continue; // already listed above
    else if (!match.some((t) => t.status === 'passed')) {
      problems.push(`"${want}" did not run (${match.map((t) => t.status).join(', ')}) — a skipped guard proves nothing`);
    }
  }
  if (tests.length === 0) problems.push('the report lists no tests at all');
  return { ok: problems.length === 0, problems, passed: tests.filter((t) => t.status === 'passed').length };
}

// ── what was published ──────────────────────────────────────────────────────

/** Markdown inline → the text a reader sees. */
export function markdownInline(s) {
  // ⚠ A picture is dropped, alt text and all: the page renders it as <img>,
  // which carries no text, so an alt kept here never matches the live page.
  // v0.46.0's `### PUT /api/admin/storages/order ![admin](badge)` held the
  // docs gate red against a site that was already current.
  return String(s)
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/(^|[\s(])[*_]([^*_]+)[*_]/g, '$1$2')
    .replace(/\s+/g, ' ')
    .trim();
}

/** Rendered HTML → comparable plain text (tags dropped, entities decoded). */
export function htmlText(html) {
  const named = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' };
  return String(html)
    .replace(/<script[\s\S]*?<\/script>/gi, ' ')
    .replace(/<style[\s\S]*?<\/style>/gi, ' ')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e) => {
      if (e[0] === '#') {
        const n = e[1] === 'x' || e[1] === 'X' ? parseInt(e.slice(2), 16) : Number(e.slice(1));
        return Number.isFinite(n) ? String.fromCodePoint(n) : m;
      }
      return named[e.toLowerCase()] ?? m;
    })
    .replace(/[\u200b\u00a0]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

/**
 * The headings of a Markdown page, as the text a reader sees, in page order:
 * the probes that tell a docs site serving this release from one serving an
 * old snapshot. Headings with markup the site renders differently (HTML, Vue
 * braces) are left out rather than half-matched.
 *
 * ⚠ Why headings and not the Releases page: the docs server regenerates
 * RELEASES.md by itself on a cron, so "Latest - vX.Y.Z" goes green even while
 * every other page is the previous release's snapshot (2026-08-29 and again
 * 2026-09-25, Altyapı lesson #339 / filex #511).
 *
 * ⚠ A `#` line inside a code block is not a heading. The gate used to read
 * `git diff --unified=0` line by line, where no fence can be seen: v0.50.0
 * added the comment "# an existing SFTP / NAS - any driver via `config`" to a
 * yaml block in docs/STORAGE.md, the gate took it for a heading, and called a
 * site that was already current an "OLD snapshot" (lesson #964, task #142).
 * So the whole page is read with CommonMark's fence rules: a run of three or
 * more backticks or tildes, indented at most three spaces, opens a fence; a
 * run of the same character at least as long, with nothing after it, closes
 * it; a fence never closed runs to the end of the page. A backtick run with
 * another backtick on its line is inline code, not a fence. YAML front matter
 * at the top of a page is not part of the page either. The rules live in
 * docs-site/scripts/markdown-headings.mjs, which the Releases page generator
 * reads as well: one reader, so the two cannot disagree (task #148).
 */
export function headingsOf(markdown) {
  const out = [];
  for (const { level, text: raw } of headingLines(markdown)) {
    if (level > 4 || /[<>{}]/.test(raw)) continue;
    const text = markdownInline(raw);
    if (text.length >= 4) out.push(text);
  }
  return out;
}

/**
 * The headings `after` has and `before` did not, counted as a multiset: a
 * heading that only moved is not new, a second "Example" on the page is.
 */
export function newHeadings(before, after) {
  const had = new Map();
  for (const h of headingsOf(before)) had.set(h, (had.get(h) ?? 0) + 1);
  const out = [];
  for (const h of headingsOf(after)) {
    const n = had.get(h) ?? 0;
    if (n > 0) had.set(h, n - 1);
    else out.push(h);
  }
  return out;
}

/** docs/STORAGE.md → <base>/STORAGE ; docs/index.md → <base>/ */
export function docsPageUrl(base, file) {
  const rel = file.replace(/^docs\//, '').replace(/\.md$/, '');
  const b = base.replace(/\/+$/, '');
  if (rel === 'index') return `${b}/`;
  return `${b}/${rel.replace(/(^|\/)index$/, '$1')}`;
}

/**
 * An electron-builder feed (`latest.yml`): its version and every file it
 * offers. A deliberately small reader for the one shape electron-builder
 * writes — anything it cannot read is an error, not an empty list.
 */
export function parseFeed(yml) {
  const version = /^version:\s*['"]?([^'"\s]+)['"]?\s*$/m.exec(yml)?.[1] ?? null;
  const files = [];
  let cur = null;
  let inFiles = false;
  for (const line of String(yml).split(/\r?\n/)) {
    if (/^files:\s*$/.test(line)) {
      inFiles = true;
      continue;
    }
    if (inFiles && /^\S/.test(line)) inFiles = false;
    if (!inFiles) continue;
    const start = /^\s*-\s+url:\s*(.+?)\s*$/.exec(line);
    if (start) {
      cur = { url: start[1].replace(/^['"]|['"]$/g, '') };
      files.push(cur);
      continue;
    }
    const kv = /^\s+(sha512|size):\s*(.+?)\s*$/.exec(line);
    if (kv && cur) cur[kv[1]] = kv[1] === 'size' ? Number(kv[2]) : kv[2].replace(/^['"]|['"]$/g, '');
  }
  if (!version) throw new Error('the feed names no version');
  if (files.length === 0) throw new Error('the feed lists no files');
  for (const f of files) if (!f.sha512) throw new Error(`the feed lists ${f.url} without a sha512`);
  return { version, files };
}

/** The update manifest's newest release (`filex.sh/updates/stable.json`). */
export function manifestNewest(doc) {
  const versions = (doc?.releases ?? []).map((r) => r?.version).filter((v) => parseVersion(v));
  versions.sort((a, b) => compareVersions(b, a));
  return versions[0] ?? null;
}

/**
 * What a running filex reports as its version (`/api/capabilities`):
 * "v0.44.2 (503fc90…, 2026-09-25T08:09:57Z)".
 */
export function capabilitiesBuild(version) {
  const m = /^(v\d+\.\d+\.\d+)(?:\s+\(([0-9a-f]{7,40})?(?:,\s*([^)]*))?\))?/.exec(String(version ?? ''));
  return m ? { tag: m[1], commit: m[2] ?? null, date: m[3] ?? null } : null;
}

/**
 * A path glob: `*` stays inside one directory, `**` crosses them, `**\/`
 * may also match nothing. Everything else is literal.
 */
export function globMatch(glob, file) {
  const special = '.+^$(){}|[]?';
  let re = '^';
  for (let i = 0; i < glob.length; i++) {
    const ch = glob[i];
    if (ch === '*' && glob[i + 1] === '*') {
      if (glob[i + 2] === '/') {
        re += '(?:.*/)?';
        i += 2;
      } else {
        re += '.*';
        i += 1;
      }
    } else if (ch === '*') {
      re += '[^/]*';
    } else if (special.includes(ch) || ch === String.fromCharCode(92)) {
      re += String.fromCharCode(92) + ch;
    } else {
      re += ch;
    }
  }
  return new RegExp(`${re}$`).test(file);
}

// ── what GitHub ran on a commit ─────────────────────────────────────────────

/**
 * The name release.yml's `run-name` gives a dry run of a branch: started by
 * hand, `publish` off, every package (`only: all`), no tag. A run's inputs are
 * not in GitHub's API, so the name is how the release gate and the tag run's
 * `verify` job tell the one dry run that tested everything from the others.
 */
export function dryRunTitle(sha) {
  return `dry run all ${sha}`;
}

/**
 * One workflow's runs on one commit, as one answer. A success anywhere wins
 * (a re-run that passed is a pass); then anything still going means wait;
 * only then is a run that ended any other way the answer.
 */
export function runVerdict(runs) {
  const list = runs ?? [];
  if (!list.length) return { state: 'none', run: null };
  const ok = list.find((r) => r.status === 'completed' && r.conclusion === 'success');
  if (ok) return { state: 'success', run: ok };
  const going = list.find((r) => r.status !== 'completed');
  if (going) return { state: 'running', run: going };
  return { state: 'failure', run: list[0] };
}

/**
 * A run's parts, read one by one (#174): ci.yml's matrix is a job per part,
 * and the gate stage waits for every part it expects (`expected`, job names)
 * rather than for the run's one conclusion. `jobs` are the run's latest
 * attempt ({ name, status, conclusion, url }); `finished` says whether the
 * run itself has completed - before that, a matrix job GitHub has not
 * created yet (it expands a matrix once its plan job is done) is `waiting`,
 * after it a part with no job is `missing`.
 *
 * A part that ended any way but success - failure, cancelled, timed out,
 * skipped - is `failure`: a part the full matrix leaves out did not pass.
 * The state: failure when any part failed (at once, whatever is still
 * running), else incomplete when one is missing, else running while any runs
 * or waits, else success.
 */
export function partsVerdict(jobs, expected, { finished = false } = {}) {
  const byName = new Map();
  for (const j of jobs ?? []) byName.set(j.name, j);
  const parts = (expected ?? []).map((name) => {
    const j = byName.get(name);
    if (!j) return { name, state: finished ? 'missing' : 'waiting' };
    if (j.status !== 'completed') return { name, state: 'running', url: j.url };
    return { name, state: j.conclusion === 'success' ? 'success' : 'failure', conclusion: j.conclusion ?? 'none', url: j.url };
  });
  const of = (...states) => parts.filter((p) => states.includes(p.state));
  const failed = of('failure');
  const missing = of('missing');
  const running = of('running', 'waiting');
  const green = of('success');
  const state = failed.length ? 'failure' : missing.length ? 'incomplete' : running.length ? 'running' : 'success';
  return { state, parts, failed, missing, running, green };
}

/** "41/52 parts green, 11 running" - one line for the log. */
export function partsLine(v) {
  const bits = [`${v.green.length}/${v.parts.length} parts green`];
  if (v.running.length) bits.push(`${v.running.length} running`);
  if (v.failed.length) bits.push(`${v.failed.length} red`);
  if (v.missing.length) bits.push(`${v.missing.length} missing`);
  return bits.join(', ');
}

/**
 * Whether a dry run kept what the tag run promotes (#174): `names` are its
 * artifacts; every one of `required` must be there, and one of `optional`
 * (the macOS row, left out of a macOS-less dry run) may be absent and is
 * named as such.
 */
export function promotionVerdict(names, { required = [], optional = [] } = {}) {
  const have = new Set(names ?? []);
  const lacking = required.filter((n) => !have.has(n));
  const without = optional.filter((n) => !have.has(n));
  return { ok: lacking.length === 0, lacking, without };
}

// ── README ──────────────────────────────────────────────────────────────────

/** Every relative image a markdown/HTML page shows. */
export function readmeImages(markdown) {
  const out = new Set();
  for (const m of String(markdown).matchAll(/!\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)/g)) out.add(m[1]);
  for (const m of String(markdown).matchAll(/<img\b[^>]*\bsrc=["']([^"']+)["']/gi)) out.add(m[1]);
  return [...out].filter((u) => !/^(?:[a-z]+:)?\/\//i.test(u) && !u.startsWith('data:')).map((u) => u.replace(/^\.\//, '').split('#')[0]);
}

// ── profiles: which gates run where (task #172) ────────────────────────────

/**
 * What `pnpm release X.Y.Z --profile <name>` runs, and where.
 *
 *   minor  (the default) here, before the export: the builds and vue-tsc, the
 *          unit suites in UTC, the desktop and the Store copy, both images and
 *          the shop window (plan.pretag). The heavy suites (plan.heavy) are
 *          not run here first: those GitHub runs on the export commit (ci.yml)
 *          are read at the gate stage, and those it does not run yet run on
 *          this machine DURING the gate stage, beside GitHub's wait.
 *   patch  X.Y.Z+1 only. The builds and the packaging always (#76: two ghost
 *          releases died building, not testing), every other gate only when
 *          something it reads changed since the last release, the Go suite
 *          on the changed packages and their importers; GitHub still runs
 *          every suite on the export commit (#165, decision 5).
 *   full   everything here, before the export, as every release did up to
 *          0.52: for a day GitHub cannot be trusted with the heavy suites.
 *
 * ⚠ Why the heavy suites left this machine (#165): from 0.50 to 0.52 the
 * pretag here found no product fault the build host and GitHub had not, ran
 * the Go suite a fifth and vitest an eighth time per release, and started
 * over after every red - 935 minutes lost over seven releases.
 */
export const PROFILES = ['minor', 'patch', 'full'];

const PROFILE_RANK = { patch: 0, minor: 1, full: 2 };

/**
 * True when a pretag that passed in profile `was` answers for `want` too. A
 * run recorded before profiles existed ran the whole chain: `full`.
 */
export function profileCovers(was, want) {
  return (PROFILE_RANK[was ?? 'full'] ?? -1) >= (PROFILE_RANK[want] ?? Infinity);
}

/**
 * Whether `file` is one of a gate's inputs: it matches a glob and no "!glob"
 * takes it back out (globMatch's grammar; the order does not matter).
 */
export function matchesInputs(globs, file) {
  let hit = false;
  for (const g of globs ?? []) {
    if (g.startsWith('!')) {
      if (globMatch(g.slice(1), file)) return false;
    } else if (!hit && globMatch(g, file)) {
      hit = true;
    }
  }
  return hit;
}

/**
 * Where the gate stage reads the heavy suites from (task #181): GitHub
 * Actions (ci.yml and the release dry run), or CircleCI when Actions is down
 * (`pnpm release X.Y.Z --resume --gate circleci`). The name is also the field
 * a heavy gate of the plan sets when that CI runs the same suite.
 */
export const GATE_SOURCES = ['github', 'circleci'];

/**
 * The gates of a profile, sorted by where they run:
 *   pretag   here, before the export
 *   local    here, during the gate stage, while the CI tests the export
 *            (with the builds they are `after`, built again there)
 *   remote   by the CI `source` names, on the export commit; the gate stage
 *            waits for it (`github`: the same list, its name before #181)
 *   skipped  nowhere this time, each with why ({ gate, why })
 *
 * `changed` is the list of files changed since the last release (a patch
 * decides with it), or null when there is none to compare with - then
 * everything runs: in doubt, a gate runs.
 *
 * `source` is where the gate stage reads the export commit's suites
 * (GATE_SOURCES). A heavy gate the plan does not give that source runs here
 * at the gate stage instead: with `circleci`, the Cypress suite and this
 * machine's clock run here, the suites CircleCI runs are read from it.
 *
 * A gate says, in the plan:
 *   always      runs in a patch too, whatever changed (builds, packaging)
 *   inputs      the files its verdict depends on (the gate cache's key)
 *   patchWhen   what makes a patch run it, when narrower than `inputs`
 *   github      (heavy) the workflow that runs the same suite on GitHub
 *   circleci    (heavy) the CircleCI job that runs the same suite
 *               (.circleci/config.yml, workflow `ci`)
 *   patch       (heavy) 'changed': a patch runs it here when touched
 *   patchOnly   (heavy) only a patch runs it (the targeted Go suite)
 *   patchSkip   (heavy) why a patch does not run it here
 */
export function selectGates({ pretag = [], heavy = [] }, profile, changed = null, { source = 'github' } = {}) {
  if (!GATE_SOURCES.includes(source)) throw new Error(`gate source ${source}: the sources are ${GATE_SOURCES.join(', ')}`);
  const touched = (g) => changed == null || changed.some((f) => matchesInputs(g.patchWhen ?? g.inputs ?? ['**'], f));
  const remote = (g) => !!g[source];
  const out = { pretag: [], local: [], remote: [], skipped: [] };
  out.github = out.remote;
  const skip = (gate, why) => out.skipped.push({ gate, why });
  const unchanged = 'nothing it reads changed since the last release';
  for (const g of pretag) {
    if (profile !== 'patch' || g.always || touched(g)) out.pretag.push(g);
    else skip(g, unchanged);
  }
  for (const g of heavy) {
    if (profile === 'full') {
      if (g.patchOnly) skip(g, 'the whole suite runs here instead');
      else out.pretag.push(g);
    } else if (profile === 'minor') {
      if (g.patchOnly) skip(g, 'only a patch runs it; the whole suite runs on GitHub');
      else if (remote(g)) out.remote.push(g);
      else out.local.push(g);
    } else if ((g.patchOnly || g.patch === 'changed') && touched(g)) {
      out.pretag.push(g);
    } else if (remote(g) && !g.patchOnly) {
      out.remote.push(g);
    } else if (source !== 'github' && g.github && !g.patchOnly) {
      // GitHub would run it for a patch, and the CI read in its place does
      // not: here, at the gate stage, like a minor's.
      out.local.push(g);
    } else {
      skip(g, g.patchOnly || g.patch === 'changed' ? unchanged : (g.patchSkip ?? `a patch runs it only on ${source === 'circleci' ? 'CircleCI' : 'GitHub'}`));
    }
  }
  // The gates that run at the gate stage take along the builds they are
  // `after`, transitively: the binary they test is built again there, from
  // the release commit, beside GitHub's wait - never trusted to be still on
  // disk, and still current, from a pretag hours earlier.
  if (out.local.length) {
    const byName = new Map([...pretag, ...heavy].map((g) => [g.name, g]));
    const need = new Set();
    const visit = (g) => {
      for (const n of g.after ?? []) {
        const d = byName.get(n);
        if (d && !need.has(n)) {
          need.add(n);
          visit(d);
        }
      }
    };
    out.local.forEach(visit);
    const local = new Set(out.local.map((g) => g.name));
    out.local = [...pretag, ...heavy].filter((g) => local.has(g.name) || need.has(g.name));
  }
  return out;
}

// ── the gate cache (task #172) ──────────────────────────────────────────────
//
// A gate that passed is not run again while nothing it reads has changed.
// The key is a digest of the gate's own recipe and the CONTENT of its inputs
// (blob ids from `git ls-tree`), never the commit: a fix in e2e/ leaves the
// Go suite's key alone, and the stamp reaches only the gates that read what it
// writes. Only green is kept. Anything that cannot be read - a working tree
// that differs from HEAD, an entry that does not parse - is a miss, and the
// gate runs: the cache may cost a run, never pass one.
//
// ⚠ "The stamp is normalised" means exactly that scoping, and no rewriting of
// file contents: a test that reads the version or the newest CHANGELOG
// section must run after the stamp (0.44.1: two such tests went red on the
// tag run), so a gate whose inputs hold what the stamp writes runs again.

export const GATE_CACHE_SCHEMA = 1;

/** `git ls-tree -r -z` output → [{ mode, type, id, path }], in its order. */
export function parseLsTree(z) {
  const out = [];
  for (const rec of String(z ?? '').split(NUL)) {
    const tab = rec.indexOf('\t');
    if (tab < 0) continue;
    const [mode, type, id] = rec.slice(0, tab).split(' ');
    out.push({ mode, type, id, path: rec.slice(tab + 1) });
  }
  return out;
}

/** The tree entries a gate reads, by its `inputs` globs. */
export function inputEntries(entries, globs) {
  return entries.filter((e) => matchesInputs(globs, e.path));
}

/**
 * The cache key of one gate: its name, what it runs (`recipe`: the command
 * with its environment and working directory, as the gate resolved them),
 * what else it reads (`recipe.extra`), the machine (`facts`) and every input
 * file's path, mode and blob id.
 */
export function gateCacheKey({ name, recipe, files, facts }) {
  const h = createHash('sha256');
  h.update(JSON.stringify({ schema: GATE_CACHE_SCHEMA, name, recipe: recipe ?? null, facts: facts ?? null }));
  const rows = files.map((f) => `${f.mode} ${f.id} ${f.path}`).sort();
  h.update(`\n${rows.length}\n`);
  for (const r of rows) h.update(`${r}\n`);
  return h.digest('hex');
}

/**
 * Whether the text of a cache entry says this gate passed on this key. A
 * file that does not parse, names another gate or key, or does not say green
 * is a miss with a reason, never an error: the gate then runs.
 */
export function cacheEntryHit(text, { key, name }) {
  let e;
  try {
    e = JSON.parse(text);
  } catch {
    return { hit: false, why: 'it does not parse' };
  }
  if (!e || typeof e !== 'object') return { hit: false, why: 'it is not an object' };
  if (e.schema !== GATE_CACHE_SCHEMA) return { hit: false, why: `schema ${e.schema ?? 'missing'}, not ${GATE_CACHE_SCHEMA}` };
  if (e.key !== key || e.name !== name) return { hit: false, why: 'it is for another gate or other inputs' };
  if (e.ok !== true) return { hit: false, why: 'it does not say green' };
  return { hit: true, entry: e };
}

// ── a patch's Go packages ───────────────────────────────────────────────────

/**
 * The Go package directories a patch changed, relative to the module
 * (`./internal/foo`), for scripts/release/gates/go-targeted.sh - which adds
 * every package that imports one of them. `all` (the whole module) when it
 * cannot place a change: no previous release to compare with, a file at the
 * module root (go.mod, go.sum), or a file no package directory holds.
 *
 * `hasGoFiles(dir)` says whether a directory (relative to the module) holds a
 * .go file today. A file under testdata/ (or a directory go ignores, `_x` or
 * `.x`) belongs to the package above it: wasmplugin's tests build their
 * fixture from testdata/echo. A deleted package belongs to its parent.
 */
export function goPackageDirs(changed, hasGoFiles, module = 'backend') {
  if (changed == null) return { all: true, dirs: [], why: 'no previous release to compare with' };
  const prefix = `${module}/`;
  const dirs = new Set();
  for (const f of changed) {
    if (!f.startsWith(prefix)) continue;
    const rel = f.slice(prefix.length);
    if (!rel.includes('/')) return { all: true, dirs: [], why: `${f} is at the module root` };
    let parts = rel.split('/').slice(0, -1);
    const cut = parts.findIndex((p) => p === 'testdata' || p.startsWith('_') || p.startsWith('.'));
    if (cut >= 0) parts = parts.slice(0, cut);
    while (parts.length && !hasGoFiles(parts.join('/'))) parts.pop();
    if (!parts.length) return { all: true, dirs: [], why: `no Go package holds ${f}` };
    dirs.add(`./${parts.join('/')}`);
  }
  return { all: false, dirs: [...dirs].sort() };
}

// ── what the stamp writes ───────────────────────────────────────────────────

/**
 * Whether `file` is one the stamp writes: CHANGELOG.md, a workspace
 * package.json (`pkgDirs`), anything under deploy/.
 */
export function stampWrites(file, pkgDirs) {
  return file === 'CHANGELOG.md' || file.startsWith('deploy/') || pkgDirs.some((p) => file === `${p}/package.json`);
}

// ── a scripts/chain run as evidence (task #170's result.json) ───────────────

/**
 * Whether a scripts/chain result (schema 1: `schema`, `profile`, `sha`,
 * `finished`, `ok`, `stopped`, `dirty_files`) proves the heavy gates for this
 * release: a finished green run of an accepted profile, on a clean checkout of
 * a commit this release contains, with nothing changed since but what the
 * stamp writes (`since`: the files changed from its commit to the release
 * commit, or null when the release does not contain that commit).
 */
export function chainVerdict(result, { accepted = [], since = null, stampFile = () => false } = {}) {
  if (!result || typeof result !== 'object') return { ok: false, problems: ['it is not a chain result (scripts/chain writes result.json)'] };
  const problems = [];
  if (result.schema !== 1) problems.push(`schema ${result.schema ?? 'missing'}; this release reads schema 1`);
  if (!result.finished) problems.push('the run never finished');
  else if (result.ok !== true) problems.push(`the run was ${result.stopped ? 'stopped' : 'red'}`);
  if (!accepted.includes(result.profile)) problems.push(`profile ${result.profile ?? 'missing'}; this release takes ${accepted.join(' or ') || 'none'}`);
  if (result.dirty_files) problems.push(`it ran with ${result.dirty_files} uncommitted file(s)`);
  if (since === null) problems.push(`it ran on ${String(result.sha ?? 'no commit').slice(0, 10)}, which this release does not contain`);
  else {
    const other = since.filter((f) => !stampFile(f));
    if (other.length) problems.push(`${other.length} file(s) changed since it ran, beyond the stamp: ${other.slice(0, 8).join(', ')}`);
  }
  return { ok: problems.length === 0, problems };
}
