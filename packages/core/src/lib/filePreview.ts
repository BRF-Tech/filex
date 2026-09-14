/**
 * filePreview — gorunum:v1-preview.
 *
 * The first lines of the REAL file, drawn inside the card's preview box when
 * there is nothing better to put there.
 *
 * ## Why this exists at all
 *
 * The backend already answers `thumb_url` for a `.ts` or a `.csv` — but what
 * it renders for them is `internal/thumb/generic.go`: a card tinted from a
 * hash of the extension with the letters "TS" drawn in the middle. Measured
 * on the local backend, `GET /api/files/thumb/6` (app.ts) is a 1,745-byte
 * JPEG of exactly that. So the card was already spending a request to be told
 * the same thing its own footer tile says, in a colour nobody chose.
 *
 * A code file's first five lines say what it is in a way no glyph can. That
 * is the whole idea: for the kinds we can read as text we skip the generic
 * thumbnail entirely and fetch a few kilobytes of the file instead — the same
 * number of requests, a preview that means something.
 *
 * ⚠ This never competes with a REAL thumbnail. The kinds it claims (code,
 * plain text, csv/tsv) are precisely the kinds this backend cannot render a
 * content-derived thumbnail for; images, video, audio and PDFs are untouched
 * and keep their `<img>`.
 *
 * ## What bounds the cost
 *
 * Four independent limits, because any one of them alone is a promise rather
 * than a bound:
 *
 *   1. **Visibility.** Nothing is fetched until the card intersects the
 *      viewport (IntersectionObserver, 150px of margin so a scroll does not
 *      chase the reader). ⚠ `useThumbs` does NOT do this — it fires on first
 *      render for every row the parent hands it — so there was nothing to
 *      reuse here; a 400-file folder would have been 400 reads.
 *   2. **A ranged request.** `Range: bytes=0-8191`. The preview endpoint
 *      answers 206 through `http.ServeContent` whenever the driver can seek
 *      (`vfStream`, manager.go).
 *   3. **A bounded read.** ⚠ A driver that cannot seek answers 200 with the
 *      WHOLE body and `Accept-Ranges: none` — the Range header is a request,
 *      not a guarantee. So the body is consumed chunk by chunk and the reader
 *      is cancelled the moment we have enough. This, not the header, is what
 *      actually caps the bytes.
 *   4. **A file-size ceiling + a concurrency cap.** 4 MB and 3 in flight.
 *
 * Results are cached per path AND per version (etag, else mtime, else size),
 * so an edited file re-reads and an unchanged one never does.
 */

import { ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import { iconFamilyFor } from './fileIcons';
import { resolveEndpoints } from '../composables/useFileApi';

/** What shape the preview takes. `null` = this node has no text preview. */
export type PreviewKind = 'code' | 'text' | 'table';

/**
 * One rendered line. Split in three so the view can print it WITHOUT
 * `v-html` — the content is somebody's file, and interpolation is the only
 * honest way to put it on screen.
 */
export interface PreviewLine {
  /** Leading whitespace, kept so nesting is visible. */
  indent: string;
  /** The opening token when it is worth tinting ('' = leave it plain). */
  tint: string;
  /** Everything after it, verbatim. */
  text: string;
}

export interface FilePreview {
  kind: PreviewKind;
  /** `code` / `text`. Empty for a table. */
  lines: PreviewLine[];
  /**
   * `table` only: first rows × first columns, already clipped and PADDED to a
   * rectangle. The card lays them out as one CSS grid, so a short row has to
   * carry its empty cells or every row below it shifts a column left.
   */
  rows: string[][];
  /** `table` only: the grid's column count (0 otherwise). */
  cols: number;
}

/* ---------------------------------------------------------------- limits */

/** Bytes asked for, and the hard cap on bytes actually read. */
const MAX_BYTES = 8 * 1024;
/** Files bigger than this are left alone. See the header: the streaming
 *  cancel is the real bound, this is the "never surprise the operator whose
 *  driver cannot seek" guard. */
const MAX_FILE_BYTES = 4 * 1024 * 1024;
/** Concurrent reads. Three keeps a fast scroll responsive without turning a
 *  folder of scripts into a thundering herd against one S3 bucket. */
const MAX_INFLIGHT = 3;
/**
 * How much is parsed — NOT how much is shown.
 *
 * ⚠ These used to be 10 lines / 6 rows / 4 columns, and those numbers were
 * chosen for one box: the 184×108 grid card. They are not that box's height
 * in any other view, and a fixed count is the wrong shape of answer anyway.
 * Measured on 2026-09-12, before this change:
 *
 *   grid card    184×108  · 10 lines parsed, ~9 fit          → close enough
 *   gallery tile 220×220  · 10 lines parsed, 15 fit          → the bottom
 *                                                              HALF of every
 *                                                              tile was empty
 *   csv, either  · 6 rows × 4 cols whatever the box was, and budget.csv has
 *                  six columns, so two of them were simply never shown
 *
 * The owner's instruction is "as much as we can fit" (2026-09-12), so the box
 * decides: the parser hands over more than any box can show and the box
 * clips what it cannot, which is what `overflow: hidden` and the bottom fade
 * on `.fe-fprev` have always been for. These are therefore CEILINGS on work
 * and DOM, not layout constants — big enough for the largest tile we draw
 * with room over it, small enough that a 400-file folder stays cheap.
 */
const MAX_LINES = 24;
/** Characters kept per line. The box clips long lines anyway; this bounds
 *  the DOM when a minified file arrives as one 8 KB line. */
const MAX_LINE_CHARS = 120;
const MAX_ROWS = 16;
/** Columns parsed. How many are DRAWN follows the box: the grid lays the
 *  cells out on tracks with a readable minimum width and clips the overflow,
 *  so a wide tile shows more of the table than a narrow one without either
 *  view knowing anything the other does not. */
const MAX_COLS = 8;
/** Cache ceiling, mirroring useThumbs' own. Parsed previews are a few hundred
 *  bytes each, so this is generous. */
const MAX_CACHED = 400;

/* ------------------------------------------------------------ can we read */

/** The two tabular extensions. `sheet` as a FAMILY also holds xls/xlsx/ods,
 *  which are zip containers — reading their first bytes gives binary. */
const TABLE_EXTS = new Set(['csv', 'tsv']);

/**
 * Is this row a deleted thing?
 *
 * ⚠ Two fields, because the two ways filex shows you a deleted file disagree.
 * A normal listing marks a soft-deleted row with `trashed`. The TRASH VIEW
 * does not: `FileExplorer.loadTrash` builds its rows from `TrashEntry` by hand
 * and that literal sets neither `trashed` nor `size` — it writes `file_size`
 * and stamps `extra_metadata.deleted_at`.
 *
 * Which means the size ceiling below was already excluding the whole trash
 * view, by accident, because `size` came back undefined. That is not a guard,
 * it is a coincidence one line in an unrelated file would end: the trash row's
 * `path` points at the trash KEY, not at the file it used to be, so a content
 * read there fetches a 404 for every row in the folder.
 */
function isTrashRow(node: FileNode): boolean {
  if (node.trashed) return true;
  const meta = node.extra_metadata;
  return !!meta && typeof meta === 'object' && 'deleted_at' in meta;
}

/**
 * Which preview this node could have, ignoring whether anything is wired up.
 *
 * Built on `iconFamilyFor` rather than on a second extension list: the
 * families ARE the taxonomy (filex lesson #67), so a new extension registered
 * for the icon set becomes previewable in the same edit. `svg` belongs to the
 * `image` family and is therefore excluded here — it is text, but it has a
 * real thumbnail, and the real one wins.
 */
export function previewKindFor(node: FileNode): PreviewKind | null {
  if (node.type !== 'file') return null;
  if (node.basename === '.trash' || node.mime_type === 'inode/storage') return null;
  if (isTrashRow(node)) return null;
  // ⚠ A node with NO size is excluded too, not just an empty one: the size is
  // what the 4 MB ceiling is checked against, and "we don't know" is not a
  // basis for deciding to read a file. Every row `projectFileNodes` emits
  // carries one.
  const size = node.size ?? 0;
  if (size <= 0 || size > MAX_FILE_BYTES) return null;
  const ext = (node.extension || '').trim().toLowerCase();
  if (TABLE_EXTS.has(ext)) return 'table';
  const family = iconFamilyFor(node);
  if (family === 'code') return 'code';
  if (family === 'text') return 'text';
  return null;
}

/**
 * Is this node's thumbnail a PAGE — the first page of a document, rather than
 * a picture that was already the shape it wanted to be?
 *
 * It matters because of where the card crops. A thumbnail box is landscape
 * (184×108 in the grid) and a page is portrait, so `object-fit: cover` has to
 * throw away most of the height, and WHICH part it throws away is the whole
 * question. Centred — the default, and what this did before — discards the
 * top, which on a document is the letterhead, the title and the date: the
 * only part that says which document it is. Measured on 2026-09-12 against a
 * reference deployment carrying the same defect, five different PDFs sharing
 * one template produced five cards nobody could tell apart, because the only
 * thing that differed between them was the title that had been cropped away.
 *
 * So pages anchor to the top and photographs stay centred, and this is the
 * one place that decides which is which. Built on `iconFamilyFor` rather than
 * a private extension list, for the same reason the preview kinds are: the
 * families ARE the taxonomy (filex lesson #67).
 */
export function drawsAsPage(node: FileNode): boolean {
  if (node.type !== 'file') return false;
  switch (iconFamilyFor(node)) {
    case 'pdf':
    case 'doc':
    case 'sheet':
    case 'slides':
      return true;
    default:
      return false;
  }
}

/**
 * Is this node's thumbnail a still taken from a MOVING picture?
 *
 * A frame lifted out of a video is, on a card, indistinguishable from a
 * photograph — which is the one thing a person needs to know before they
 * click it. The views draw a play badge over these, so the answer lives here
 * rather than in each of them.
 */
export function drawsAsVideo(node: FileNode): boolean {
  return node.type === 'file' && iconFamilyFor(node) === 'video';
}

/* --------------------------------------------------------------- parsing */

/**
 * Openers worth a tint. Deliberately small and deliberately language-agnostic:
 * the brief's own instruction is that plain text beats fake colour, so a token
 * is tinted only when it really is a keyword. A JSON file opening on `{`, or a
 * YAML file opening on a key, gets no colour at all — which is the correct
 * answer, not a missing feature.
 */
const KEYWORDS = new Set([
  'import', 'export', 'from', 'require', 'include', 'use', 'using', 'package',
  'module', 'namespace', 'const', 'let', 'var', 'function', 'func', 'fn', 'def',
  'class', 'struct', 'enum', 'interface', 'type', 'impl', 'trait',
  'public', 'private', 'protected', 'static', 'async', 'await', 'return',
  'if', 'else', 'for', 'while', 'switch', 'case', 'try', 'catch',
  'select', 'insert', 'update', 'delete', 'create', 'alter', 'drop',
]);

/** Markdown-ish line markers — the only thing tinted in a `text` preview. */
const TEXT_MARKER = /^(#{1,6}|[-*+]|>|\d+\.)$/;

/** Strip the punctuation a keyword is usually wearing (`const,` `if(` `def:`). */
function bareToken(token: string): string {
  return token.replace(/[(){}[\];:,.]+$/, '').toLowerCase();
}

function toLine(raw: string, kind: PreviewKind): PreviewLine {
  const clipped = raw.length > MAX_LINE_CHARS ? raw.slice(0, MAX_LINE_CHARS) : raw;
  const m = /^(\s*)(\S+)([\s\S]*)$/.exec(clipped);
  if (!m) return { indent: '', tint: '', text: clipped };
  const [, indent, first, rest] = m;
  const tintable =
    kind === 'code' ? KEYWORDS.has(bareToken(first)) : TEXT_MARKER.test(first);
  if (!tintable) return { indent: '', tint: '', text: clipped };
  return { indent, tint: first, text: rest };
}

/**
 * Split one delimited row into cells, honouring `"…"` quoting (and `""` as an
 * escaped quote) so a value containing the separator does not become two
 * columns. Small on purpose — this draws six rows on a 184px card, it is not
 * a CSV parser and must never grow into one.
 */
export function splitCells(line: string, sep: string): string[] {
  const out: string[] = [];
  let cur = '';
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quoted) {
      if (c === '"') {
        if (line[i + 1] === '"') {
          cur += '"';
          i++;
        } else {
          quoted = false;
        }
      } else {
        cur += c;
      }
    } else if (c === '"') {
      quoted = true;
    } else if (c === sep) {
      out.push(cur);
      cur = '';
    } else {
      cur += c;
    }
  }
  out.push(cur);
  return out;
}

