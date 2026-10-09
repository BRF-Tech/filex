// The release an installed copy updates FROM, and its desktop packages.
//
//   node previous-release.mjs --repo <owner/name> --below <version> --out <dir> [--asset <name>]...
//
// Picks the newest published release (no draft, no pre-release) whose version
// is lower than --below, writes its version to <dir>/version, and downloads
// each --asset that release has into <dir>. An asset the release does not
// have is named on stdout ("missing <name>") and skipped: older releases have
// fewer packages (no arm64 before 0.48.1), and the caller decides what that
// means. Exit 1 when there is no such release or GitHub cannot be read: a
// check that cannot find what to update from must not pass as one that did.
//
// ⚠ Lower than THIS build's version, not "the latest release": in a tag run
// the Release of the tag being built exists already (the binaries job makes
// it), and a dry run of a branch still carries the version of the last
// release (desktop/package.json is bumped by the release): it then updates
// from the one before, which is still an update made by this build.
//
// Reads GH_TOKEN or GITHUB_TOKEN when set (API rate limit); the downloads are
// the public Release URLs and carry no token.
import fs from 'node:fs';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';

/** {major, minor, patch, pre} of "v1.2.3" / "1.2.3-rc.1", or null. */
export function parseVersion(v) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/.exec(String(v ?? '').trim());
  if (!m) return null;
  return { major: Number(m[1]), minor: Number(m[2]), patch: Number(m[3]), pre: m[4] ?? '' };
}

/** Semver order: <0, 0, >0. A pre-release sorts below its release. */
export function compareVersions(a, b) {
  const x = parseVersion(a);
  const y = parseVersion(b);
  if (!x || !y) throw new Error(`not a version: ${!x ? a : b}`);
  for (const k of ['major', 'minor', 'patch']) if (x[k] !== y[k]) return x[k] - y[k];
  if (x.pre === y.pre) return 0;
  if (!x.pre) return 1;
  if (!y.pre) return -1;
  const p = x.pre.split('.');
  const q = y.pre.split('.');
  for (let i = 0; i < Math.max(p.length, q.length); i++) {
    if (p[i] === undefined) return -1;
    if (q[i] === undefined) return 1;
    const pn = /^\d+$/.test(p[i]);
    const qn = /^\d+$/.test(q[i]);
    if (pn && qn && Number(p[i]) !== Number(q[i])) return Number(p[i]) - Number(q[i]);
    if (pn !== qn) return pn ? -1 : 1;
    if (p[i] !== q[i]) return p[i] < q[i] ? -1 : 1;
  }
  return 0;
}

/**
 * The newest published release lower than `below`, from GitHub's list
 * (objects with tag_name, draft, prerelease, assets). null when none is.
 */
export function previousRelease(releases, below) {
  if (!parseVersion(below)) throw new Error(`not a version: ${below}`);
  const older = releases.filter((r) => !r.draft && !r.prerelease && parseVersion(r.tag_name) && compareVersions(r.tag_name, below) < 0);
  older.sort((a, b) => compareVersions(b.tag_name, a.tag_name));
  return older[0] ?? null;
}

async function listReleases(repo) {
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || '';
  const headers = { accept: 'application/vnd.github+json', 'user-agent': 'filex-release' };
  if (token) headers.authorization = `Bearer ${token}`;
  const all = [];
  for (let page = 1; page <= 3; page++) {
    const r = await fetch(`https://api.github.com/repos/${repo}/releases?per_page=100&page=${page}`, { headers });
    if (!r.ok) throw new Error(`GitHub answered ${r.status} for the releases of ${repo}`);
    const list = await r.json();
    all.push(...list);
    if (list.length < 100) break;
  }
  return all;
}

async function download(asset, dir) {
  const dest = path.join(dir, asset.name);
  const r = await fetch(asset.browser_download_url, { redirect: 'follow', headers: { 'user-agent': 'filex-release' } });
  if (!r.ok || !r.body) throw new Error(`${asset.name}: HTTP ${r.status}`);
  await pipeline(Readable.fromWeb(r.body), fs.createWriteStream(dest));
  const size = fs.statSync(dest).size;
  if (asset.size && size !== asset.size) throw new Error(`${asset.name}: ${size} bytes, the Release says ${asset.size}`);
  return dest;
}

function parse(argv) {
  const o = { assets: [] };
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    const v = argv[++i];
    if (v === undefined) throw new Error(`${k} needs a value`);
    if (k === '--repo') o.repo = v;
    else if (k === '--below') o.below = v;
    else if (k === '--out') o.out = v;
    else if (k === '--asset') o.assets.push(v);
    else throw new Error(`unknown argument ${k}`);
  }
  if (!o.repo || !o.below || !o.out) throw new Error('usage: node previous-release.mjs --repo <owner/name> --below <version> --out <dir> [--asset <name>]...');
  return o;
}

async function main(argv) {
  const o = parse(argv);
  const prev = previousRelease(await listReleases(o.repo), o.below);
  if (!prev) {
    console.error(`${o.repo} has no published release below ${o.below}`);
    return 1;
  }
  fs.mkdirSync(o.out, { recursive: true });
  const version = prev.tag_name.replace(/^v/, '');
  fs.writeFileSync(path.join(o.out, 'version'), version);
  console.log(`previous release: ${prev.tag_name} (this build: ${o.below})`);
  for (const name of o.assets) {
    const asset = (prev.assets ?? []).find((a) => a.name === name);
    if (!asset) {
      console.log(`missing ${name}`);
      continue;
    }
    const dest = await download(asset, o.out);
    console.log(`downloaded ${name} (${fs.statSync(dest).size} bytes)`);
  }
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(String(e?.message ?? e));
      process.exit(1);
    },
  );
}
