// Derives the short "what changed" blurb an app store shows, from CHANGELOG.md.
//
// ⚠⚠ Why this is derived and not written by hand: a `releaseNotes` field
// somebody types once and forgets is worse than an absent one. An empty field
// says "we do not publish notes"; a field frozen at v0.4.0 tells a user
// installing v0.34.2 that they are getting February's release. The same
// failure that put `v0.4.0` in three store manifests for twenty-nine releases
// (see sync-deploy-versions.mjs) applies here with no version number to make
// the drift visible — so the notes are generated, and
// `web/tests/deploy/deployVersions.test.ts` fails the build when the manifest
// stops matching what this function produces.
//
// The derivation is deliberately lossy. A store card is a few lines under a
// title, not a changelog: this takes the HEADLINE of each top-level entry for
// the released version, caps the list, and links to the full file.

import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

/** Max headlines carried into the store blurb. */
const MAX_ITEMS = 5;
/** Max characters per headline before it is cut at a word boundary. */
const MAX_LEN = 180;

const CHANGELOG_URL = 'https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md';

/**
 * Extracts the body of one `## [version] - date` section from CHANGELOG.md.
 * Returns null when that version has no section — a release with no changelog
 * entry is a mistake the caller should surface, not paper over.
 */
export function changelogSection(changelog, version) {
  const start = changelog.search(
    new RegExp(`^## \\[${version.replace(/\./g, '\\.')}\\]`, 'm'),
  );
  if (start < 0) return null;
  const rest = changelog.slice(start);
  const nl = rest.indexOf('\n');
  const heading = rest.slice(0, nl);
  const after = rest.slice(nl + 1);
  const end = after.search(/^## \[/m);
  return { heading, body: end < 0 ? after : after.slice(0, end) };
}

/** Markdown → the plain text a store renders. */
function plain(s) {
  return s
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1') // links keep their text
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\*\*([^*]*)\*\*/g, '$1')
    .replace(/(^|\s)[*_]([^*_]+)[*_]/g, '$1$2')
    .replace(/[⚠⭐]/gu, '')
    .replace(/\s+/g, ' ')
    .trim();
}

/** Cuts at a word boundary rather than mid-word, and marks the cut. */
function clamp(s, max = MAX_LEN) {
  if (s.length <= max) return s;
  const cut = s.slice(0, max);
  const sp = cut.lastIndexOf(' ');
  return `${(sp > max * 0.6 ? cut.slice(0, sp) : cut).replace(/[.,;:—-]+$/, '')}…`;
}

/**
 * The headline of one top-level changelog bullet: its bold lead when it has
 * one (the house style), otherwise its first sentence.
 */
function headline(bullet) {
  const bold = bullet.match(/^\s*\*\*(.+?)\*\*/s);
  const text = plain(bold ? bold[1] : bullet);
  const stop = text.match(/^(.+?[.!?])(\s|$)/);
  return clamp(stop ? stop[1] : text);
}

/** Splits a changelog section body into its top-level `- ` bullets. */
function bullets(body) {
  const out = [];
  let cur = null;
  for (const line of body.split('\n')) {
    if (/^- /.test(line)) {
      if (cur) out.push(cur);
      cur = line.slice(2);
    } else if (cur !== null && /^\s+\S/.test(line)) {
      cur += ' ' + line.trim();
    } else if (cur !== null && line.trim() === '') {
      // A blank line inside a bullet continues it; the next `- ` ends it.
    } else if (cur !== null && /^#{2,}/.test(line)) {
      out.push(cur);
      cur = null;
    }
  }
  if (cur) out.push(cur);
  return out;
}

/**
 * The store blurb for `version`, or null when the changelog has no section for
 * it. Pure: same changelog + version ⇒ same string, which is what lets a test
 * assert the manifest still matches.
 */
export function releaseNotes(changelog, version) {
  const sec = changelogSection(changelog, version);
  if (!sec) return null;
  const date = sec.heading.match(/-\s*(\d{4}-\d{2}-\d{2})\s*$/)?.[1];
  const items = bullets(sec.body).map(headline).filter(Boolean);

  const lines = [`filex v${version}${date ? ` — ${date}` : ''}`];
  for (const it of items.slice(0, MAX_ITEMS)) lines.push(`• ${it}`);
  if (items.length === 0) lines.push('• Maintenance release.');
  lines.push(`Full changelog: ${CHANGELOG_URL}`);
  return lines.join('\n');
}

/** Convenience for callers that just have a repo root. */
export function releaseNotesFromRepo(repo, version) {
  return releaseNotes(fs.readFileSync(path.join(repo, 'CHANGELOG.md'), 'utf8'), version);
}

// ───────────────────────────────────────────────────────────────────────────
// The GitHub release body
// ───────────────────────────────────────────────────────────────────────────
//
// ⚠⚠ Why this exists: the Releases page is where a stranger looks to decide
// whether a project is alive, and ours said nothing. GoReleaser derives the
// body from `git log`, and the filters in `.goreleaser.yml` drop `docs:`,
// `test:`, `chore:` and `ci:` — so a release whose work landed under those
// prefixes showed the release commit and nothing else. Measured on the
// published v0.34.2 page, that was the ENTIRE body:
//
//   ## Changelog
//   ### Others
//   * 99eb9eb… chore(release): v0.34.2
//
// while CHANGELOG.md carried 1,445 characters of prose for the same version.
// The prose is written for humans and already exists; the release page should
// carry it rather than a hash.
//
// ⚠ How it reaches GoReleaser: `--release-notes=FILE`. Measured against
// goreleaser v2.17.1 (2026-09-07), the file replaces the generated changelog
// ONLY — `release.header` and `release.footer` from .goreleaser.yml are still
// prepended and appended, so this must emit the changelog part alone and must
// NOT repeat the "## filex vX" title the header already prints.

