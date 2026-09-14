/**
 * fileIcons — extension/type → inline SVG icon mapping.
 *
 * Replaces the old emoji maps in GridView/ListView with a minimalist
 * line-icon set. Icons are hand-drawn 24×24 strokes on `currentColor`;
 * each family gets an accent via `--fe-icon-<family>` (variables.css),
 * applied through the `fe-ficon--<family>` class in base.css.
 *
 * Scope: what a listing row IS — its file type, plus the one STATE that
 * changes the picture rather than the caption (an encrypted folder, bottom of
 * this file). What a row DOES is `lib/actionIcons.ts`.
 *
 * ⚠ `.trash` and STORAGE rows used to be the documented exception here, and
 * they were drawn as 💾 and 🗑 by three separate copies of the same line in
 * GridView, GalleryView and ListView. On the multi-storage "My files" root —
 * the screen the sidebar now lands everybody on — that made a pixelated
 * floppy disk the first thing on the page, in a shape that changes between
 * Windows, macOS and Linux because it is a system font's idea of the glyph
 * rather than ours. They are families like any other now, so the card, the
 * tile and the row all get one definition (filex lesson #67).
 */

export type IconFamily =
  | 'folder'
  | 'image'
  | 'video'
  | 'audio'
  | 'pdf'
  | 'doc'
  | 'sheet'
  | 'slides'
  | 'archive'
  | 'code'
  | 'text'
  // Not file types: the two rows a listing can hold that are PLACES rather
  // than things. They carry no extension, so `iconFamilyFor` recognises them
  // from the node instead.
  | 'storage'
  | 'trash'
  | 'unknown';

const EXT_FAMILIES: Record<string, IconFamily> = {};

function reg(family: IconFamily, exts: string[]) {
  for (const e of exts) EXT_FAMILIES[e] = family;
}

reg('image', ['jpg', 'jpeg', 'png', 'webp', 'gif', 'bmp', 'avif', 'heic', 'svg', 'ico', 'tiff', 'tif']);
reg('video', ['mp4', 'webm', 'mov', 'mkv', 'avi', 'ogv', 'm4v']);
reg('audio', ['mp3', 'wav', 'flac', 'ogg', 'm4a', 'aac', 'opus']);
reg('pdf', ['pdf']);
reg('doc', ['doc', 'docx', 'odt', 'rtf']);
/* gorunum:v1-preview — `tsv` joins its twin. It was the one tabular extension
 * with no family at all (so: the "unknown" glyph, and invisible to the
 * Spreadsheet filter), while the text viewer has read it as a table since
 * v0.5 (`viewers/CsvViewer.vue` branches on `ext === 'tsv'`). */
reg('sheet', ['xls', 'xlsx', 'ods', 'csv', 'tsv']);
reg('slides', ['ppt', 'pptx', 'odp']);
reg('archive', ['zip', 'tar', 'gz', 'bz2', '7z', 'rar', 'xz', 'zst']);
reg('code', [
  'js', 'ts', 'jsx', 'tsx', 'mjs', 'cjs', 'vue', 'py', 'go', 'rs', 'php', 'rb',
  'java', 'kt', 'swift', 'c', 'cpp', 'h', 'hpp', 'cs', 'css', 'scss', 'less',
  'html', 'htm', 'json', 'yml', 'yaml', 'xml', 'sh', 'bash', 'ps1', 'sql', 'toml',
]);
reg('text', ['txt', 'md', 'markdown', 'log', 'ini', 'conf', 'cfg', 'env']);

/**
 * The synthetic "a storage" row a listing can carry.
 *
 * The backend never sends `inode/storage`; FileExplorer mints these rows for
 * the multi-storage overview, and they are `type: 'dir'` with no folder behind
 * them — every mutation would 4xx. The test was written out longhand in three
 * view components, which is how a fourth surface came to be written without
 * it, so it is a function now and this file is its one home.
 */
export function isStorageRow(node: { mime_type?: string | null }): boolean {
  return node.mime_type === 'inode/storage';
}

