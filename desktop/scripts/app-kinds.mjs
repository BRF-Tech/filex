// The kinds of file the app store's apps open, for the packages that cannot
// learn them while they run: macOS (Info.plist), the Microsoft Store (the MSIX
// manifest) and the snap (its desktop file). See src/os-assoc.ts for why
// those three are fixed at build time and the others are not.
//
// ONE source, two steps:
//
//   refresh   reads the store's SIGNED index (<store>/v1/index.json + .sig,
//             verified with the index key build/app-kinds.json was made with),
//             the manifest of every app's newest version (held to the SHA-256
//             the index pins), and writes build/app-kinds.json: every
//             extension an app's `viewer` view opens. The release audit runs
//             `check`, which fails when the store's apps open kinds the file
//             does not have yet.
//   write     renders that file into electron-builder.yml (mac
//             fileAssociations, snapcraft.core24.mimeTypes) and
//             build/appx-extensions.xml, between the `app-kinds` markers -
//             run by `pnpm run build`, so every package is built from the file
//             and nothing between the markers is edited by hand
//             (desktop/test/app-kinds-build.test.ts fails when they differ).
//
//   node --experimental-strip-types desktop/scripts/app-kinds.mjs refresh [--globs <globs2> --globs-label "<text>"] [--trust-key <hex>]
//   node --experimental-strip-types desktop/scripts/app-kinds.mjs write
//   node --experimental-strip-types desktop/scripts/app-kinds.mjs check
//
// `--globs`: a shared-mime-info globs2 file (/usr/share/mime/globs2 on a Linux
// machine) - which media type a desktop gives each extension, all a snap's
// desktop file can name. Without it the types already in the file are kept,
// and a new extension gets none (the snap does not declare it) until a
// refresh on Linux.
//
// The office documents (src/openwith.ts OFFICE_EXTENSIONS) are declared by
// hand beside these and are not repeated; programs and shortcuts
// (src/app-kinds.ts NEVER_REGISTERED) are never declared.

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { KIND_RE, MEDIA_TYPE_RE } from '../src/openwith.ts';
import { packageKinds, parseGlobs2, typeHintsOf, typesOfKind } from '../src/os-assoc.ts';

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const DESKTOP_DIR = path.join(HERE, '..');
export const SNAPSHOT_FILE = path.join(DESKTOP_DIR, 'build', 'app-kinds.json');
export const BUILDER_YML = path.join(DESKTOP_DIR, 'electron-builder.yml');
export const APPX_XML = path.join(DESKTOP_DIR, 'build', 'appx-extensions.xml');

export const DEFAULT_STORE = 'https://apps.filex.sh';

// ── the store ───────────────────────────────────────────────────────

/** An ed25519 public key (32 bytes, hex) as node:crypto takes it. */
function ed25519Key(hex) {
  const raw = Buffer.from(String(hex), 'hex');
  if (raw.length !== 32) throw new Error('an ed25519 key is 32 bytes (64 hex characters)');
  return crypto.createPublicKey({ key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), raw]), format: 'der', type: 'spki' });
}

/**
 * The store's index signature, checked as filex checks it (backend
 * appstore/catalog.go verifyIndex): ed25519 over the LOWER-HEX SHA-256 of the
 * bytes as served; the signature hex or base64.
 */
export function verifyIndex(body, sigText, keyHex) {
  const s = String(sigText).trim();
  const sig = /^[0-9a-f]+$/i.test(s) ? Buffer.from(s, 'hex') : Buffer.from(s, 'base64');
  const sha = crypto.createHash('sha256').update(body).digest('hex');
  return crypto.verify(null, Buffer.from(sha), ed25519Key(keyHex), sig);
}

/**
 * The version of each app whose manifest says what it opens: an app (not a
 * language pack or a storage plugin) that is not revoked, its newest version
 * that is not yanked, with an interface (`ui`).
 */
