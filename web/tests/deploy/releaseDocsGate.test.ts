// The docs gate of `pnpm release` (docsSite in scripts/release/verify.mjs),
// run whole: on a throwaway git history, against a docs site that is a map of
// pages in memory. No port is opened and nothing touches the network.
//
// ⚠ Why the gate and not only the heading rule: v0.50.0's deploy was held red
// by a `#` comment inside a yaml block in docs/STORAGE.md (lesson #964). The
// fault was in HOW the gate read the release (diff lines, where a code fence
// cannot be seen), so the proof is the gate itself: a release that adds only
// that comment leaves a current site green, and a heading the release really
// added is still red on a site that does not have it.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { docsSite } from '../../../scripts/release/verify.mjs';

const TIMEOUT = 60_000;
const BASE = 'http://docs.example.invalid';
const TAG = 'v0.2.0';

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

/** A repository at v0.1.0 whose git ignores the user's own configuration. */
function history(v010: Record<string, string>) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-docs-gate-'));
  roots.push(root);
  const gitconfig = path.join(root, 'gitconfig');
  fs.writeFileSync(
    gitconfig,
    '[user]\n\tname = Docs Gate Test\n\temail = docs@test.invalid\n[init]\n\tdefaultBranch = main\n' +
      '[core]\n\tautocrlf = false\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n',
  );
  const env: NodeJS.ProcessEnv = { ...process.env };
  for (const k of Object.keys(env)) if (k.startsWith('GIT_')) delete env[k];
  Object.assign(env, { GIT_CONFIG_GLOBAL: gitconfig, GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0' });
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo);
  const git = (...args: string[]) => {
    const r = spawnSync('git', ['-C', repo, ...args], { env, encoding: 'utf8' });
    if (r.status !== 0) throw new Error(`git ${args.join(' ')} → ${r.status}\n${r.stdout}\n${r.stderr}`);
    return r.stdout.trim();
  };
  /** Writes the files (null deletes one) and commits them; returns the commit. */
  const commit = (files: Record<string, string | null>, message: string) => {
    for (const [f, text] of Object.entries(files)) {
      const p = path.join(repo, f);
      if (text === null) {
        fs.rmSync(p);
      } else {
        fs.mkdirSync(path.dirname(p), { recursive: true });
        fs.writeFileSync(p, text);
      }
    }
    git('add', '-A');
    git('commit', '-q', '-m', message);
    return git('rev-parse', 'HEAD');
  };
  git('init', '-q');
  commit(v010, 'v0.1.0');
  git('tag', 'v0.1.0');
  return { repo, commit };
}

/** The site: path → HTML; every other path answers 404. */
function site(pages: Record<string, string>) {
  return async (url: string) => {
    const html = pages[new URL(url).pathname];
    if (html === undefined) return { ok: false, status: 404, text: async () => 'not found' };
    return { ok: true, status: 200, text: async () => html };
  };
}

async function gate(repo: string, releaseCommit: string, pages: Record<string, string>) {
  const g = docsSite(`docs serve ${TAG}`, BASE, { fetch: site({ '/RELEASES': `<main><p>Latest - ${TAG}</p></main>`, ...pages }) });
  return g.check({ repo, prevTag: 'v0.1.0', releaseCommit, tag: TAG });
}

// docs/STORAGE.md as v0.50.0 changed it: a comment in a yaml block, which the
// page prints as code, backticks and all.
const COMMENT = '# an existing SFTP / NAS - any driver via `config`';
const storage = (comment: string[], extra: string[] = []) =>
  ['# Storage', '', '## Mount a storage at install time', '', '```yaml', ...comment, 'storage:', '  type: sftp', '```', '', ...extra].join('\n');
const storagePage = (comment: string[], extra: string[] = []) =>
  '<main><h1 id="storage">Storage</h1><h2 id="mount">Mount a storage at install time</h2>' +
  `<div class="language-yaml"><pre><code>${[...comment, 'storage:', '  type: sftp'].map((l) => `<span class="line"><span>${l}</span></span>`).join('\n')}</code></pre></div>` +
  extra.map((h) => `<h2>${h}</h2>`).join('') +
  '</main>';