/** Pick the icon family for a listing node. */
export function iconFamilyFor(node: {
  type?: string;
  extension?: string | null;
  mime_type?: string | null;
  basename?: string | null;
}): IconFamily {
  // ⚠ Both of these are also `type: 'dir'`, so they have to be answered
  // BEFORE the folder branch or they fall through to a plain folder — which
  // is what `HomeView` still does by asking for `{ type: 'dir' }` by hand.
  if (isStorageRow(node)) return 'storage';
  if (node.basename === '.trash') return 'trash';
  if (node.type === 'dir') return 'folder';
  const ext = (node.extension || '').toLowerCase();
  return EXT_FAMILIES[ext] ?? 'unknown';
}

// Shared fragments. The document families reuse one sheet-with-fold base so
// the set reads as one system; media families get standalone shapes.
const FILE_BASE =
  '<path d="M6.5 3h7L18.5 8v11.5a1.5 1.5 0 0 1-1.5 1.5H6.5A1.5 1.5 0 0 1 5 19.5v-15A1.5 1.5 0 0 1 6.5 3z"/>' +
  '<path d="M13.5 3v5h5"/>';

const GLYPHS: Record<IconFamily, string> = {
  // Folder is the one "filled" member — a soft currentColor wash keeps it
  // visually anchored without breaking the line style.
  /**
   * gorunum:v1 — the folder is SOLID, and it is the one glyph that never
   * gets a tile. A folder is the most common thing in a listing; drawn as an
   * outline in the same weight as the file glyphs it disappears into them,
   * and the eye has to read the label to tell a folder from a document. Filled
   * and in its own colour it is recognisable before the name is read.
   */
  folder:
    '<path d="M2.5 6.5A2.5 2.5 0 0 1 5 4h4.6a2.5 2.5 0 0 1 1.77.73L13 6.4h6a2.5 2.5 0 0 1 2.5 2.5v8.6A2.5 2.5 0 0 1 19 20H5a2.5 2.5 0 0 1-2.5-2.5z" fill="currentColor" stroke="none"/>',
  image:
    '<rect x="3" y="5" width="18" height="14" rx="2"/>' +
    '<circle cx="8.6" cy="10" r="1.6"/>' +
    '<path d="M5.2 16.6l4.3-4.3 3 3 2.4-2.4 3.9 3.7"/>',
  video:
    '<rect x="3" y="5" width="18" height="14" rx="2"/>' +
    '<path d="M10.2 9.3v5.4l4.8-2.7z" fill="currentColor" stroke="none"/>',
  audio:
    '<path d="M9 17.5V7.2l9-2.2v10.4"/>' +
    '<circle cx="6.8" cy="17.6" r="2.2"/>' +
    '<circle cx="15.8" cy="15.5" r="2.2"/>',
  pdf:
    FILE_BASE +
    '<rect x="7.5" y="12" width="9" height="5" rx="1" fill="currentColor" fill-opacity="0.14"/>',
  doc:
    FILE_BASE +
    '<path d="M8.5 13h7M8.5 16h7"/>',
  sheet:
    FILE_BASE +
    '<path d="M8 12.5h8M8 15.5h8M8 18h8M12 12.5V18"/>',
  slides:
    FILE_BASE +
    '<rect x="8" y="12.5" width="8" height="5" rx="0.8"/>',
  archive:
    FILE_BASE +
    '<path d="M10 3v1.6M10 6.6v1.6M10 10.2v1.6"/>' +
    '<rect x="8.6" y="14" width="2.8" height="3.4" rx="0.8"/>',
  code:
    FILE_BASE +
    '<path d="M10.4 12.5L8.3 15l2.1 2.5M13.6 12.5l2.1 2.5-2.1 2.5"/>',
  text:
    FILE_BASE +
    '<path d="M8.5 12h7M8.5 15h7M8.5 18h4.5"/>',
  // The two-bay drive the sidebar already draws for the same storages, so a
  // drive is one shape across the panel rather than one per surface.
  storage:
    '<rect x="3" y="5" width="18" height="6" rx="1.6"/>' +
    '<rect x="3" y="13" width="18" height="6" rx="1.6"/>' +
    '<path d="M6.5 8h.01M6.5 16h.01"/>',
  trash:
    '<path d="M4 7h16"/>' +
    '<path d="M9.5 7V5.6A1.6 1.6 0 0 1 11.1 4h1.8a1.6 1.6 0 0 1 1.6 1.6V7"/>' +
    '<path d="M6.2 7l.85 11.6A1.6 1.6 0 0 0 8.65 20h6.7a1.6 1.6 0 0 0 1.6-1.4L17.8 7"/>' +
    '<path d="M10.2 10.8v5.6M13.8 10.8v5.6"/>',
  unknown:
    FILE_BASE +
    '<path d="M10.3 13.1a1.8 1.8 0 1 1 2.5 1.9c-.6.3-.8.7-.8 1.4"/>' +
    '<circle cx="12" cy="18.4" r="0.9" fill="currentColor" stroke="none"/>',
};

