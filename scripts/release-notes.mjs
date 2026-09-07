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
