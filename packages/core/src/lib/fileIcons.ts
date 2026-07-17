/**
 * fileIcons — extension/type → inline SVG icon mapping.
 *
 * Replaces the old emoji maps in GridView/ListView with a minimalist
 * line-icon set. Icons are hand-drawn 24×24 strokes on `currentColor`;
 * each family gets an accent via `--fe-icon-<family>` (variables.css),
 * applied through the `fe-ficon--<family>` class in base.css.
 *
 * Scope: file-TYPE icons only. Action emojis (trash/star/…) and the
 * special `.trash` / storage rows stay emoji — see the views.
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
reg('sheet', ['xls', 'xlsx', 'ods', 'csv']);
reg('slides', ['ppt', 'pptx', 'odp']);
reg('archive', ['zip', 'tar', 'gz', 'bz2', '7z', 'rar', 'xz', 'zst']);
reg('code', [
  'js', 'ts', 'jsx', 'tsx', 'mjs', 'cjs', 'vue', 'py', 'go', 'rs', 'php', 'rb',
  'java', 'kt', 'swift', 'c', 'cpp', 'h', 'hpp', 'cs', 'css', 'scss', 'less',
  'html', 'htm', 'json', 'yml', 'yaml', 'xml', 'sh', 'bash', 'ps1', 'sql', 'toml',
]);
reg('text', ['txt', 'md', 'markdown', 'log', 'ini', 'conf', 'cfg', 'env']);

/** Pick the icon family for a listing node. */
export function iconFamilyFor(node: {
  type?: string;
  extension?: string | null;
}): IconFamily {
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
  folder:
    '<path d="M3 7a2 2 0 0 1 2-2h4.2l2 2.4H19a2 2 0 0 1 2 2V17a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" fill="currentColor" fill-opacity="0.14"/>',
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
export function fileIconSvg(node: { type?: string; extension?: string | null }): string {
  return iconSvg(iconFamilyFor(node));
}
