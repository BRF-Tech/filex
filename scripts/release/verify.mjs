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
  htmlText,
  manifestNewest,
  newHeadings,
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
 *
 * A page's new headings are read from the whole page at both releases
 * (newHeadings), not from the diff: a diff line cannot tell a heading from a
 * `#` comment inside a code block, and v0.50.0's deploy was held red by one
 * (lesson #964). A page the previous release did not have is new throughout;
 * a page the release deleted has nothing to probe.
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
      const changed = git(c.repo, 'diff', '--name-only', '-z', '--no-renames', '--diff-filter=d', `${c.prevTag}..${c.releaseCommit}`, '--', `${dir}/*.md`, `${dir}/**/*.md`);
      if (changed.status !== 0) return { ok: false, detail: `git diff failed: ${changed.stderr}` };
      const byPage = new Map();
      for (const file of changed.stdout.split('\0').filter((f) => f && !/\/?RELEASES\.md$/.test(f))) {
        const now = git(c.repo, 'show', `${c.releaseCommit}:${file}`);
        if (now.status !== 0) return { ok: false, detail: `could not read ${file} at ${c.releaseCommit}: ${now.stderr.trim()}` };
        const was = git(c.repo, 'show', `${c.prevTag}:${file}`);
        const added = newHeadings(was.status === 0 ? was.stdout : '', now.stdout);
        if (added.length) byPage.set(file, added);
      }
      const probes = [...byPage.values()].flat();
      let checked = 0;
      let live = 0;
      const unpublished = [];
      for (const [file, headings] of byPage) {
        const url = docsPageUrl(base, file);
        const page = await getText(url, opts);
        if (page.status === 404) {
          unpublished.push(file);
          notes.push(`${file}: not published (404) — not counted`);
          continue;
        }
        if (page.error) {
          problems.push(page.error);
          continue;
        }
        live += 1;
        // Whitespace is squeezed out of both sides: htmlText turns every tag
        // into a space, so a heading with inline code (MCP.md's "403 Forbidden
        // (session_required)" in backticks) reads "( session_required )" on the page and
        // was reported missing while it was there (v0.49.0, the first release
        // to add such headings).
        const squash = (s) => s.replace(/\s+/g, '');
        const text = squash(htmlText(page.text));
        const missing = headings.filter((h) => !text.includes(squash(h)));
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
      // Only the pages that answered count as live: a page the site does not
      // publish proves nothing either way, and is named apart, not added in.
      const skipped = unpublished.length
        ? `; ${unpublished.length} page(s) with new headings are not published (404) and not counted: ${unpublished.join(', ')}`
        : '';
      return {
        ok: true,
        detail: probes.length
          ? `${checked} new heading(s) on ${live} page(s) are live${skipped}; RELEASES names ${c.tag}`
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

/**
 * The Windows update feed offers BOTH installers, x64 first.
 *
 * ⚠ electron-updater reads one latest.yml on Windows whatever the CPU and
 * takes the file whose name carries `process.arch`, else the first one (older
 * updaters: always the first). A feed with the arm64 installer first, or with
 * `path` naming it, hands every existing x64 install an installer it cannot
 * run; a feed without it leaves Windows on Arm on the x64 build under
 * emulation. release.yml joins the two (merge-latest-yml.mjs); this reads
 * the result back where the apps read it.
 */
export function windowsFeedArches(name, url, opts = {}) {
  return {
    name,
    check: async (c) => {
      const { text, error } = await getText(url, opts);
      if (error) return { ok: false, detail: error };
      let feed;
      try {
        feed = parseFeed(text);
      } catch (e) {
        return { ok: false, detail: `${url}: ${e.message}` };
      }
      const urls = feed.files.map((f) => f.url);
      const top = /^path:\s*['"]?([^'"\s]+)['"]?\s*$/m.exec(text)?.[1] ?? null;
      const problems = [];
      if (feed.version !== c.version) problems.push(`offers ${feed.version}, not ${c.version}`);
      if (!/(^|[-_.])x64([-_.]|$)/.test(urls[0] ?? '')) problems.push(`the first installer is ${urls[0]}, not the x64 one — existing x64 installs would be offered it`);
      if (!urls.some((u) => /(^|[-_.])arm64([-_.]|$)/.test(u))) problems.push('no arm64 installer — Windows on Arm keeps the x64 build');
      if (top && top !== urls[0]) problems.push(`path: names ${top}, not the first installer ${urls[0]}`);
      return problems.length ? { ok: false, detail: `${url}: ${problems.join('; ')}` } : { ok: true, detail: urls.join(' + ') };
    },
  };
}

/**
 * A snap's revision for THIS version is on a channel for every architecture
 * (api.snapcraft.io, no account needed). Each release uploads one revision
 * per architecture; a job that failed or skipped leaves that architecture's
 * users on the previous version, and nothing else says so.
 */
export function snapChannel(name, snap, arches, { channel = 'stable', ...opts } = {}) {
  return {
    name,
    check: async (c) => {
      const url = `https://api.snapcraft.io/v2/snaps/info/${snap}?fields=version,revision`;
      const impl = opts.fetch ?? globalThis.fetch;
      let doc;
      try {
        const res = await impl(url, { headers: { 'Snap-Device-Series': '16' }, signal: AbortSignal.timeout(opts.timeout ?? TIMEOUT) });
        if (!res.ok) return { ok: false, detail: `${url} answered HTTP ${res.status}` };
        doc = await res.json();
      } catch (e) {
        return { ok: false, detail: `could not check — ${url}: ${e?.cause?.code ?? e?.message ?? e}` };
      }
      const map = doc?.['channel-map'] ?? [];
      const lines = [];
      const problems = [];
      for (const arch of arches) {
        const entry = map.find((m) => m?.channel?.name === channel && m?.channel?.architecture === arch);
        if (!entry) problems.push(`${channel}/${arch}: no revision`);
        else if (entry.version !== c.version) problems.push(`${channel}/${arch}: ${entry.version} (revision ${entry.revision}), not ${c.version}`);
        else lines.push(`${channel}/${arch}: ${entry.version} (revision ${entry.revision})`);
      }
      return problems.length ? { ok: false, detail: problems.join('\n') } : { ok: true, detail: lines.join(', ') };
    },
  };
}

/**
 * The Microsoft Store offers THIS version's bundle, for every architecture,
 * to anyone (displaycatalog.mp.microsoft.com, no account needed). It changes
 * only once certification passes — hours to three working days after the
 * submission — so a release that went through its own run reads the run
 * instead (plan.mjs storeBundle); this is what a release submitted outside
 * that run (by hand in Partner Center, 0.52.0) is checked against.
 * `storeVersion` is the Store's version for the release (1.0.5200.0 for 0.52.0,
 * desktop/scripts/appx-manifest.cjs).
 */
export async function storeListing(productId, identity, storeVersion, arches, opts = {}) {
  const url = `https://displaycatalog.mp.microsoft.com/v7.0/products?bigIds=${productId}&market=US&languages=en-US`;
  const impl = opts.fetch ?? globalThis.fetch;
  let doc;
  try {
    const res = await impl(url, { signal: AbortSignal.timeout(opts.timeout ?? TIMEOUT) });
    if (!res.ok) return { ok: false, detail: `${url} answered HTTP ${res.status}` };
    doc = await res.json();
  } catch (e) {
    return { ok: false, detail: `could not check — ${url}: ${e?.cause?.code ?? e?.message ?? e}` };
  }
  const packages = (doc?.Products ?? [])
    .flatMap((p) => p?.DisplaySkuAvailabilities ?? [])
    .flatMap((d) => d?.Sku?.Properties?.Packages ?? []);
  const prefix = `${identity}_${storeVersion}_`;
  const offered = packages.filter((p) => String(p?.PackageFullName ?? '').startsWith(prefix));
  if (!offered.length) {
    const seen = [...new Set(packages.map((p) => String(p?.PackageFullName ?? '').split('_')[1]).filter(Boolean))];
    return { ok: false, detail: `the Store offers ${identity} ${seen.join(', ') || 'nothing'}, not ${storeVersion} (certification can take up to three working days)` };
  }
  const has = new Set(offered.flatMap((p) => p?.Architectures ?? []));
  const missing = arches.filter((a) => !has.has(a));
  if (missing.length) return { ok: false, detail: `the Store offers ${identity} ${storeVersion} without ${missing.join(', ')}` };
  return { ok: true, detail: `the Store offers ${identity} ${storeVersion} (${arches.join(' + ')})` };
}