/** `,` normally; `\t` for a .tsv; `;` when the first row clearly uses it —
 *  the shape Excel writes in most of Europe, Turkey included. */
function separatorFor(ext: string, firstLine: string): string {
  if (ext === 'tsv') return '\t';
  if (!firstLine.includes(',') && firstLine.includes(';')) return ';';
  return ',';
}

/**
 * Bytes → a drawable preview, or null when the bytes are not text after all
 * (a `.json` that is really a binary blob, ciphertext, a mislabelled file).
 *
 * `truncated` says the byte cap cut the stream, in which case the last line is
 * dropped: half a line of source looks like a bug, not like a preview.
 */
export function parsePreview(
  bytes: Uint8Array,
  kind: PreviewKind,
  ext: string,
  truncated: boolean,
): FilePreview | null {
  // A NUL in the first few KB is the cheapest reliable "this is not text".
  for (let i = 0; i < bytes.length; i++) {
    if (bytes[i] === 0) return null;
  }
  const text = new TextDecoder('utf-8', { fatal: false }).decode(bytes);
  // A decode full of replacement characters is binary wearing a text
  // extension — or a file in an encoding we are not going to guess at.
  let bad = 0;
  for (const ch of text) if (ch === '�') bad++;
  if (text.length > 0 && bad / text.length > 0.1) return null;

  const lines = text.split(/\r\n|\r|\n/);
  if (truncated && lines.length > 1) lines.pop();
  // Blank lines at either end waste a box that only holds eight — and every
  // text file that ends in a newline has one. Interior blanks stay: they are
  // part of how the file looks, which is the whole point.
  while (lines.length && lines[0].trim() === '') lines.shift();
  while (lines.length && lines[lines.length - 1].trim() === '') lines.pop();
  if (lines.length === 0) return null;

  if (kind === 'table') {
    const sep = separatorFor(ext, lines[0]);
    const rows = lines
      .filter((l) => l.trim() !== '')
      .slice(0, MAX_ROWS)
      .map((l) => splitCells(l, sep).slice(0, MAX_COLS).map((c) => c.trim().slice(0, 24)));
    if (rows.length === 0) return null;
    const cols = rows.reduce((n, r) => Math.max(n, r.length), 0);
    for (const r of rows) while (r.length < cols) r.push('');
    return { kind, lines: [], rows, cols };
  }

  return {
    kind,
    lines: lines.slice(0, MAX_LINES).map((l) => toLine(l, kind)),
    rows: [],
    cols: 0,
  };
}

