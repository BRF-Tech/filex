// The pure half of `pnpm release`: every judgement the release makes that can
// be made from text alone. Nothing in this file runs a process, reads the
// disk or touches the network, so each rule is unit-tested on its own
// (web/tests/deploy/releaseChecks.test.ts) and the CLI around it
// (scripts/release.mjs) is only plumbing.
//
// Each rule below exists because a release went out without it. The incident
// is written next to the rule, so nobody deletes one as "too strict" without
// reading what it cost the last time.

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
  // 0.x goes straight to 1.1.0 (Burak's decision, 2026-09-24).
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
 * The headings a diff ADDS to documentation pages, as `{ file, text }` — the
 * probe that tells a docs site serving this release from one serving an old
 * snapshot. Headings with markup the site renders differently (HTML, Vue
 * braces) are left out rather than half-matched.
 *
 * ⚠ Why headings and not the Releases page: the docs server regenerates
 * RELEASES.md by itself on a cron, so "Latest — vX.Y.Z" goes green even while
 * every other page is the previous release's snapshot (2026-08-29 and again
 * 2026-09-25, Altyapı lesson #339 / filex #511).
 */
export function headingsAdded(unifiedDiff) {
  const out = [];
  let file = null;
  for (const line of String(unifiedDiff).split('\n')) {
    const f = /^\+\+\+ b\/(.+)$/.exec(line);
    if (f) {
      file = f[1];
      continue;
    }
    if (!file || line.startsWith('+++')) continue;
    const h = /^\+#{1,4}\s+(.+?)\s*#*\s*$/.exec(line);
    if (!h) continue;
    if (/[<>{}]/.test(h[1])) continue;
    const text = markdownInline(h[1]);
    if (text.length >= 4) out.push({ file, text });
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

// ── README ──────────────────────────────────────────────────────────────────

/** Every relative image a markdown/HTML page shows. */
export function readmeImages(markdown) {
  const out = new Set();
  for (const m of String(markdown).matchAll(/!\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)/g)) out.add(m[1]);
  for (const m of String(markdown).matchAll(/<img\b[^>]*\bsrc=["']([^"']+)["']/gi)) out.add(m[1]);
  return [...out].filter((u) => !/^(?:[a-z]+:)?\/\//i.test(u) && !u.startsWith('data:')).map((u) => u.replace(/^\.\//, '').split('#')[0]);
}
