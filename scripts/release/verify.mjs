// Gates that look at what was PUBLISHED, not at what was built.
//
// ⚠⚠ Every one of these exists because a step "succeeded" and the thing it
// was supposed to change did not change:
//
//   - v0.31.0 was out while every desktop feed still said 0.27.4 and the
//     update manifest said v0.28.0 — four releases no installed copy was told
//     about (2026-09-05, lesson #69).
//   - docs.filex.sh kept serving the previous release's pages after a refresh
//     script reported "published — 145 files": the site is built from a
//     snapshot on the server, and only the Releases page refreshes itself
//     (2026-08-29, again 2026-09-25 — lesson #511).
//   - a desktop feed can name the right version and point at bytes that are
//     not the ones it describes; electron-updater then refuses the download
//     on every machine, silently.
//
// So each gate reads the live surface back and compares it with the release,
// and each one fails loudly when it cannot tell — "could not check" is not a
// pass.
//
// A gate here is a spec for engine.mjs's Gates: { name, check(ctx) }.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { git } from './engine.mjs';
import {
  capabilitiesBuild,
  docsPageUrl,
  headingsAdded,
  htmlText,
  manifestNewest,
  parseFeed,
  readmeImages,
} from './checks.mjs';

const TIMEOUT = 30_000;

// Every factory takes `{ fetch }`: the release's own tests hand in a fetch
// that reads a fixture directory, so they prove these gates red and green
// without opening a port or touching the network.
async function get(url, { timeout = TIMEOUT, method = 'GET', fetch: impl = globalThis.fetch } = {}) {
  try {
    const res = await impl(url, { method, redirect: 'follow', signal: AbortSignal.timeout(timeout) });
    return { res };
  } catch (e) {
    return { error: `${url}: ${e?.cause?.code ?? e?.message ?? e}` };
  }
}

async function getText(url, opts) {
  const { res, error } = await get(url, opts);
  if (error) return { error: `could not check — ${error}` };
  if (!res.ok) return { error: `${url} answered HTTP ${res.status}`, status: res.status };
  return { text: await res.text() };
}

/** Streams a download through sha512 without holding it in memory. */
async function digest(url, opts) {
  const { res, error } = await get(url, { ...opts, timeout: 30 * 60_000 });
  if (error) return { error: `could not check — ${error}` };
  if (!res.ok) return { error: `${url} answered HTTP ${res.status}` };
  const h = createHash('sha512');
  let size = 0;
  for await (const chunk of res.body) {
    h.update(chunk);
    size += chunk.length;
  }
  return { sha512: h.digest('base64'), size };
}

/**
 * A running filex reports the release AND the commit it was built from:
 * `/api/capabilities` → "v0.44.2 (503fc90…, date)". The commit must be the
 * PUBLIC export commit the tag names — the images are built from the public
 * repository — so a server running a private build, or last release's image
 * with a re-tagged label, does not pass.
 */
export function runningRelease(name, baseUrl, opts = {}) {
  return {
    name,
    check: async (c) => {
      const url = `${baseUrl.replace(/\/+$/, '')}/api/capabilities`;
      const { text, error } = await getText(url, opts);
      if (error) return { ok: false, detail: error };
      let version;
      try {
        version = JSON.parse(text).version;
      } catch {
        return { ok: false, detail: `${url} did not answer JSON` };
      }
      const b = capabilitiesBuild(version);
      if (!b) return { ok: false, detail: `${url} reports "${version}", which is not a release build` };
      if (b.tag !== c.tag) return { ok: false, detail: `${url} runs ${b.tag}, not ${c.tag} — the deploy has not happened, or did not take` };
      if (c.exportHead && b.commit && !c.exportHead.startsWith(b.commit) && !b.commit.startsWith(c.exportHead)) {
        return { ok: false, detail: `${url} runs ${b.tag} built from ${b.commit}, but ${c.tag} is ${c.exportHead}` };
      }
      return { ok: true, detail: `${version}` };
    },
  };
}

/** The update manifest every AUTO_UPGRADE install reads names this release as its newest. */
export function updateManifest(name, url, opts = {}) {
  return {
    name,
    check: async (c) => {
      const { text, error } = await getText(url, opts);
      if (error) return { ok: false, detail: error };
      let doc;
      try {
        doc = JSON.parse(text);
      } catch {
        return { ok: false, detail: `${url} is not JSON` };
      }
      const newest = manifestNewest(doc);
      if (newest !== c.tag) {
        return { ok: false, detail: `${url} says the newest release is ${newest ?? 'nothing'}, not ${c.tag} — no install will be offered this release` };
      }
      return { ok: true, detail: `${url}: newest ${newest}` };
    },
  };
}

/**
 * Every desktop feed names this version, every file it offers is served, and
 * the served bytes hash to the sha512 the feed promises. `mustExist` names
 * files a feed does not list but a page links to (the portable .exe).
 */