const SVG_CACHE = new Map<IconFamily, string>();

/** Inline SVG markup for a family (safe static strings — v-html friendly). */
export function iconSvg(family: IconFamily): string {
  let svg = SVG_CACHE.get(family);
  if (!svg) {
    svg =
      `<svg class="fe-ficon fe-ficon--${family}" viewBox="0 0 24 24" fill="none" ` +
      'stroke="currentColor" stroke-width="1.6" stroke-linecap="round" ' +
      'stroke-linejoin="round" aria-hidden="true" focusable="false">' +
      `${GLYPHS[family]}</svg>`;
    SVG_CACHE.set(family, svg);
  }
  return svg;
}

/** Convenience: node → SVG markup in one call. */
export function fileIconSvg(node: Parameters<typeof iconFamilyFor>[0]): string {
  return iconSvg(iconFamilyFor(node));
}

const TILE_CACHE = new Map<IconFamily, string>();

/**
 * gorunum:v1 — the listing badge: a filled, rounded square in the family's
 * colour with the glyph knocked out in white.
 *
 * Why not the bare coloured glyph it replaces: at 16px a line drawing in a
 * pale accent is a smudge, and twelve of them down a column read as noise
 * rather than as twelve kinds of file. A solid chip of colour is legible at a
 * glance, holds its shape at any density, and gives the row a consistent left
 * edge whether the file has a thumbnail or not.
 *
 * The folder is the exception and returns its solid glyph untiled — a folder
 * is not a file type, and boxing it makes a listing look like a colour chart.
 */
export function iconTile(family: IconFamily): string {
  if (family === 'folder') {
    return `<span class="fe-ftile fe-ftile--folder">${iconSvg(family)}</span>`;
  }
  let tile = TILE_CACHE.get(family);
  if (!tile) {
    tile = `<span class="fe-ftile fe-ftile--${family}">${iconSvg(family)}</span>`;
    TILE_CACHE.set(family, tile);
  }
  return tile;
}

/** Convenience: node → tile markup in one call. */
export function fileIconTile(node: Parameters<typeof iconFamilyFor>[0]): string {
  return iconTile(iconFamilyFor(node));
}

/* ======================================================================
 * ikon:emoji — the one listing marker that is a STATE, not a type.
 *
 * An end-to-end encrypted folder (wiring:e2) is a folder like any other; what
 * is special about it is a fact, not a kind, so it cannot be an `IconFamily`
 * — `iconFamilyFor` is the taxonomy `lib/fileFilters` filters by, and moving
 * these rows out of `folder` would quietly drop them from the Folders filter.
 * It lives beside the families instead, as one definition.
 *
 * ⚠ It was three. `specialEmojiFor(n) { if (n.type === 'dir' && n.e2e) return
 * '🔒' }` was copy-pasted verbatim into ListView, GridView and GalleryView —
 * the exact shape of the thing the duplicate-code gate exists to stop, and
 * the second time this file has had to absorb it (the first was `.trash` and
 * the storage row, drawn as 🗑 and 💾 by the same three copies).
 * ====================================================================== */