export function catalogVersions(index) {
  const out = [];
  for (const a of Array.isArray(index?.apps) ? index.apps : []) {
    if (!a || a.kind !== 'app' || a.revoked) continue;
    const versions = Array.isArray(a.versions) ? a.versions.filter((v) => v && !v.yanked) : [];
    const v = versions.find((x) => x.version === a.latest) ?? versions[0];
    if (!v || !v.ui || !v.manifest?.url || !/^[0-9a-f]{64}$/.test(String(v.manifest.sha256 ?? ''))) continue;
    out.push({ name: String(a.name), version: String(v.version), manifest: { url: String(v.manifest.url), sha256: String(v.manifest.sha256) } });
  }
  return out.sort((x, y) => (x.name < y.name ? -1 : x.name > y.name ? 1 : 0));
}

/** The extensions and media types a manifest's `viewer` views with an interface open. */
export function viewerKindsOf(manifest) {
  const ext = new Set();
  const mime = new Set();
  for (const v of Array.isArray(manifest?.views) ? manifest.views : []) {
    if (!v || v.placement !== 'viewer' || !v.ui) continue;
    for (const e of Array.isArray(v.applies?.ext) ? v.applies.ext : []) {
      const k = String(e).toLowerCase();
      if (KIND_RE.test(k)) ext.add(k);
    }
    for (const m of Array.isArray(v.applies?.mime) ? v.applies.mime : []) {
      const t = String(m).toLowerCase();
      if (MEDIA_TYPE_RE.test(t)) mime.add(t);
    }
  }
  return { ext: [...ext].sort(), mime: [...mime].sort() };
}

/**
 * build/app-kinds.json from what was read: every extension an app opens,
 * with the apps that open it and the media types a desktop gives it
 * (`globs`, a parseGlobs2 map, narrowed to the ones the apps name in their
 * own `mime` - src/os-assoc.ts typesOfKind, the rule the running app keeps;
 * without a globs file the previous file's are kept, and a new extension's
 * are `null` - unknown).
 */
export function snapshotOf({ store, indexKey, serial, apps, previous = null, globs = null, globsLabel = null }) {
  const byExt = new Map();
  for (const a of apps) {
    for (const e of a.ext) {
      const row = byExt.get(e) ?? { ext: e, apps: [] };
      if (!row.apps.includes(a.name)) row.apps.push(a.name);
      byExt.set(e, row);
    }
  }
  const prevLinux = new Map((previous?.kinds ?? []).map((k) => [k.ext, k.linux ?? null]));
  const hints = typeHintsOf(apps);
  const kinds = [...byExt.values()]
    .sort((x, y) => (x.ext < y.ext ? -1 : x.ext > y.ext ? 1 : 0))
    .map((k) => ({
      ext: k.ext,
      apps: k.apps.sort(),
      linux: globs ? typesOfKind(globs.get(k.ext)?.types ?? [], hints[k.ext]).sort() : (prevLinux.get(k.ext) ?? null),
    }));
  return {
    about:
      'The kinds of file the app store\'s apps open, for the packages that declare them at build time (macOS, the Microsoft Store, the snap). ' +
      'Written by `pnpm --dir desktop app-kinds:refresh` (scripts/app-kinds.mjs); edit nothing here by hand.',
    store,
    index_key: indexKey,
    serial,
    linux_types_from: globs ? (globsLabel ?? 'a globs2 file') : (previous?.linux_types_from ?? null),
    apps: apps.map((a) => ({ name: a.name, version: a.version })),
    kinds,
  };
}

// ── what the packages declare ───────────────────────────────────────

/**
 * The extensions a package declares beside the office documents: one-part
 * (Windows and macOS type a file by its last extension), never a program
 * (src/os-assoc.ts packageKinds - the app counts them for Settings with the
 * same rule).
 */
export function staticKinds(snapshot) {
  return packageKinds((snapshot?.kinds ?? []).map((k) => k.ext));
}

/**
 * The media types the snap's desktop file declares: the ones a desktop gives
 * those extensions. ⚠ A snap cannot define a type of its own (snapd installs
 * no shared-mime-info package from a snap), so an extension the system has
 * no type for is not in a snap's "Open with" at all.
 */
