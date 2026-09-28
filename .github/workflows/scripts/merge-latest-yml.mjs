// One Windows update feed for every architecture.
//
//   node merge-latest-yml.mjs --out <latest.yml> <feed.yml> [<feed.yml>...]
//
// ⚠⚠ Why: electron-updater reads a SINGLE `latest.yml` on Windows, whatever
// the CPU (Linux gets `latest-linux-arm64.yml` of its own; Windows does not).
// Each electron-builder run writes a `latest.yml` naming only the installer it
// just built, so an x64 build followed by an arm64 build leaves the arm64 one:
// every x64 install would then be offered an arm64 installer. This joins the
// feeds instead.
//
// How the app picks from the joined list (electron-updater 6.x, Provider.js
// findFile): the first `.exe` whose URL contains `process.arch` ("x64",
// "arm64"), else the FIRST `.exe`. Older updaters only ever took the first.
// So the x64 installer always comes first, and the top-level `path`/`sha512`
// (what the oldest updaters read) stay the x64 installer's — an install that
// predates arm64 builds sees exactly the feed it saw before.
//
// Rules, each an error rather than a guess:
//   - every feed names the same version;
//   - a file named twice keeps its FIRST position and its LAST content (a
//     rebuilt arm64 installer replaces the old entry, it does not duplicate it);
//   - the result contains an x64 installer, first.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

function scalar(raw) {
  const v = raw.trim();
  if ((v.startsWith("'") && v.endsWith("'")) || (v.startsWith('"') && v.endsWith('"'))) return v.slice(1, -1).replace(/''/g, "'");
  if (/^-?\d+$/.test(v)) return Number(v);
  if (v === 'true' || v === 'false') return v === 'true';
  return v;
}

/**
 * Parses the flat shape electron-builder writes (top-level scalars plus a
 * `files:` list of flat maps). Anything else is refused rather than
 * half-read: a key this does not understand would be silently dropped from
 * the feed every installed app reads.
 */
export function parseFeed(text) {
  const out = { files: [] };
  let inFiles = false;
  let cur = null;
  for (const line of text.split(/\r?\n/)) {
    if (!line.trim() || /^\s*#/.test(line)) continue;
    let m;
    if ((m = /^([A-Za-z][\w-]*):\s*(.*)$/.exec(line))) {
      inFiles = m[1] === 'files' && m[2].trim() === '';
      cur = null;
      if (!inFiles) out[m[1]] = scalar(m[2]);
      continue;
    }
    if (inFiles && (m = /^\s+-\s+([A-Za-z][\w-]*):\s*(.*)$/.exec(line))) {
      cur = { [m[1]]: scalar(m[2]) };
      out.files.push(cur);
      continue;
    }
    if (inFiles && cur && (m = /^\s+([A-Za-z][\w-]*):\s*(.*)$/.exec(line))) {
      cur[m[1]] = scalar(m[2]);
      continue;
    }
    throw new Error(`unexpected line in feed: ${JSON.stringify(line)}`);
  }
  if (!out.version) throw new Error('feed has no version');
  if (!out.files.length) throw new Error('feed lists no files');
  for (const f of out.files) if (!f.url || !f.sha512) throw new Error(`feed entry without url/sha512: ${JSON.stringify(f)}`);
  return out;
}

function yamlScalar(v) {
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  const s = String(v);
  // Quoted like electron-builder quotes a date, and whenever YAML would read
  // the bare text as something other than this string.
  const plain = /^[\w./+=-]+$/.test(s) && !/^(true|false|null|~|yes|no|on|off)$/i.test(s) && !/^-?\d+(\.\d+)?([eE][-+]?\d+)?$/.test(s);
  return plain ? s : `'${s.replace(/'/g, "''")}'`;
}

export function renderFeed(feed) {
  const lines = [`version: ${yamlScalar(feed.version)}`, 'files:'];
  for (const f of feed.files) {
    const [first, ...rest] = Object.keys(f);
    lines.push(`  - ${first}: ${yamlScalar(f[first])}`);
    for (const k of rest) lines.push(`    ${k}: ${yamlScalar(f[k])}`);
  }
  for (const [k, v] of Object.entries(feed)) {
    if (k === 'version' || k === 'files') continue;
    lines.push(`${k}: ${yamlScalar(v)}`);
  }
  return `${lines.join('\n')}\n`;
}

const isX64 = (url) => /(^|[-_.])(x64|amd64|x86_64)([-_.]|$)/i.test(url);

export function mergeFeeds(feeds) {
  if (!feeds.length) throw new Error('nothing to merge');
  const versions = [...new Set(feeds.map((f) => String(f.version)))];
  if (versions.length > 1) throw new Error(`the feeds name different versions: ${versions.join(', ')}`);
  // First position wins, last content wins.
  const order = [];
  for (const feed of feeds) for (const f of feed.files) if (!order.includes(f.url)) order.push(f.url);
  const lastContent = new Map();
  for (const feed of feeds) for (const f of feed.files) lastContent.set(f.url, { ...f });
  let files = order.map((u) => lastContent.get(u));
  const x64 = files.filter((f) => isX64(f.url));
  if (!x64.length) throw new Error(`no x64 installer among ${files.map((f) => f.url).join(', ')} — every existing install is x64 and reads this feed`);
  files = [...x64, ...files.filter((f) => !isX64(f.url))];
  const primary = x64[0];
  // Top-level keys from the feed that carried the x64 installer, so the
  // fields the oldest updaters read keep naming it.
  const base = feeds.find((f) => f.files.some((x) => x.url === primary.url)) ?? feeds[0];
  const out = { version: base.version, files };
  for (const [k, v] of Object.entries(base)) if (!(k in out)) out[k] = v;
  if ('path' in out) out.path = primary.url;
  if ('sha512' in out) out.sha512 = primary.sha512;
  return out;
}

function main(argv) {
  const i = argv.indexOf('--out');
  if (i < 0 || !argv[i + 1]) {
    console.error('usage: node merge-latest-yml.mjs --out <latest.yml> <feed.yml>...');
    return 2;
  }
  const outFile = argv[i + 1];
  const inputs = argv.filter((_, j) => j !== i && j !== i + 1);
  if (!inputs.length) {
    console.error('no input feeds');
    return 2;
  }
  const merged = mergeFeeds(inputs.map((f) => parseFeed(fs.readFileSync(f, 'utf8'))));
  fs.mkdirSync(path.dirname(path.resolve(outFile)), { recursive: true });
  fs.writeFileSync(outFile, renderFeed(merged));
  console.log(`${outFile}: ${merged.version}, ${merged.files.map((f) => f.url).join(' + ')}`);
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (e) {
    console.error(String(e?.message ?? e));
    process.exit(1);
  }
}