/** True for a folder that is end-to-end encrypted. */
export function isEncryptedFolder(node: { type?: string; e2e?: boolean }): boolean {
  return node.type === 'dir' && node.e2e === true;
}

/**
 * The solid folder with a padlock cut OUT of it.
 *
 * One path, `fill-rule="evenodd"`: the shackle and the body are subpaths
 * inside the folder outline, so they become holes and show whatever the tile
 * sits on. That is deliberate — the alternative (drawing the lock in a
 * background colour) picks one background and is wrong on the other two
 * surfaces this tile appears on.
 *
 * ⚠ It replaces a 🔒 that replaced the folder entirely, so the row lost the
 * one thing it had always said: that it is a folder. Both facts are here now,
 * and the callers give the span an `aria-label` for the half a shape cannot
 * carry.
 */
const LOCKED_FOLDER =
  '<path fill="currentColor" stroke="none" fill-rule="evenodd" d="' +
  /* the folder — the same outline as GLYPHS.folder */
  'M2.5 6.5A2.5 2.5 0 0 1 5 4h4.6a2.5 2.5 0 0 1 1.77.73L13 6.4h6a2.5 2.5 0 0 1 2.5 2.5v8.6A2.5 2.5 0 0 1 19 20H5a2.5 2.5 0 0 1-2.5-2.5z' +
  /* the shackle: outer arc over, step in, inner arc back, close down the leg */
  'M9.7 12a2.3 2.3 0 0 1 4.6 0h-1.05a1.25 1.25 0 0 0-2.5 0z' +
  /* the body */
  'M9.2 12h5.6a1 1 0 0 1 1 1v3.6a1 1 0 0 1-1 1H9.2a1 1 0 0 1-1-1V13a1 1 0 0 1 1-1z' +
  '"/>';

let LOCKED_FOLDER_TILE = '';

/**
 * Tile markup for an encrypted folder — `fe-ftile--folder`'s own box, so it
 * is the same size, colour and (lack of) chip as every other folder in the
 * column; only the shape differs.
 */
export function encryptedFolderTile(): string {
  if (!LOCKED_FOLDER_TILE) {
    LOCKED_FOLDER_TILE =
      '<span class="fe-ftile fe-ftile--folder fe-ftile--folder-locked">' +
      '<svg class="fe-ficon fe-ficon--folder" viewBox="0 0 24 24" fill="none" ' +
      'stroke="currentColor" stroke-width="1.6" stroke-linecap="round" ' +
      'stroke-linejoin="round" aria-hidden="true" focusable="false">' +
      `${LOCKED_FOLDER}</svg></span>`;
  }
  return LOCKED_FOLDER_TILE;
}

/* ======================================================================
 * gorunum:v1-preview — what the thing IS, said in words.
 *
 * The Type column used to print the raw extension in caps: `TS`, `CSV`,
 * `JPG`. That is not a kind, it is a suffix — it answers "what did the file
 * get called" when the question the column asks is "what is this". The
 * reference shell prints `TypeScript`, `Spreadsheet`, `Image`.
 *
 * ⚠ ONE table, here, beside the extension → family map it is built on top of
 * (filex lesson #67). Every surface that names a kind — the Type column
 * today, an info panel tomorrow — calls `typeLabelFor` and gets the same
 * answer; a second table beside it would disagree the first week somebody
 * added an extension to only one of them.
 *
 * Three tiers, in order:
 *   1. the extension has a name of its own      →  `ftype.typescript`
 *   2. otherwise its FAMILY has one            →  `ftype.code`, `ftype.image`
 *   3. neither (family `unknown`)              →  the uppercased extension
 *
 * Tier 3 is the deliberate fallback the brief asks for: an unmapped `.zig`
 * reads "ZIG", which is at least the truth, rather than an empty cell or a
 * confident lie like "File".
 * ====================================================================== */

/** Extension → its own i18n key. Only extensions whose NAME differs from
 *  what the family would say need an entry here. */