/**
 * Max characters of changelog carried into a GitHub release body.
 *
 * ⚠ Not a style preference. Measured on goreleaser v2.17.1: a body over
 * 125,000 characters is truncated SILENTLY — no warning, the run reports
 * success — and what gets cut is the END, which is where the footer's "Report
 * a bug" link lives. v0.34.0's changelog section is 57,408 characters on its
 * own, so this is a real ceiling and not a theoretical one. 20,000 leaves the
 * footer five times its own length of headroom, and was measured accepted
 * without complaint.
 */
const GITHUB_MAX = 20000;

const CHANGELOG_FILE_URL = `${CHANGELOG_URL}#`;

/**
 * GitHub's heading-anchor rule, reused rather than re-derived — the same
 * function docs.filex.sh slugifies with, so a link built here and a heading
 * rendered there cannot drift. (Loaded lazily: this module is imported by a
 * vitest suite that must not pay for a second file read at import time.)
 */
async function anchorFor(heading) {
  const { githubSlug } = await import('../docs-site/.vitepress/github-slug.mjs');
  return githubSlug(heading.replace(/^#+\s*/, ''));
}

/**
 * Cuts a changelog section down to `max` characters at a boundary a reader
 * would recognise — the start of a `### ` group or of a top-level `- ` bullet
 * — rather than mid-sentence. Returns the kept text and whether anything was
 * dropped.
 */
function clampSection(body, max) {
  if (body.length <= max) return { text: body, truncated: false };
  const window = body.slice(0, max);
  // A `### ` boundary is the nicest cut — the page then ends on whole groups
  // (Upgrade notes, Security, Fixed) instead of halfway through one — but only
  // when it is not paying for that tidiness with a third of the text. Below
  // that, the last top-level bullet; below that, a paragraph break.
  const group = window.lastIndexOf('\n### ');
  const cut =
    group > max * 0.6
      ? group
      : Math.max(window.lastIndexOf('\n- '), window.lastIndexOf('\n\n'));
  return { text: body.slice(0, cut > 0 ? cut : max).trimEnd(), truncated: true };
}

/**
 * The markdown body for the GitHub release of `version`: that version's
 * CHANGELOG.md section, capped, with a link to the full entry.
 *
 * Returns null when the changelog has no section for the version — the caller
 * is expected to fail rather than publish an empty page, which is the whole
 * point of deriving this instead of typing it.
 */
export async function githubReleaseBody(changelog, version, { max = GITHUB_MAX } = {}) {
  const sec = changelogSection(changelog, version);
  if (!sec) return null;
  const anchor = await anchorFor(sec.heading);
  const full = `${CHANGELOG_FILE_URL}${anchor}`;
  const { text, truncated } = clampSection(sec.body.trim(), max);

  const out = ['## What changed', '', text, ''];
  if (truncated) {
    out.push(
      `**This release has more to it than fits on one page.** The rest of the`,
      `entry — and every earlier release — is in [CHANGELOG.md](${full}).`,
      '',
    );
  } else {
    out.push(`[Full changelog entry](${full})`, '');
  }
  return out.join('\n');
}

// ── CLI ────────────────────────────────────────────────────────────────────
//
//   node scripts/release-notes.mjs --github 0.35.0 > notes.md
//
// Exits non-zero, loudly, when the version has no changelog section: the
// release workflow runs this BEFORE goreleaser, so a missing entry stops the
// release instead of publishing a page that says nothing.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [flag, version] = process.argv.slice(2);
  if (flag !== '--github' || !version) {
    console.error('usage: node scripts/release-notes.mjs --github <version>');
    process.exit(2);
  }
  const v = version.replace(/^v/, '');
  const repo = path.resolve(import.meta.dirname, '..');
  const body = await githubReleaseBody(
    fs.readFileSync(path.join(repo, 'CHANGELOG.md'), 'utf8'),
    v,
  );
  if (!body) {
    console.error(
      `CHANGELOG.md has no "## [${v}]" section.\n` +
        'Every release earns an entry; write one before tagging. Without it the\n' +
        'release page would carry a commit hash and nothing else.',
    );
    process.exit(1);
  }
  process.stdout.write(body);
}

/**
 * Reads the `releaseNotes:` literal block out of a YAML file's text.
 *
 * ⚠ Text, not a YAML parse: the sync script rewrites this file as text (it
 * must preserve every comment and the exact shape of the rest), so read and
 * write have to agree on the same representation.
 */
// ⚠ Only indented lines are part of the block: `(?:  .*\n)*`, not a pattern
// that also swallows the blank line after it. One that did would delete the
// separator on every rewrite and glue the next key onto the notes.
export const NOTES_BLOCK = /^releaseNotes: \|-\n((?:  .*\n)*)/m;

export function readNotesBlock(yamlText) {
  const m = yamlText.match(NOTES_BLOCK);
  if (!m) return null;
  return m[1]
    .replace(/\n+$/, '')
    .split('\n')
    .map((l) => l.replace(/^ {2}/, ''))
    .join('\n');
}

/** Renders the block this file writes, for a given blurb. */
export function renderNotesBlock(notes) {
  return `releaseNotes: |-\n${notes
    .split('\n')
    .map((l) => `  ${l}`)
    .join('\n')}\n`;
}