export function snapMediaTypes(snapshot) {
  const keep = new Set(staticKinds(snapshot));
  const out = new Set();
  for (const k of snapshot?.kinds ?? []) {
    if (!keep.has(k.ext) || !Array.isArray(k.linux)) continue;
    for (const t of k.linux) if (MEDIA_TYPE_RE.test(t) && !t.endsWith('/*')) out.add(t);
  }
  return [...out].sort();
}

const NOTE = 'generated by scripts/app-kinds.mjs from build/app-kinds.json, do not edit';

/** The marker pair a region sits between (a comment in the file's own syntax). */
export const REGIONS = {
  mac: { begin: `    # >>> app-kinds: mac (${NOTE})`, end: '    # <<< app-kinds: mac' },
  snap: { begin: `    # >>> app-kinds: snap (${NOTE})`, end: '    # <<< app-kinds: snap' },
  store: { begin: `        <!-- >>> app-kinds: store (${NOTE}) -->`, end: '        <!-- <<< app-kinds: store -->' },
};

/** mac.fileAssociations' second entry: the store's kinds, rank Alternate like the office one. */
export function renderMac(kinds) {
  if (!kinds.length) return [];
  // Quoted: an extension like `001` is a number to YAML.
  return ['    - ext:', ...kinds.map((k) => `        - "${k}"`), '      name: Document', '      role: Editor', '      rank: Alternate'];
}

/** snapcraft.core24.mimeTypes: merged with linux.mimeTypes by electron-builder (arrays concatenate). */
export function renderSnap(types) {
  if (!types.length) return [];
  return ['    mimeTypes:', ...types.map((t) => `      - ${t}`)];
}

/** The Store package's second file-type association. ⚠ No double hyphen in it: makeappx refuses the manifest. */
export function renderStore(kinds) {
  if (!kinds.length) return [];
  return [
    '        <uap3:Extension Category="windows.fileTypeAssociation">',
    '          <uap3:FileTypeAssociation Name="filex.apps" Parameters="&quot;%1&quot;">',
    '            <uap:DisplayName>filex</uap:DisplayName>',
    '            <uap:SupportedFileTypes>',
    ...kinds.map((k) => `              <uap:FileType>.${k}</uap:FileType>`),
    '            </uap:SupportedFileTypes>',
    '          </uap3:FileTypeAssociation>',
    '        </uap3:Extension>',
  ];
}

/** `text` with the lines between `region`'s markers replaced by `body`. */
export function replaceRegion(text, region, body) {
  const nl = text.includes('\r\n') ? '\r\n' : '\n';
  const lines = text.split(/\r?\n/);
  const b = lines.indexOf(region.begin);
  const e = lines.indexOf(region.end);
  if (b < 0 || e < 0 || e < b) throw new Error(`the markers of the "${region.begin.trim()}" region are missing or out of order`);
  return [...lines.slice(0, b + 1), ...body, ...lines.slice(e)].join(nl);
}

/** electron-builder.yml and appx-extensions.xml as `snapshot` makes them. */
export function render(snapshot, yml, xml) {
  const kinds = staticKinds(snapshot);
  let y = replaceRegion(yml, REGIONS.mac, renderMac(kinds));
  y = replaceRegion(y, REGIONS.snap, renderSnap(snapMediaTypes(snapshot)));
  return { yml: y, xml: replaceRegion(xml, REGIONS.store, renderStore(kinds)) };
}

// ── the command line ────────────────────────────────────────────────

function readSnapshot() {
  return JSON.parse(fs.readFileSync(SNAPSHOT_FILE, 'utf8'));
}