const EXT_TYPE_KEYS: Record<string, string> = {
  // Languages the person reading the column thinks of by name.
  ts: 'ftype.typescript',
  tsx: 'ftype.typescript',
  mts: 'ftype.typescript',
  cts: 'ftype.typescript',
  js: 'ftype.javascript',
  jsx: 'ftype.javascript',
  mjs: 'ftype.javascript',
  cjs: 'ftype.javascript',
  vue: 'ftype.vue',
  py: 'ftype.python',
  go: 'ftype.go',
  rs: 'ftype.rust',
  php: 'ftype.php',
  rb: 'ftype.ruby',
  java: 'ftype.java',
  kt: 'ftype.kotlin',
  swift: 'ftype.swift',
  c: 'ftype.c',
  h: 'ftype.c',
  cpp: 'ftype.cpp',
  hpp: 'ftype.cpp',
  cs: 'ftype.csharp',
  // Structured text that is code by family but not a language.
  css: 'ftype.stylesheet',
  scss: 'ftype.stylesheet',
  less: 'ftype.stylesheet',
  html: 'ftype.html',
  htm: 'ftype.html',
  json: 'ftype.json',
  yml: 'ftype.yaml',
  yaml: 'ftype.yaml',
  xml: 'ftype.xml',
  toml: 'ftype.toml',
  sql: 'ftype.sql',
  sh: 'ftype.shell',
  bash: 'ftype.shell',
  zsh: 'ftype.shell',
  ps1: 'ftype.powershell',
  // Plain-text family members that are each their own thing.
  md: 'ftype.markdown',
  markdown: 'ftype.markdown',
  txt: 'ftype.plaintext',
  log: 'ftype.log',
  ini: 'ftype.config',
  conf: 'ftype.config',
  cfg: 'ftype.config',
  env: 'ftype.config',
  // Tabular: a `.csv` IS a spreadsheet to the person looking at the column,
  // whatever the program that wrote it was.
  csv: 'ftype.sheet',
  tsv: 'ftype.sheet',
  // Design files. No icon family of their own (they fall to `unknown`, which
  // is a decision for the GLYPH, not for the word) — naming them still beats
  // printing "FIG".
  fig: 'ftype.figma',
  sketch: 'ftype.design',
  xd: 'ftype.design',
  psd: 'ftype.design',
  ai: 'ftype.design',
};

/** Family → i18n key, the second tier. `unknown` is absent on purpose: it is
 *  what sends a node to the uppercased-extension fallback. */
const FAMILY_TYPE_KEYS: Partial<Record<IconFamily, string>> = {
  folder: 'node.folder',
  image: 'ftype.image',
  video: 'ftype.video',
  audio: 'ftype.audio',
  pdf: 'ftype.pdf',
  doc: 'ftype.document',
  sheet: 'ftype.sheet',
  slides: 'ftype.slides',
  archive: 'ftype.archive',
  code: 'ftype.code',
  text: 'ftype.plaintext',
};

/**
 * The i18n key naming this node's kind, or null when nothing maps it and the
 * caller should fall back to the extension. Directories answer
 * `node.folder` — the key the grid card's caption already prints, not a
 * second string saying the same word.
 */
export function typeLabelKey(node: {
  type?: string;
  extension?: string | null;
}): string | null {
  if (node.type === 'dir') return 'node.folder';
  const ext = (node.extension || '').trim().toLowerCase();
  const own = EXT_TYPE_KEYS[ext];
  if (own) return own;
  return FAMILY_TYPE_KEYS[iconFamilyFor(node)] ?? null;
}

/**
 * The finished words for the Type column and anything else that names a
 * kind: the mapped name, else the uppercased extension, else an em dash.
 *
 * ⚠ The fallback lives HERE rather than at each call site. Two callers each
 * writing `key ? t(key) : ext.toUpperCase()` is two chances to render an
 * empty cell, and the first one to forget the `|| '—'` prints nothing at all
 * for a file with no extension.
 */
export function typeLabelFor(
  node: { type?: string; extension?: string | null },
  t: (key: string) => string,
): string {
  const key = typeLabelKey(node);
  if (key) return t(key);
  const ext = (node.extension || '').trim();
  return ext ? ext.toUpperCase() : '—';
}