describe('the docs gate (docsSite)', () => {
  it('leaves a current site green when a release adds only a # comment inside a yaml block (v0.50.0, lesson #964)', { timeout: TIMEOUT }, async () => {
    const { repo, commit } = history({ 'docs/STORAGE.md': storage([]) });
    const release = commit({ 'docs/STORAGE.md': storage([COMMENT]) }, 'docs: name the SFTP example');
    const r = await gate(repo, release, { '/STORAGE': storagePage([COMMENT]) });
    expect(r.detail).not.toContain('OLD snapshot');
    expect(r.ok).toBe(true);
    expect(r.detail).toBe(`RELEASES names ${TAG}; this release added no headings to probe`);
  });

  it('still finds a heading the release really added: red on an old snapshot, green once it is live', { timeout: TIMEOUT }, async () => {
    const { repo, commit } = history({ 'docs/STORAGE.md': storage([]) });
    const release = commit({ 'docs/STORAGE.md': storage([COMMENT], ['## Mount a NAS over SFTP', '', 'Prose.', '']) }, 'docs: a NAS section');

    const stale = await gate(repo, release, { '/STORAGE': storagePage([]) });
    expect(stale.ok).toBe(false);
    expect(stale.detail).toContain(`${BASE}/STORAGE is an OLD snapshot: 1 heading(s)`);
    expect(stale.detail).toContain('"Mount a NAS over SFTP"');
    expect(stale.detail).not.toContain('an existing SFTP');

    const live = await gate(repo, release, { '/STORAGE': storagePage([COMMENT], ['Mount a NAS over SFTP']) });
    expect(live.detail).toBe(`1 new heading(s) on 1 page(s) are live; RELEASES names ${TAG}`);
    expect(live.ok).toBe(true);
  });

  it('probes a new page whole, and neither a deleted page nor a heading that only moved', { timeout: TIMEOUT }, async () => {
    const guide = (...sections: string[]) => ['# Guide', '', ...sections.flatMap((s) => [`## ${s}`, '', 'Text.', ''])].join('\n');
    const { repo, commit } = history({
      'docs/GUIDE.md': guide('Getting started', 'Sharing a folder'),
      'docs/OLD.md': '# The old page\n\n## Soon to be gone\n',
    });
    const release = commit(
      {
        'docs/GUIDE.md': guide('Sharing a folder', 'Getting started'),
        'docs/OLD.md': null,
        'docs/NEW.md': '# The new page\n\n## A brand new section\n',
        'docs/internal/NOTES.md': '# Notes the site leaves out\n',
      },
      'docs: a new page, an old one gone, two sections swapped',
    );
    const r = await gate(repo, release, {
      '/GUIDE': '<main><h1>Guide</h1><h2>Getting started</h2><h2>Sharing a folder</h2></main>',
      '/NEW': '<main><h1>The new page</h1><h2>A brand new section</h2></main>',
    });
    // the two on the new page; the swapped sections were on the site already
    expect(r.detail).toMatch(/^2 new heading\(s\) on /);
    expect(r.ok).toBe(true);
    expect(r.log).toContain('docs/internal/NOTES.md: not published (404)');
  });

  it('counts only the pages that answered as live, and names an unpublished one apart', { timeout: TIMEOUT }, async () => {
    // The green line used to count every page with a new heading, 404s
    // included: "1 new heading(s) on 2 page(s) are live" with one page live.
    const { repo, commit } = history({ 'docs/GUIDE.md': '# Guide\n\n## Getting started\n' });
    const release = commit(
      {
        'docs/GUIDE.md': '# Guide\n\n## Getting started\n\n## Sharing a folder\n',
        'docs/internal/NOTES.md': '# Notes the site leaves out\n',
      },
      'docs: a section, and notes the site does not publish',
    );
    const r = await gate(repo, release, { '/GUIDE': '<main><h1>Guide</h1><h2>Getting started</h2><h2>Sharing a folder</h2></main>' });
    expect(r.ok).toBe(true);
    expect(r.detail).toBe(
      `1 new heading(s) on 1 page(s) are live; 1 page(s) with new headings are not published (404) and not counted: docs/internal/NOTES.md; RELEASES names ${TAG}`,
    );
  });
});