async function fetchBytes(url) {
  const res = await fetch(url, { redirect: 'follow' });
  if (!res.ok) throw new Error(`${url}: ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}

/** Reads the store and returns the snapshot it makes (refresh, check). */
async function readStore({ store, keyHex, previous, globs, globsLabel }) {
  const body = await fetchBytes(`${store}/v1/index.json`);
  const sig = (await fetchBytes(`${store}/v1/index.json.sig`)).toString('utf8');
  if (!verifyIndex(body, sig, keyHex)) {
    throw new Error(`the index of ${store} does not verify with the key ${keyHex.slice(0, 12)}... - if the store rotated its index key, check the new one and pass --trust-key <hex>`);
  }
  const index = JSON.parse(body.toString('utf8'));
  const apps = [];
  for (const v of catalogVersions(index)) {
    const bytes = await fetchBytes(v.manifest.url);
    const sha = crypto.createHash('sha256').update(bytes).digest('hex');
    if (sha !== v.manifest.sha256) throw new Error(`${v.name} ${v.version}: the manifest is not the one the index pins (${sha})`);
    apps.push({ name: v.name, version: v.version, ...viewerKindsOf(JSON.parse(bytes.toString('utf8'))) });
  }
  return snapshotOf({ store, indexKey: keyHex, serial: index.serial ?? null, apps, previous, globs, globsLabel });
}

function argOf(argv, name) {
  const i = argv.indexOf(name);
  return i >= 0 ? argv[i + 1] : undefined;
}

/** `refresh` / `write` / `check`. Returns the process exit code. */
export async function run(argv) {
  const cmd = argv[0];
  if (cmd === 'write') {
    const out = render(readSnapshot(), fs.readFileSync(BUILDER_YML, 'utf8'), fs.readFileSync(APPX_XML, 'utf8'));
    for (const [file, text] of [[BUILDER_YML, out.yml], [APPX_XML, out.xml]]) {
      if (fs.readFileSync(file, 'utf8') !== text) {
        fs.writeFileSync(file, text);
        console.log(`app-kinds: wrote ${path.relative(DESKTOP_DIR, file)}`);
      }
    }
    return 0;
  }
  if (cmd === 'refresh' || cmd === 'check') {
    const previous = readSnapshot();
    const store = argOf(argv, '--store') ?? previous.store ?? DEFAULT_STORE;
    const keyHex = argOf(argv, '--trust-key') ?? previous.index_key;
    const globsFile = argOf(argv, '--globs');
    const globs = globsFile ? parseGlobs2(fs.readFileSync(globsFile, 'utf8')) : null;
    let next;
    try {
      next = await readStore({ store, keyHex, previous, globs, globsLabel: argOf(argv, '--globs-label') ?? globsFile });
    } catch (err) {
      const msg = String(err?.message ?? err);
      // `check` runs in the release audit: a store that cannot be reached says
      // nothing about the file, and must not stop a release.
      if (cmd === 'check' && !/does not verify|not the one the index pins/.test(msg)) {
        console.log(`app-kinds: the store could not be read (${msg}) - build/app-kinds.json is not checked this time`);
        return 0;
      }
      console.error(`app-kinds: ${msg}`);
      return 1;
    }
    if (cmd === 'check') {
      const have = JSON.stringify(previous.kinds.map((k) => [k.ext, k.apps]));
      const want = JSON.stringify(next.kinds.map((k) => [k.ext, k.apps]));
      if (have === want) {
        console.log(`app-kinds: build/app-kinds.json holds the store's ${next.kinds.length} kinds`);
        return 0;
      }
      console.error('app-kinds: the store\'s apps open other kinds than build/app-kinds.json says. Run:');
      console.error('    pnpm --dir desktop app-kinds:refresh && pnpm --dir desktop app-kinds');
      console.error('and commit build/app-kinds.json, electron-builder.yml and build/appx-extensions.xml.');
      return 1;
    }
    fs.writeFileSync(SNAPSHOT_FILE, `${JSON.stringify(next, null, 2)}\n`);
    const unknown = next.kinds.filter((k) => k.linux === null).map((k) => k.ext);
    console.log(`app-kinds: ${next.kinds.length} kinds from ${next.apps.length} apps (index serial ${next.serial})`);
    if (unknown.length) console.log(`app-kinds: no media types known for ${unknown.join(', ')} - refresh on Linux with --globs /usr/share/mime/globs2 for the snap`);
    return 0;
  }
  console.error('usage: app-kinds.mjs refresh [--globs <file> --globs-label <text>] [--store <url>] [--trust-key <hex>] | write | check');
  return 2;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  run(process.argv.slice(2)).then((code) => process.exit(code), (err) => {
    console.error(err);
    process.exit(1);
  });
}