/* ---------------------------------------------------------------- loading */

export interface PreviewLoaderOptions {
  /** Same prefix the explorer hands the views. `undefined` = no API wired
   *  (a legacy embedder on an explicit `endpoint`) → the loader stays off. */
  apiBase?: string;
  /**
   * ⚠ ASYNC, and it is the whole reason this signature is a function and not
   * an object. Spreading a Promise into a headers literal yields `{}` — the
   * request goes out with no Authorization and 401s with nothing in the
   * console. Every call site here `await`s it.
   */
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  credentials?: RequestCredentials;
  /**
   * Answered on every decision, not once at construction: "is reading file
   * bytes off right now?". The explorer passes its encrypted-folder state —
   * inside one, every body on the wire is ciphertext, and fetching 8 KB of it
   * to discover that it does not decode is a request spent to learn nothing.
   */
  disabled?: () => boolean;
}

export interface FilePreviewLoader {
  /** False when nothing is wired — callers must then keep their old markup. */
  readonly enabled: boolean;
  /** The kind this node would get, or null (including when disabled). */
  kindFor: (node: FileNode) => PreviewKind | null;
  /** Reactive read. null = not loaded (yet), or never. */
  get: (node: FileNode) => FilePreview | null;
  /** Template-ref sink: hand it the preview box and its node. */
  bind: (el: Element | null, node: FileNode) => void;
  /** In-flight + queued, for tests and measurement. */
  stats: () => { loaded: number; inflight: number; queued: number; failed: number };
  dispose: () => void;
}