export function desktopFeeds(name, baseUrl, feeds, { mustExist = [], bytes = true, ...opts } = {}) {
  return {
    name,
    check: async (c) => {
      const base = baseUrl.replace(/\/+$/, '');
      const problems = [];
      const lines = [];
      for (const feed of feeds) {
        const { text, error } = await getText(`${base}/${feed}`, opts);
        if (error) {
          problems.push(error);
          continue;
        }
        let parsed;
        try {
          parsed = parseFeed(text);
        } catch (e) {
          problems.push(`${feed}: ${e.message}`);
          continue;
        }
        if (parsed.version !== c.version) {
          problems.push(`${feed} offers ${parsed.version}, not ${c.version} — installed apps are told they are up to date`);
          continue;
        }
        for (const f of parsed.files) {
          if (!bytes) continue;
          const d = await digest(`${base}/${f.url}`, opts);
          if (d.error) problems.push(`${feed} → ${f.url}: ${d.error}`);
          else if (d.sha512 !== f.sha512) problems.push(`${feed} → ${f.url}: the served bytes do not hash to the sha512 the feed promises`);
          else if (Number.isFinite(f.size) && f.size !== d.size) problems.push(`${feed} → ${f.url}: ${d.size} bytes served, the feed says ${f.size}`);
          else lines.push(`${f.url} ${d.size} bytes, sha512 matches`);
        }
      }
      for (const f of mustExist) {
        const { res, error } = await get(`${base}/${f}`, { ...opts, method: 'HEAD' });
        if (error) problems.push(`could not check — ${error}`);
        else if (!res.ok) problems.push(`${base}/${f} answered HTTP ${res.status}`);
      }
      if (problems.length) return { ok: false, detail: problems.join('\n') };
      return { ok: true, detail: `${feeds.length} feed(s) name ${c.version}; ${lines.length} file(s) hash as promised`, log: lines.join('\n') };
    },
  };
}

/**
 * The docs site serves THIS release's pages: every heading the release added
 * to a published docs page is on the live page. Pages the site does not
 * publish (404, e.g. srcExclude) are listed and not counted. With no new
 * headings in the release there is nothing that could be stale, and the
 * Releases page is checked alone.
 */
export function docsSite(name, baseUrl, { dir = 'docs', ...opts } = {}) {
  return {
    name,
    check: async (c) => {
      const base = baseUrl.replace(/\/+$/, '');
      const problems = [];
      const notes = [];
      const rel = await getText(`${base}/RELEASES`, opts);
      if (rel.error) problems.push(rel.error);
      else if (!htmlText(rel.text).includes(c.tag)) problems.push(`${base}/RELEASES does not mention ${c.tag} — step 10 (the Releases page) has not reached the site`);

      if (!c.prevTag) return { ok: false, detail: 'no previous release tag to diff against' };
      const diff = git(c.repo, 'diff', '--no-color', '--unified=0', `${c.prevTag}..${c.releaseCommit}`, '--', `${dir}/*.md`, `${dir}/**/*.md`);
      if (diff.status !== 0) return { ok: false, detail: `git diff failed: ${diff.stderr}` };
      const probes = headingsAdded(diff.stdout).filter((h) => !/\/?RELEASES\.md$/.test(h.file));
      const byPage = new Map();
      for (const p of probes) byPage.set(p.file, [...(byPage.get(p.file) ?? []), p.text]);
      let checked = 0;
      for (const [file, headings] of byPage) {
        const url = docsPageUrl(base, file);
        const page = await getText(url, opts);
        if (page.status === 404) {
          notes.push(`${file}: not published (404) — not counted`);
          continue;
        }
        if (page.error) {
          problems.push(page.error);
          continue;
        }
        const text = htmlText(page.text);
        const missing = headings.filter((h) => !text.includes(h));
        checked += headings.length - missing.length;
        if (missing.length) {
          problems.push(
            `${url} is an OLD snapshot: ${missing.length} heading(s) this release added are not on it — ` +
              missing.slice(0, 5).map((m) => `"${m}"`).join(', ') +
              '\n  The site is built from a copy on the server, not from git: copy docs/ and docs-site/ from the EXPORT, then refresh (CONTRIBUTING step 11).',
          );
        }
      }
      if (problems.length) return { ok: false, detail: problems.join('\n'), log: notes.join('\n') };
      return {
        ok: true,
        detail: probes.length
          ? `${checked} new heading(s) on ${byPage.size} page(s) are live; RELEASES names ${c.tag}`
          : `RELEASES names ${c.tag}; this release added no headings to probe`,
        log: notes.join('\n'),
      };
    },
  };
}

/** Every picture README.md shows is in the tree. */
export function readmePictures(name = 'every picture README.md shows exists') {
  return {
    name,
    check: async (c) => {
      const readme = path.join(c.repo, 'README.md');
      if (!fs.existsSync(readme)) return { ok: false, detail: 'README.md is missing' };
      const imgs = readmeImages(fs.readFileSync(readme, 'utf8'));
      const missing = imgs.filter((p) => !fs.existsSync(path.join(c.repo, decodeURIComponent(p))));
      if (missing.length) return { ok: false, detail: `README.md shows ${missing.length} picture(s) that are not in the tree:\n${missing.join('\n')}` };
      return { ok: true, detail: `${imgs.length} picture(s), all present` };
    },
  };
}