/** path + version: an edited file re-reads, an untouched one never does. */
function keyOf(node: FileNode): string {
  const etag = typeof node.etag === 'string' ? node.etag : '';
  const version = etag || String(node.last_modified ?? '') || String(node.size ?? '');
  return `${node.path} ${version}`;
}

export function createFilePreviews(opts: PreviewLoaderOptions): FilePreviewLoader {
  const enabled =
    opts.apiBase !== undefined &&
    typeof opts.authHeaders === 'function' &&
    typeof IntersectionObserver !== 'undefined' &&
    typeof fetch === 'function';

  const cache = ref<Record<string, FilePreview>>({});
  const order: string[] = [];
  const failed = new Set<string>();
  const queued = new Set<string>();
  const queue: FileNode[] = [];
  const controllers = new Set<AbortController>();
  /** element → the key it is currently observed for, so a re-render of the
   *  same card does not re-observe and a recycled row does. */
  const bound = new WeakMap<Element, string>();
  let inflight = 0;
  let observer: IntersectionObserver | null = null;
  /** The observed element's node, kept until the callback fires. */
  const watching = new Map<Element, FileNode>();

  function previewUrl(path: string): string {
    // resolveEndpoints is the one place that knows how apiBase becomes a
    // manager URL; `?q=` + `&action=` is the pair every other caller sends.
    const manager = resolveEndpoints({ apiBase: opts.apiBase }).manager;
    const sep = manager.includes('?') ? '&' : '?';
    const query = new URLSearchParams({ q: 'preview', action: 'preview', path });
    return `${manager}${sep}${query.toString()}`;
  }

  function remember(key: string, value: FilePreview) {
    if (order.length >= MAX_CACHED) {
      const evict = order.shift();
      if (evict) {
        const next = { ...cache.value };
        delete next[evict];
        cache.value = next;
      }
    }
    order.push(key);
    cache.value = { ...cache.value, [key]: value };
  }

  /**
   * Read at most `MAX_BYTES` of the body, whatever the server decided to send.
   * A 206 is already short; a 200 from a driver that cannot seek is the whole
   * file, and cancelling the reader is what stops it.
   */
  async function readHead(res: Response): Promise<{ bytes: Uint8Array; truncated: boolean }> {
    const body = res.body;
    if (!body) {
      const buf = new Uint8Array(await res.arrayBuffer());
      return { bytes: buf.slice(0, MAX_BYTES), truncated: buf.length > MAX_BYTES };
    }
    const reader = body.getReader();
    const chunks: Uint8Array[] = [];
    let total = 0;
    try {
      while (total < MAX_BYTES) {
        const { done, value } = await reader.read();
        if (done) break;
        if (value && value.length) {
          chunks.push(value);
          total += value.length;
        }
      }
    } finally {
      try {
        await reader.cancel();
      } catch {
        /* already closed */
      }
    }
    const joined = new Uint8Array(Math.min(total, MAX_BYTES));
    let at = 0;
    for (const c of chunks) {
      if (at >= joined.length) break;
      joined.set(c.subarray(0, joined.length - at), at);
      at += c.length;
    }
    return { bytes: joined, truncated: total >= MAX_BYTES };
  }

  async function load(node: FileNode, kind: PreviewKind): Promise<void> {
    const key = keyOf(node);
    const ctrl = new AbortController();
    controllers.add(ctrl);
    try {
      // ⚠ The await. Without it this object holds a Promise under
      // `authHeaders`, fetch drops it, and the request 401s silently.
      const base = opts.authHeaders ? await opts.authHeaders() : {};
      const res = await fetch(previewUrl(node.path), {
        headers: { ...base, Accept: '*/*', Range: `bytes=0-${MAX_BYTES - 1}` },
        credentials: opts.credentials,
        signal: ctrl.signal,
      });
      if (!res.ok && res.status !== 206) throw new Error(String(res.status));
      const { bytes, truncated } = await readHead(res);
      ctrl.abort();
      const ext = (node.extension || '').trim().toLowerCase();
      const parsed = parsePreview(bytes, kind, ext, truncated);
      if (!parsed) {
        failed.add(key);
        return;
      }
      remember(key, parsed);
    } catch {
      // 403/404/offline/not-really-text — the card keeps its type tile, and
      // we do not retry inside this session (same policy as useThumbs).
      failed.add(key);
    } finally {
      controllers.delete(ctrl);
    }
  }

  function pump() {
    while (inflight < MAX_INFLIGHT && queue.length > 0) {
      const node = queue.shift()!;
      const kind = previewKindFor(node);
      queued.delete(keyOf(node));
      if (!kind) continue;
      inflight++;
      void load(node, kind).finally(() => {
        inflight--;
        pump();
      });
    }
  }

  function enqueue(node: FileNode) {
    const key = keyOf(node);
    if (cache.value[key] || failed.has(key) || queued.has(key)) return;
    queued.add(key);
    queue.push(node);
    pump();
  }

  function ensureObserver(): IntersectionObserver | null {
    if (!enabled) return null;
    if (!observer) {
      observer = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            if (!entry.isIntersecting) continue;
            const node = watching.get(entry.target);
            observer?.unobserve(entry.target);
            watching.delete(entry.target);
            if (node) enqueue(node);
          }
        },
        { rootMargin: '150px' },
      );
    }
    return observer;
  }

  function kindFor(node: FileNode): PreviewKind | null {
    if (!enabled) return null;
    if (opts.disabled?.()) return null;
    return previewKindFor(node);
  }

  return {
    enabled,
    kindFor,
    get(node: FileNode): FilePreview | null {
      if (!enabled) return null;
      return cache.value[keyOf(node)] ?? null;
    },
    bind(el: Element | null, node: FileNode) {
      if (!el || !kindFor(node)) return;
      const key = keyOf(node);
      if (bound.get(el) === key) return;
      bound.set(el, key);
      if (cache.value[key] || failed.has(key)) return;
      const io = ensureObserver();
      if (!io) return;
      watching.set(el, node);
      io.observe(el);
    },
    stats: () => ({
      loaded: order.length,
      inflight,
      queued: queue.length,
      failed: failed.size,
    }),
    dispose() {
      observer?.disconnect();
      observer = null;
      watching.clear();
      for (const c of controllers) c.abort();
      controllers.clear();
      queue.length = 0;
      queued.clear();
    },
  };
}
