// "Open with filex" — the desktop app as a handler for office documents.
//
// The problem this exists for: there is no Microsoft Office on this machine and
// none wanted, which is also true of most Linux desktops and many Macs. filex
// already renders and EDITS .docx/.xlsx/.pptx through its OnlyOffice
// integration — but only for documents that live on a filex server. A file
// sitting on the desktop had no way in. Double-clicking it now opens filex,
// which puts it in front of that same editor and writes the result back over
// the original file.
//
// Everything in this module is PURE (or fs-only) on purpose. The parts that can
// destroy data — deciding whether a local file already has a remote twin,
// naming the scratch copy, replacing the original atomically, deciding what is
// stale enough to delete — are exactly the parts that must be measurable
// without an Electron window, a server or a mouse. The Electron-facing half is
// openwith-io.ts (HTTP) and main.ts (windows, dialogs, lifecycle).

import crypto from 'node:crypto';
import fs from 'node:fs';
import nodePath from 'node:path';

/**
 * The types filex takes over — office documents ONLY, deliberately.
 *
 * These are the ones with no editor on a plain machine and a real editor on the
 * filex side (OnlyOffice). Images, PDFs and code already open in something on
 * every OS, so claiming them would be taking a file type away from an app that
 * handles it better.
 *
 * `.csv` is the one that is not an office document by name (#151, the
 * maintainer's call): since 0.51 a server with ONLYOFFICE edits it as a
 * spreadsheet and saves it back as CSV in the file's own dialect - its
 * delimiter, its byte order mark, the text of every cell nobody changed -
 * which neither a text editor nor a spreadsheet app without import settings
 * does.
 *
 * ⚠ Widening this list is a one-line change HERE, but it is not a one-line
 * change in the product. The same extensions are named in
 * electron-builder.yml (mac.fileAssociations, linux.mimeTypes - with the
 * OFFICE_MIME_TYPES entry below), build/installer.nsh (claim AND release),
 * build/appx-extensions.xml (the Store package) and scripts/pkg-manifests.mjs
 * FILE_EXTENSIONS (winget), or the app appears in "Open with" on one OS and
 * not the others (test/extension-lists.test.ts and test/pkg-manifests.test.ts
 * fail when they disagree); scripts/openwith-e2e.mjs counts them, and
 * docs/DESKTOP.md and the README list them. It is the one list of file kinds
 * the desktop keeps on purpose - the operating system is told it at install
 * time, before any server is reached - and every entry is held to the
 * server's own document types (web/tests/lib/serverRuleVectors.test.ts, #211).
 */
export const OFFICE_EXTENSIONS = [
  'docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt', 'odt', 'ods', 'odp', 'rtf', 'csv',
] as const;

const OFFICE_SET: ReadonlySet<string> = new Set<string>(OFFICE_EXTENSIONS);

/**
 * The media type per extension — ONE table, three consumers.
 *
 * The upload sends it, the Linux "make filex the default" button feeds it to
 * `xdg-mime`, and electron-builder's `linux.fileAssociations` in
 * electron-builder.yml has to repeat it by hand (a YAML file cannot import
 * TypeScript). If they disagree, the app registers for a type it will not open.
 */
export const OFFICE_MIME_TYPES: Readonly<Record<string, string>> = {
  docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  doc: 'application/msword',
  xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  xls: 'application/vnd.ms-excel',
  pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  ppt: 'application/vnd.ms-powerpoint',
  odt: 'application/vnd.oasis.opendocument.text',
  ods: 'application/vnd.oasis.opendocument.spreadsheet',
  odp: 'application/vnd.oasis.opendocument.presentation',
  rtf: 'application/rtf',
  // shared-mime-info's own name for it (text/x-csv and
  // text/x-comma-separated-values are its aliases).
  csv: 'text/csv',
};

/** The scratch folder on the server. Leading dot: it is machinery, not content. */
export const SCRATCH_DIR_NAME = '.filex-open';

/** Lowercase extension without the dot, or '' when there is none. */
export function extensionOf(p: string): string {
  const base = p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? '';
  const dot = base.lastIndexOf('.');
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : '';
}

/** True for a path this app is willing to be the handler for. */
export function isOfficeDocument(p: string): boolean {
  return OFFICE_SET.has(extensionOf(p));
}

// ─────────────────────────── argv ───────────────────────────

export interface ArgvIntent {
  /** `filex://…` links — the sign-in callback. */
  deepLinks: string[];
  /** Everything that looks like a path to a file on this machine. */
  files: string[];
}

/**
 * Splits a command line into the two things the OS can hand this app.
 *
 * ⚠ A file path is not a deep link and a deep link is not a file path, and the
 * app has had a `filex://` handler far longer than a file handler — so the
 * classification is written once, here, rather than as two `argv.find(…)` calls
 * that can drift apart. `filex://auth?code=…` reaching the document opener
 * would try to stat a URL; `C:\x.docx` reaching the auth path would fail a PKCE
 * exchange with a baffling message.
 *
 * ⚠ Argument 0 is the executable and, in an `electron .` dev run, argument 1 is
 * the project directory — neither is a document anyone asked to open. Chromium
 * also passes its own switches through (`--user-data-dir=…`, `--lang=…`), which
 * is why anything starting with `-` is dropped rather than stat'ed.
 *
 * `file://` URLs are accepted and converted: some Linux launchers hand a URL
 * where the shell would hand a path.
 */
export function classifyArgv(
  argv: readonly string[],
  opts: { defaultApp?: boolean; scheme?: string } = {},
): ArgvIntent {
  const scheme = opts.scheme ?? 'filex';
  const out: ArgvIntent = { deepLinks: [], files: [] };
  const start = opts.defaultApp ? 2 : 1;
  for (let i = start; i < argv.length; i++) {
    const raw = String(argv[i] ?? '');
    if (!raw) continue;
    if (raw.startsWith(scheme + '://')) {
      out.deepLinks.push(raw);
      continue;
    }
    if (raw.startsWith('-')) continue; // a switch, not a document
    if (/^file:\/\//i.test(raw)) {
      const asPath = fileUrlToPath(raw);
      if (asPath) out.files.push(asPath);
      continue;
    }
    // Any OTHER scheme is something this app was not asked to handle. A Windows
    // drive letter (`C:\x`) is deliberately NOT caught by this test: it has a
    // colon but no `//`.
    if (/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) continue;
    out.files.push(raw);
  }
  return out;
}

/** `file:///C:/a%20b.docx` → `C:/a b.docx`. Null when it is not decodable. */
export function fileUrlToPath(url: string): string | null {
  try {
    const u = new URL(url);
    let p = decodeURIComponent(u.pathname);
    if (u.hostname && u.hostname !== 'localhost') p = '//' + u.hostname + p; // UNC
    // A Windows path arrives as `/C:/…`; a POSIX one keeps its leading slash.
    if (/^\/[a-z]:/i.test(p)) p = p.slice(1);
    return p || null;
  } catch {
    return null;
  }
}

// ─────────────────────────── sync twin ───────────────────────────

/** The subset of the sync engine's pair record this module needs. Structural,
 *  so `sync.ts`'s `Pair` satisfies it without this file importing it. */
export interface SyncPairView {
  id: string;
  local: string;
  remote: string;
  /** Single-file pair: `local` names a file, not a folder. */
  file?: boolean;
  paused?: boolean;
}

export interface SyncTwin {
  pairId: string;
  /** Wire path on the server: `<storage>://<rel>`. */
  remote: string;
}

/**
 * The remote file a local path already mirrors, when the sync engine keeps one.
 *
 * This is the case worth catching. The document is ALREADY on the server, so
 * there is nothing to upload, nothing to write back and nothing to clean up:
 * the editor saves to the server and the sync engine brings the bytes down to
 * this very file. A scratch copy here would be a second, diverging copy of a
 * file the user is watching sync.
 *
 * ⚠ Deliberately the reverse of main.ts's `mirrorPathFor`, and deliberately the
 * LONGEST match: pairing both `docs://` and `docs://reports` is legal, and the
 * shorter pair would answer with a wire path the deeper one is responsible for.
 *
 * ⚠ A PAUSED pair is not a twin. Saving would reach the server and never come
 * back down — the user would look at a stale local file and believe they had
 * saved it. Those fall through to the copy-and-write-back route, which does not
 * depend on the engine running at all.
 */
export function resolveSyncTwin(
  localPath: string,
  pairs: readonly SyncPairView[],
  opts: { platform?: NodeJS.Platform } = {},
): SyncTwin | null {
  const platform = opts.platform ?? process.platform;
  const target = localPathForm(localPath, platform);
  if (!target) return null;

  let best: SyncTwin | null = null;
  let bestDepth = -1;
  for (const p of pairs) {
    if (p.paused) continue;
    const local = localPathForm(p.local, platform);
    if (!local || !local.keyParts.length) continue;
    const depth = local.keyParts.length;
    if (depth <= bestDepth) continue;
    if (p.file) {
      if (local.key === target.key) {
        best = { pairId: p.id, remote: p.remote };
        bestDepth = depth;
      }
      continue;
    }
    // Inside it, compared part by part — never by cutting the path at the
    // folder's length, which a lower-cased "İ" makes one character longer.
    if (target.keyParts.length <= depth) continue;
    if (!local.keyParts.every((part, i) => part === target.keyParts[i])) continue;
    // The rest comes from the ORIGINAL parts, case preserved: the comparison
    // is case-insensitive on Windows, the wire path is not.
    const segs = target.parts.slice(depth);
    best = { pairId: p.id, remote: joinRemote(p.remote, segs) };
    bestDepth = depth;
  }
  return best;
}

/** A local path in the one form this app compares paths by (localPathForm). */
export interface LocalPathForm {
  /** Resolved, and case-folded on Windows: equal keys are the same file. */
  key: string;
  /** Its folders and name, as written (case preserved). */
  parts: string[];
  /** The same, case-folded on Windows: compared part by part. */
  keyParts: string[];
}

/**
 * The one rule for "is this the same file / inside this folder on this
 * computer": the path is resolved (`..`, a trailing separator), and on Windows
 * compared case-insensitively — `C:\Docs\a.docx` and `c:\docs\A.DOCX` are one
 * file. null for no path at all.
 *
 * ⚠ ONE rule: main.ts, OpeningDocs and resolveSyncTwin each lower-cased
 * Windows paths their own way. And "inside" is decided part by part:
 * `"İ".toLowerCase()` is two characters, so cutting the original path at a
 * lower-cased folder's length lost a letter of the file name.
 */
export function localPathForm(p: string, platform: NodeJS.Platform = process.platform): LocalPathForm | null {
  const raw = String(p ?? '');
  if (!raw.trim()) return null;
  const win = platform === 'win32';
  const resolved = (win ? nodePath.win32 : nodePath.posix).resolve(raw);
  const parts = resolved.split(win ? /[\\/]+/ : /\/+/).filter(Boolean);
  return {
    key: win ? resolved.toLowerCase() : resolved,
    parts,
    keyParts: win ? parts.map((s) => s.toLowerCase()) : parts,
  };
}

/** Two paths naming the same file on this computer (localPathForm). */
export function sameLocalPath(a: string, b: string, platform: NodeJS.Platform = process.platform): boolean {
  const fa = localPathForm(a, platform);
  const fb = localPathForm(b, platform);
  return !!fa && !!fb && fa.key === fb.key;
}

/** `docs://` + [a,b] → `docs://a/b`; `docs://x` + [a] → `docs://x/a`. */
export function joinRemote(base: string, segments: readonly string[]): string {
  const b = String(base ?? '');
  const clean = b.endsWith('://') ? b : b.replace(/\/+$/, '');
  const rest = segments.filter(Boolean).join('/');
  if (!rest) return clean;
  return clean.endsWith('://') ? clean + rest : clean + '/' + rest;
}

// ─────────────────────────── scratch naming ───────────────────────────

/** 12 hex characters. Long enough that two documents opened in the same second
 *  cannot collide, short enough to leave the real name readable in a listing. */
export function newSessionId(): string {
  return crypto.randomBytes(6).toString('hex');
}

/**
 * The name the copy gets on the server: `<session>-<original name>`.
 *
 * ⚠ The session id goes FIRST. The extension has to stay last (OnlyOffice picks
 * its editor from it, and the server's own type sniffing agrees), and a suffix
 * before the extension is what turns `rapor.docx` into `rapor-a1b2.docx` — two
 * things that read as two documents in a listing rather than as one document
 * and its working copy.
 *
 * ⚠ Non-ASCII is KEPT. The people this feature is for have files called
 * `Bütçe Özeti.xlsx`; mangling that to `B_t_e_zeti.xlsx` would make the one
 * place they ever see the copy (the trash, afterwards) unreadable. Only the
 * characters that are illegal in a path segment on some platform, and the ones
 * the server's own upload guard refuses (`\`, `/`), are replaced.
 */
export function scratchBasename(localPath: string, sessionId: string): string {
  const base = String(localPath).split(/[\\/]/).pop() ?? '';
  const dot = base.lastIndexOf('.');
  const stemRaw = dot > 0 ? base.slice(0, dot) : base;
  const ext = dot > 0 ? base.slice(dot + 1).toLowerCase() : '';
  let stem = stemRaw
    .replace(/[\u0000-\u001f\u007f]/g, '')
    .replace(/[\\/:*?"<>|]/g, '_')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^\.+/, '')
    .replace(/[. ]+$/, '');
  if (stem.length > 60) stem = stem.slice(0, 60).replace(/[. ]+$/, '');
  if (!stem) stem = 'document';
  return ext ? sessionId + '-' + stem + '.' + ext : sessionId + '-' + stem;
}

/** `<storage>://.filex-open/<name>` */
export function scratchRemotePath(storage: string, basename: string): string {
  return storage + '://' + SCRATCH_DIR_NAME + '/' + basename;
}

/** `<storage>://.filex-open` */
export function scratchRemoteDir(storage: string): string {
  return storage + '://' + SCRATCH_DIR_NAME;
}

// ─────────────────────────── change detection ───────────────────────────

/** What a listing says about one remote file. All three fields are optional
 *  because different storage drivers fill different ones. */
export interface RemoteStat {
  size?: number;
  lastModified?: number;
  etag?: string;
}

/**
 * One comparable string per version of a remote file.
 *
 * ⚠ Size alone is not enough and mtime alone is not enough: a one-character
 * edit in a compressed .docx frequently comes back the same length, and some
 * drivers report no mtime at all. The etag is used when the server offers one;
 * the size+mtime pair is the fallback.
 */
export function fingerprint(s: RemoteStat | null | undefined): string {
  if (!s) return '';
  if (s.etag) return 'etag:' + s.etag;
  return 'sm:' + (s.size ?? -1) + ':' + (s.lastModified ?? -1);
}

/** True when the remote file is not the version named by `baseline`. An absent
 *  `current` (the file is gone) is NOT a change to write back. */
export function hasChanged(
  baseline: RemoteStat | null | undefined,
  current: RemoteStat | null | undefined,
): boolean {
  if (!current) return false;
  const a = fingerprint(baseline);
  const b = fingerprint(current);
  return Boolean(b) && a !== b;
}

// ─────────────────────────── the local document's version ───────────────────────────
//
// Issue #184. The document on this computer is not filex's alone while it is
// open: an agent rewrites it, another editor saves it, a sync client brings a
// newer copy down. filex used to write every save over it without a look, so
// the first save after such a change destroyed it — silently, since the user
// only ever saw their own edit land.
//
// So filex remembers the version it last READ from the file (at opening) or
// last WROTE to it (each write-back), and compares before it writes again.

/** One version of the local document, as filex last read or wrote it. */
export interface LocalVersion {
  size: number;
  /** As the file system reports it (fractional milliseconds on some). */
  mtimeMs: number;
  /** sha256 of the bytes, hex — the truth. Size and timestamp are the fast path. */
  sha256: string;
  /** When filex recorded it (Date.now()): the racy-timestamp rule needs it. */
  at: number;
}

/**
 * How close to the moment filex looked a write may have happened and still
 * share its timestamp with a later one. FAT keeps two-second timestamps, and
 * SMB and some FUSE mounts whole seconds: inside this window two writes of the
 * same length are indistinguishable by size and mtime, so the content decides
 * (git's "racily clean" rule).
 */
export const RACY_WINDOW_MS = 3000;

export function sha256Hex(bytes: Buffer | Uint8Array): string {
  return crypto.createHash('sha256').update(bytes).digest('hex');
}

function versionFrom(st: { size: number; mtimeMs: number }, bytes: Buffer | Uint8Array, at: number): LocalVersion {
  return { size: st.size, mtimeMs: st.mtimeMs, sha256: sha256Hex(bytes), at };
}

function isGone(err: unknown): boolean {
  const code = (err as NodeJS.ErrnoException)?.code;
  return code === 'ENOENT' || code === 'ENOTDIR' || code === 'EISDIR';
}

/**
 * The file's bytes and its version, read through ONE handle: the stat and the
 * bytes are of the same file even when somebody renames another one over the
 * name in between. null when there is no regular file there.
 */
export async function readLocalVersion(
  p: string,
  opts: { now?: number } = {},
): Promise<{ version: LocalVersion; bytes: Buffer } | null> {
  const fh = await fs.promises.open(p, 'r').catch((err: unknown) => {
    if (isGone(err)) return null;
    throw err;
  });
  if (!fh) return null;
  try {
    const st = await fh.stat();
    if (!st.isFile()) return null;
    const bytes = await fh.readFile();
    return { version: versionFrom({ size: bytes.length, mtimeMs: st.mtimeMs }, bytes, opts.now ?? Date.now()), bytes };
  } finally {
    await fh.close().catch(() => undefined);
  }
}

/** What the local document is now, next to the version filex last knew. */
export type LocalDrift =
  /** The same content. `version` may carry a newer timestamp (a touch) and is
   *  the baseline to keep from here on. */
  | { kind: 'same'; version: LocalVersion }
  /** Different bytes: somebody else wrote it. */
  | { kind: 'changed'; version: LocalVersion; bytes: Buffer }
  /** No regular file at that path any more (deleted, moved, replaced by a folder). */
  | { kind: 'gone' };

/**
 * Has the document changed since `baseline`?
 *
 * Size and timestamp equal, and the baseline not racy: the same, without
 * reading a byte. Anything else is decided by the CONTENT — a timestamp that
 * moved with the same bytes (a backup tool, `touch`, an editor saving what it
 * loaded) is not a change, and must not ask the user a question about one.
 */
export async function localDrift(
  p: string,
  baseline: LocalVersion,
  opts: { now?: number } = {},
): Promise<LocalDrift> {
  let st: fs.Stats;
  try {
    st = await fs.promises.stat(p);
  } catch (err) {
    if (isGone(err)) return { kind: 'gone' };
    throw err;
  }
  if (!st.isFile()) return { kind: 'gone' };
  const racy = baseline.mtimeMs >= baseline.at - RACY_WINDOW_MS;
  if (!racy && st.size === baseline.size && st.mtimeMs === baseline.mtimeMs) {
    return { kind: 'same', version: baseline };
  }
  const read = await readLocalVersion(p, opts);
  if (!read) return { kind: 'gone' };
  if (read.version.sha256 === baseline.sha256) return { kind: 'same', version: read.version };
  return { kind: 'changed', version: read.version, bytes: read.bytes };
}

/**
 * A save that was NOT written because the document changed outside filex since
 * filex last read or wrote it (issue #184). Nothing was written anywhere: the
 * caller still holds the bytes and decides with the user what becomes of them.
 */
export class LocalChangedError extends Error {
  readonly current: LocalVersion | null;
  constructor(target: string, current: LocalVersion | null) {
    super(target + ' changed outside filex since filex last read it - the save was not written over it');
    this.name = 'LocalChangedError';
    this.current = current;
  }
}

/**
 * `report.docx` → `report.filex-conflict-20261006T101500.docx`, beside it; the
 * n-th one in the same second `report.filex-conflict-20261006T101500-n.docx`.
 */
export function conflictPathFor(target: string, stamp: string, n = 1): string {
  return besidePath(target, '.filex-conflict-' + stamp + (n > 1 ? '-' + n : ''));
}

/** `<dir>/<stem><tag><.ext>`: a file named after the document, beside it,
 *  with the document's own extension last (the app that opens it picks by it). */
function besidePath(target: string, tag: string): string {
  const dir = nodePath.dirname(target);
  const base = nodePath.basename(target);
  const dot = base.lastIndexOf('.');
  const stem = dot > 0 ? base.slice(0, dot) : base;
  const ext = dot > 0 ? base.slice(dot) : '';
  return nodePath.join(dir, stem + tag + ext);
}

/**
 * Writes filex's version of a document BESIDE it, as a conflict copy, and says
 * where. Exclusive create: an existing file is never replaced — a second copy
 * in the same second gets `-2`, `-3`, … before the extension.
 */
export async function writeConflictCopy(
  target: string,
  bytes: Buffer | Uint8Array,
  opts: { now?: Date } = {},
): Promise<string> {
  const stamp = stampOf(opts.now ?? new Date());
  for (let n = 1; n < 100; n++) {
    const p = conflictPathFor(target, stamp, n);
    try {
      await fs.promises.writeFile(p, bytes, { flag: 'wx' });
      return p;
    } catch (err) {
      if ((err as NodeJS.ErrnoException)?.code === 'EEXIST') continue;
      throw err;
    }
  }
  throw new Error('no free name for a conflict copy beside ' + target);
}

// ─────────────────────────── write-back ───────────────────────────

/**
 * A write-back that did not land on the original file, carrying WHERE the bytes
 * ended up instead.
 *
 * ⚠ This type exists so the failure cannot be swallowed. A silent failed
 * write-back is the worst thing this feature can produce: the user saved in the
 * editor, saw no error, and their document did not change. The caller is
 * expected to shout — a dialog, not a log line — and `keptAt` is what it names.
 */
export class WriteBackError extends Error {
  readonly keptAt: string | null;
  constructor(message: string, keptAt: string | null) {
    super(message);
    this.name = 'WriteBackError';
    this.keptAt = keptAt;
  }
}

/** `report.docx` → `report.filex-recovered-20260904T101500.docx`, beside it. */
export function recoveryPathFor(target: string, stamp: string): string {
  return besidePath(target, '.filex-recovered-' + stamp);
}

function stampOf(now: Date): string {
  return now.toISOString().replace(/[-:]/g, '').replace(/\..+$/, '');
}

/**
 * Replaces the original document with the edited bytes, atomically.
 *
 * Temp file in the SAME directory, then rename over the target: a rename inside
 * one directory is atomic on every filesystem this app runs on, so a reader
 * (Explorer's preview pane, a backup agent, the sync engine) sees either the
 * old document or the new one, never a half-written one. Writing straight over
 * the target would leave a truncated file behind if the process died — and the
 * file it truncated is the user's only copy.
 *
 * ⚠ Same directory, never the OS temp dir. A rename across drives is EXDEV, and
 * the copy+delete fallback it needs is exactly the non-atomic write this
 * function exists to avoid.
 *
 * ⚠ The target must still exist. A document the user deleted or moved while it
 * was open must not be resurrected by a background save — those bytes go to a
 * recovery file next to where it used to be, and the caller says so out loud.
 *
 * ⚠⚠ And it must still be the version filex knows (`expect`, issue #184). A
 * document somebody else rewrote since filex last read or wrote it is not
 * written over: LocalChangedError, nothing written anywhere, and the caller
 * asks the user. The look is taken AFTER the temp file is on disk, right
 * before the rename, so the window in which an outside write can still slip
 * under the save is as short as the file system allows (one stat, and a read
 * only when the stat cannot tell). `expect` left out is the caller saying it
 * knows of no version to protect — the sweep's recovery, a test.
 *
 * Returns the version it left on disk: filex's own write, which the caller
 * keeps as the new baseline so it is never mistaken for an outside change.
 */
export async function writeBackAtomic(
  target: string,
  bytes: Buffer | Uint8Array,
  opts: { fallbackDir?: string; now?: Date; expect?: LocalVersion | null } = {},
): Promise<LocalVersion> {
  const dir = nodePath.dirname(target);
  const stamp = stampOf(opts.now ?? new Date());

  let mode: number | undefined;
  try {
    const st = await fs.promises.stat(target);
    if (!st.isFile()) throw new Error('not a regular file');
    mode = st.mode;
  } catch {
    const kept = await stash(bytes, recoveryPathFor(target, stamp), opts.fallbackDir, stamp);
    throw new WriteBackError(
      'the original file is no longer at ' + target + ' - the edit was not applied',
      kept,
    );
  }

  const tmp = nodePath.join(
    dir,
    '.' + nodePath.basename(target) + '.filex-openwith-' + crypto.randomBytes(4).toString('hex'),
  );
  try {
    await fs.promises.writeFile(tmp, bytes);
    // Keep the document's permissions. A fresh temp file is created with the
    // process umask, so without this a group-readable document quietly became
    // owner-only the first time it was edited through filex.
    if (mode !== undefined) await fs.promises.chmod(tmp, mode & 0o777).catch(() => undefined);
  } catch (err) {
    await fs.promises.rm(tmp, { force: true }).catch(() => undefined);
    const kept = await stash(bytes, recoveryPathFor(target, stamp), opts.fallbackDir, stamp);
    throw new WriteBackError(
      'could not write next to ' + target + ': ' + String((err as Error)?.message ?? err),
      kept,
    );
  }

  if (opts.expect) {
    let drift: LocalDrift;
    try {
      drift = await localDrift(target, opts.expect);
    } catch {
      // Unreadable right now (locked mid-write by whoever is changing it):
      // not provably unchanged, so not written over.
      await fs.promises.rm(tmp, { force: true }).catch(() => undefined);
      throw new LocalChangedError(target, null);
    }
    if (drift.kind === 'changed') {
      await fs.promises.rm(tmp, { force: true }).catch(() => undefined);
      throw new LocalChangedError(target, drift.version);
    }
    if (drift.kind === 'gone') {
      await fs.promises.rm(tmp, { force: true }).catch(() => undefined);
      const kept = await stash(bytes, recoveryPathFor(target, stamp), opts.fallbackDir, stamp);
      throw new WriteBackError(
        'the original file is no longer at ' + target + ' - the edit was not applied',
        kept,
      );
    }
  }

  try {
    await fs.promises.rename(tmp, target);
  } catch (err) {
    // The document is open in something that locks it (Windows), or the
    // directory turned read-only. The bytes are already on disk — move them
    // where the user can find them rather than deleting the only copy of the
    // edit.
    let kept: string | null = null;
    try {
      const rec = recoveryPathFor(target, stamp);
      await fs.promises.rename(tmp, rec);
      kept = rec;
    } catch {
      kept = tmp; // could not even rename it — leave it where it is
    }
    throw new WriteBackError(
      'could not replace ' + target + ': ' + String((err as Error)?.message ?? err),
      kept,
    );
  }

  // What is on disk now is filex's own write. Its timestamp is the file
  // system's; the hash is of the bytes written, not re-read — a re-read could
  // already be somebody else's next write, which would then pass for ours.
  const at = Date.now();
  const st = await fs.promises.stat(target).catch(() => null);
  return versionFrom({ size: bytes.length, mtimeMs: st?.mtimeMs ?? -1 }, bytes, at);
}

// ─────────────────────────── watching the local document ───────────────────────────

export interface LocalDocMonitorOptions {
  /** The document on this computer. */
  path: string;
  /** The version filex read when it opened it. */
  baseline: LocalVersion;
  /** A version filex did not write appeared — once per version. */
  onChange: (version: LocalVersion, bytes: Buffer) => void;
  /** Quiet time after the last folder event before looking. */
  debounceMs?: number;
  platform?: NodeJS.Platform;
  /** A look that could not be taken, a watch that died. Never thrown. */
  onError?: (err: unknown) => void;
}

/**
 * Watches one local document for changes made OUTSIDE filex (issue #184).
 *
 * ⚠ The FOLDER is watched, not the file. Agents and most editors save through
 * a temp file renamed over the document; a watch on the file follows the old
 * inode and goes deaf at the first such save. Events for other names in the
 * folder are dropped (a null name — some platforms give none — is looked at).
 *
 * ⚠ An event only says "look". What decides is localDrift against the
 * baseline: a touched timestamp with the same bytes is no change, filex's OWN
 * write-back is the new baseline (ownWrite), and one outside version is
 * reported once however many events and looks it takes.
 *
 * The folder watch is the fast path. Network drives and some FUSE mounts send
 * no events, so the open-with poll calls check() every tick as well.
 */
export class LocalDocMonitor {
  // Plain fields, not constructor parameter properties (see SessionStore).
  private readonly file: string;
  private readonly nameKey: string;
  private readonly fold: boolean;
  private readonly debounceMs: number;
  private readonly onChange: LocalDocMonitorOptions['onChange'];
  private readonly onError?: LocalDocMonitorOptions['onError'];
  private base: LocalVersion;
  private watcher: fs.FSWatcher | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private writing = 0;
  private heardWhileWriting = false;
  private inFlight: Promise<LocalDrift> | null = null;
  private again = false;
  private reported = '';
  private stopped = false;

  constructor(opts: LocalDocMonitorOptions) {
    this.file = opts.path;
    const platform = opts.platform ?? process.platform;
    // Windows and macOS file systems are case-insensitive by default: the
    // event may name the document in another case than it was opened by.
    this.fold = platform === 'win32' || platform === 'darwin';
    const name = nodePath.basename(opts.path);
    this.nameKey = this.fold ? name.toLowerCase() : name;
    this.debounceMs = Math.max(0, opts.debounceMs ?? 400);
    this.onChange = opts.onChange;
    this.onError = opts.onError;
    this.base = opts.baseline;
  }

  /** The version filex last read or wrote: what a save may replace. */
  get baseline(): LocalVersion {
    return this.base;
  }

  get path(): string {
    return this.file;
  }

  start(): void {
    if (this.watcher || this.stopped) return;
    try {
      this.watcher = fs.watch(nodePath.dirname(this.file), { persistent: false }, (_event, name) => {
        if (name) {
          const n = String(name);
          if ((this.fold ? n.toLowerCase() : n) !== this.nameKey) return;
        }
        this.schedule();
      });
      this.watcher.on('error', (err) => {
        // The folder went away, or the platform gave up on it. The poll's
        // look carries on without it.
        this.onError?.(err);
        this.closeWatcher();
      });
    } catch (err) {
      this.onError?.(err);
      this.closeWatcher();
    }
  }

  stop(): void {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.closeWatcher();
  }

  /** filex now stands on this version (it wrote it, or the user chose to
   *  keep it): the baseline from here on, and never an outside change. */
  adopt(v: LocalVersion): void {
    this.base = v;
    this.reported = '';
  }

  /** This outside version is already being dealt with (the write-back found
   *  it first): not reported again. The baseline stays where it was. */
  markSeen(v: LocalVersion | null): void {
    if (v) this.reported = v.sha256;
  }

  /**
   * filex writes the document itself. Looks are held while it does — the
   * folder event of its own rename must not be judged against the old
   * baseline — and the version it wrote becomes the baseline before they
   * resume.
   */
  async ownWrite<T extends LocalVersion>(write: () => Promise<T>): Promise<T> {
    this.writing++;
    try {
      const v = await write();
      this.adopt(v);
      return v;
    } finally {
      this.writing--;
      if (this.writing === 0 && this.heardWhileWriting) {
        this.heardWhileWriting = false;
        this.schedule();
      }
    }
  }

  /** Look now. Reports (onChange) a new outside version; returns what it saw. */
  async check(): Promise<LocalDrift> {
    if (this.stopped) return { kind: 'same', version: this.base };
    if (this.writing > 0) {
      this.heardWhileWriting = true;
      return { kind: 'same', version: this.base };
    }
    if (this.inFlight) {
      this.again = true;
      return this.inFlight;
    }
    this.inFlight = this.look();
    try {
      return await this.inFlight;
    } finally {
      this.inFlight = null;
      if (this.again) {
        this.again = false;
        this.schedule();
      }
    }
  }

  private async look(): Promise<LocalDrift> {
    const base = this.base;
    let drift: LocalDrift;
    try {
      drift = await localDrift(this.file, base);
    } catch (err) {
      // Locked mid-write, a permission blip: the next event or tick looks
      // again.
      this.onError?.(err);
      return { kind: 'same', version: base };
    }
    // filex wrote (or adopted) meanwhile: this look was against a baseline
    // that is no longer the one, so the next look decides.
    if (this.base !== base || this.writing > 0) {
      this.schedule();
      return drift;
    }
    if (drift.kind === 'same') {
      // A touch (same bytes, newer timestamp), or a racy baseline confirmed:
      // keep the newer record so the next look is the cheap one.
      this.base = drift.version;
      return drift;
    }
    if (drift.kind !== 'changed') return drift;
    // ⚠ Settled, or not yet: a writer still writing IN PLACE gives a file
    // that moves under the read. Looked at again after the next quiet spell
    // rather than reported half-written.
    const now = await fs.promises.stat(this.file).catch(() => null);
    if (!now || now.size !== drift.version.size || now.mtimeMs !== drift.version.mtimeMs) {
      this.schedule();
      return drift;
    }
    if (drift.version.sha256 === this.reported) return drift;
    this.reported = drift.version.sha256;
    try {
      this.onChange(drift.version, drift.bytes);
    } catch (err) {
      this.onError?.(err);
    }
    return drift;
  }

  private schedule(): void {
    if (this.stopped) return;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.check();
    }, this.debounceMs);
  }

  private closeWatcher(): void {
    try {
      this.watcher?.close();
    } catch {
      /* already closed */
    }
    this.watcher = null;
  }
}

/** Last resort: get the bytes onto disk somewhere and report where. */
async function stash(
  bytes: Buffer | Uint8Array,
  preferred: string,
  fallbackDir: string | undefined,
  stamp: string,
): Promise<string | null> {
  try {
    await fs.promises.writeFile(preferred, bytes);
    return preferred;
  } catch {
    /* the original's directory is not usable — try the app's own */
  }
  if (!fallbackDir) return null;
  try {
    await fs.promises.mkdir(fallbackDir, { recursive: true });
    const p = nodePath.join(fallbackDir, stamp + '-' + nodePath.basename(preferred));
    await fs.promises.writeFile(p, bytes);
    return p;
  } catch {
    return null;
  }
}

// ─────────────────────────── a save in another format (#151) ───────────────────────────
//
// ONLYOFFICE writes no Word 97, Excel 97 or PowerPoint 97 file: an edited .doc
// comes back as DOCX, an .xls as XLSX (measured on Docs 9.4 with its defaults;
// a Document Server with `assemblyFormatAsOrigin: false` does the same to an
// ODF file and to a CSV). Since 0.51 the server's callback leaves the working
// copy as it is and writes the edit BESIDE it in the format it came back in
// (backend onlyoffice/callback_format.go saveBeside):
//
//     .filex-open/a1b2c3d4e5f6-rapor.doc     the working copy, unchanged
//     .filex-open/a1b2c3d4e5f6-rapor.docx    the edit
//
// The write-back watched only the working copy, so it never saw the edit:
// rapor.doc did not change, nobody was told (the server keeps its notices
// about `.filex-open` to itself), and the edit went with the working folder
// when the session was cleaned up. Now the desktop does on this computer what
// the server does on a storage: rapor.doc is left as it is and the edit goes
// beside it, as rapor.docx - never over another file of that name.

/**
 * The formats a save may come back in beside the working copy: the ones the
 * server's callback keeps (callback_format.go besideTypes). Held to that list
 * by the shared drift file (backend/internal/api/handlers/testdata/
 * rule-mirrors.json, web/tests/lib/serverRuleVectors.test.ts - #211).
 */
export const BESIDE_FORMATS: ReadonlySet<string> = new Set([
  'docx', 'xlsx', 'pptx', 'docm', 'xlsm', 'pptm', 'odt', 'ods', 'odp',
]);

/** One entry of the scratch folder's listing (openwith-io.ts RemoteEntry, structurally). */
export interface ScratchEntry {
  basename: string;
  type?: 'file' | 'dir';
  size?: number;
  lastModified?: number;
  etag?: string;
}

/** The last segment of a wire path: a copy's name in its folder. */
function wireBase(remote: string): string {
  return remote.slice(remote.lastIndexOf('/') + 1);
}

/** What one listing says about one file in it, or null when it is not there. */
export function statIn(entries: readonly ScratchEntry[], basename: string): RemoteStat | null {
  const hit = entries.find((e) => e.basename === basename && (e.type ?? 'file') === 'file');
  return hit ? { size: hit.size, lastModified: hit.lastModified, etag: hit.etag } : null;
}

/** A save the server wrote beside a working copy, in another format. */
export interface BesideCopy {
  basename: string;
  /** Its format: the extension, lower case, no dot. */
  ext: string;
  /** 1 for `<copy>.docx`, n for `<copy> (n).docx`: the order they were written in. */
  n: number;
  stat: RemoteStat;
}

/**
 * The saves the server wrote BESIDE one working copy, in another format,
 * oldest first.
 *
 * `<session>-rapor.doc` → `<session>-rapor.docx`, and the next save of the
 * same copy `<session>-rapor (2).docx` - the server finds the first name
 * taken (ops.UniqueDestNumbered). The working copy's own format, a format the
 * server does not keep beside, a conflict copy (`.filex-conflict-`) and
 * another document's copy (another session id, another name) do not match.
 */
export function besideSaves(copyBasename: string, entries: readonly ScratchEntry[]): BesideCopy[] {
  const dot = copyBasename.lastIndexOf('.');
  if (dot <= 0) return [];
  const stem = copyBasename.slice(0, dot);
  const own = copyBasename.slice(dot + 1).toLowerCase();
  const out: BesideCopy[] = [];
  for (const e of entries) {
    if ((e.type ?? 'file') !== 'file') continue;
    const d = e.basename.lastIndexOf('.');
    if (d <= 0) continue;
    const ext = e.basename.slice(d + 1).toLowerCase();
    if (ext === own || !BESIDE_FORMATS.has(ext)) continue;
    let base = e.basename.slice(0, d);
    let n = 1;
    const numbered = / \((\d+)\)$/.exec(base);
    if (numbered && base.slice(0, numbered.index) === stem) {
      n = Number(numbered[1]);
      base = stem;
    }
    if (base !== stem) continue;
    out.push({ basename: e.basename, ext, n, stat: { size: e.size, lastModified: e.lastModified, etag: e.etag } });
  }
  return out.sort((a, b) => a.n - b.n || (a.stat.lastModified ?? 0) - (b.stat.lastModified ?? 0));
}

/**
 * The beside saves of one working copy that have not come home yet: newer than
 * what the session brought back of each (`brought`, OpenWithSession.beside).
 * Oldest first - the last one is the newest edit, and it holds the ones
 * before it (they are saves of the same document, one after another).
 */
export function unseenBesideSaves(
  copyRemote: string,
  entries: readonly ScratchEntry[],
  brought: ReadonlyArray<{ remote: string; seen: RemoteStat | null }> | undefined,
): BesideCopy[] {
  const known = new Map((brought ?? []).map((b) => [wireBase(b.remote), b.seen] as const));
  return besideSaves(wireBase(copyRemote), entries).filter((b) => hasChanged(known.get(b.basename) ?? null, b.stat));
}

/** The family a type is saved in: which OOXML, ODF or old binary format a save of it can come back as. */
const FAMILY: Readonly<Record<string, 'word' | 'cell' | 'slide'>> = {
  docx: 'word', doc: 'word', odt: 'word', rtf: 'word',
  xlsx: 'cell', xls: 'cell', ods: 'cell', csv: 'cell',
  pptx: 'slide', ppt: 'slide', odp: 'slide',
};
const OOXML_OF = { word: 'docx', cell: 'xlsx', slide: 'pptx' } as const;
const BINARY_OF = { word: 'doc', cell: 'xls', slide: 'ppt' } as const;
const ODF_TYPES: ReadonlyArray<readonly [string, string]> = [
  ['application/vnd.oasis.opendocument.text', 'odt'],
  ['application/vnd.oasis.opendocument.spreadsheet', 'ods'],
  ['application/vnd.oasis.opendocument.presentation', 'odp'],
];
const ZIP_MAGIC = [0x50, 0x4b, 0x03, 0x04];
const OLE_MAGIC = [0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1];

function startsWithBytes(b: Uint8Array, magic: readonly number[]): boolean {
  if (b.length < magic.length) return false;
  for (let i = 0; i < magic.length; i++) if (b[i] !== magic[i]) return false;
  return true;
}

/**
 * The ODF type a zip says it is - its first entry is `mimetype`, stored, and
 * names it (the ODF package rule) - or null.
 */
function odfTypeOf(b: Uint8Array): string | null {
  if (b.length < 30) return null;
  const nameLen = b[26] | (b[27] << 8);
  const extraLen = b[28] | (b[29] << 8);
  if (Buffer.from(b.subarray(30, 30 + nameLen)).toString('latin1') !== 'mimetype') return null;
  const start = 30 + nameLen + extraLen;
  // The media type runs up to the next entry's header (`PK…`, upper case,
  // which no media type has): `…opendocument.text`, and not the
  // `…opendocument.text-template` that starts the same.
  const text = Buffer.from(b.subarray(start, start + 96)).toString('latin1');
  const mime = /^[a-z0-9.+\/-]+/.exec(text)?.[0] ?? '';
  for (const [type, ext] of ODF_TYPES) if (mime === type) return ext;
  return null;
}

/**
 * The format a save's BYTES are in, when that is not the document's own and
 * the bytes say so beyond doubt; null when they may be written as the
 * document (the bytes are its format, or nothing about them can be told).
 *
 * The server says it by the name it writes (besideSaves). This is for a
 * server that does not: before 0.51 the callback wrote ONLYOFFICE's DOCX for
 * a .doc, and the XLSX of a Document Server with `assemblyFormatAsOrigin:
 * false` for a .csv, over the working copy itself - and the write-back would
 * have put a zip under the name of the person's .doc or .csv. A zip is OOXML
 * when it carries `[Content_Types].xml`, ODF when its first entry says so; an
 * old Office file is an OLE compound file. A CSV saved as CSV is text, and is
 * written over the .csv as it always was (0.52's KeepCSV kept its cells on
 * the server already).
 */
export function savedFormatOf(localPath: string, bytes: Uint8Array): string | null {
  const ext = extensionOf(localPath);
  const family = FAMILY[ext];
  if (!family) return null;
  if (startsWithBytes(bytes, ZIP_MAGIC)) {
    const odf = odfTypeOf(bytes);
    if (odf) return odf === ext ? null : odf;
    if (!Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).includes('[Content_Types].xml', 0, 'latin1')) {
      return null;
    }
    return ext === OOXML_OF[family] ? null : OOXML_OF[family];
  }
  if (startsWithBytes(bytes, OLE_MAGIC)) {
    return ext === BINARY_OF[family] ? null : BINARY_OF[family];
  }
  return null;
}

/** `rapor.doc` + `docx` → `rapor.docx`, in the same folder. */
export function besidePathFor(localPath: string, ext: string): string {
  const base = nodePath.basename(localPath);
  const dot = base.lastIndexOf('.');
  const stem = dot > 0 ? base.slice(0, dot) : base;
  return nodePath.join(nodePath.dirname(localPath), stem + '.' + ext.toLowerCase());
}

/** `rapor.docx`, 2 → `rapor (2).docx`: the server's numbering (ops.UniqueDestNumbered). */
export function numberedPathFor(p: string, n: number): string {
  return n > 1 ? besidePath(p, ' (' + n + ')') : p;
}

/** Where a session's saves in another format go on this computer. */
export interface BesideTarget {
  /** The format: the extension, lower case, no dot. */
  ext: string;
  path: string;
  /** What filex last wrote there: a save goes over it only while it is still this (#184's rule). */
  version: LocalVersion;
}

/**
 * What became of one save in another format:
 *   created  - a new file beside the document: `rapor.docx`, or `rapor (2).docx`
 *              when that name was taken (an earlier edit, somebody's own file);
 *   updated  - over the file this session made, still as filex left it;
 *   conflict - that file changed outside filex since: the save went to a
 *              conflict copy beside it, and the session's next saves go there.
 */
export type BesideOutcome = 'created' | 'updated' | 'conflict';

async function writtenVersion(p: string, bytes: Buffer | Uint8Array): Promise<LocalVersion> {
  const st = await fs.promises.stat(p).catch(() => null);
  return versionFrom({ size: bytes.length, mtimeMs: st?.mtimeMs ?? -1 }, bytes, Date.now());
}

/**
 * Writes a save that came back in another format (`ext`) BESIDE the person's
 * document - never over it, and never over another file.
 *
 * The first one of a session takes `rapor.docx` beside `rapor.doc` - created,
 * never replaced: a `rapor.docx` already there is an earlier edit (a previous
 * session saved one, and the person opened the unchanged .doc again) or a
 * file of the person's own, and the save takes `rapor (2).docx`, the next free
 * name, as the server does. Every later save of the session in that format goes
 * to the SAME file (`target`, what the previous call returned), held to the
 * version filex wrote there: rewritten outside filex since (the person opened
 * it in another program), it is not written over - the save goes to a conflict
 * copy beside it (`rapor.filex-conflict-<time>.docx`), where the next ones go.
 *
 * A folder that takes no new file: WriteBackError, with the edit kept in
 * `fallbackDir` (the caller says where, out loud). The file this session made,
 * gone since: WriteBackError the same way (writeBackAtomic) - not recreated.
 */
export async function writeBesideSave(
  localPath: string,
  ext: string,
  bytes: Buffer | Uint8Array,
  target: BesideTarget | null,
  opts: { fallbackDir?: string; now?: Date } = {},
): Promise<{ target: BesideTarget; outcome: BesideOutcome }> {
  const format = ext.toLowerCase();
  if (target && target.ext === format) {
    try {
      const version = await writeBackAtomic(target.path, bytes, {
        fallbackDir: opts.fallbackDir,
        now: opts.now,
        expect: target.version,
      });
      return { target: { ext: format, path: target.path, version }, outcome: 'updated' };
    } catch (err) {
      if (!(err instanceof LocalChangedError)) throw err;
    }
    // Named after the document, in the save's format - `rapor.filex-conflict-
    // <time>.docx` - as a conflict copy of the document itself is (#184).
    const named = besidePathFor(localPath, format);
    let kept: string;
    try {
      kept = await writeConflictCopy(named, bytes, { now: opts.now });
    } catch (err) {
      const stamp = stampOf(opts.now ?? new Date());
      const at = await stash(bytes, recoveryPathFor(named, stamp), opts.fallbackDir, stamp);
      throw new WriteBackError(
        'could not keep a conflict copy beside ' + target.path + ': ' + String((err as Error)?.message ?? err),
        at,
      );
    }
    return { target: { ext: format, path: kept, version: await writtenVersion(kept, bytes) }, outcome: 'conflict' };
  }
  const want = besidePathFor(localPath, format);
  for (let n = 1; n < 100; n++) {
    const p = numberedPathFor(want, n);
    try {
      // Exclusive: an existing file of that name is never replaced.
      await fs.promises.writeFile(p, bytes, { flag: 'wx' });
    } catch (err) {
      if ((err as NodeJS.ErrnoException)?.code === 'EEXIST') continue;
      const stamp = stampOf(opts.now ?? new Date());
      const kept = await stash(bytes, recoveryPathFor(want, stamp), opts.fallbackDir, stamp);
      throw new WriteBackError(
        'could not write ' + nodePath.basename(p) + ' beside ' + localPath + ': ' + String((err as Error)?.message ?? err),
        kept,
      );
    }
    return { target: { ext: format, path: p, version: await writtenVersion(p, bytes) }, outcome: 'created' };
  }
  const stamp = stampOf(opts.now ?? new Date());
  const kept = await stash(bytes, recoveryPathFor(want, stamp), opts.fallbackDir, stamp);
  throw new WriteBackError('no free name for ' + nodePath.basename(want) + ' beside ' + localPath, kept);
}

// ─────────────────────────── sessions on disk ───────────────────────────

/**
 * One document being edited through a scratch copy.
 *
 * Written to disk BEFORE the editor opens, not after: the record is what makes
 * a crash recoverable. Without it a killed app leaves a copy on the server that
 * nothing knows about, and possibly an edit that never came home.
 */
export interface OpenWithSession {
  id: string;
  accountId: string;
  serverUrl: string;
  storage: string;
  /** The document on this machine — the only path write-back may touch. */
  localPath: string;
  /** The scratch copy's wire path. */
  remote: string;
  createdAt: string;
  updatedAt: string;
  /** The version uploaded, or last brought back. Anything newer is an edit. */
  seen: RemoteStat | null;
  /** The pid that owns it. Any other pid means a previous run. */
  ownerPid: number;
  /**
   * Earlier working copies of this session (issue #184): each outside change
   * the editor took in was uploaded as a NEW copy and the editor moved to it,
   * so a late save of the old editor lands on the old copy and never over the
   * new version. Swept like `remote` — an edit on one is recovered beside the
   * document, never over it.
   */
  retired?: Array<{ remote: string; seen: RemoteStat | null }>;
  /**
   * Saves the server wrote beside a working copy in another format (#151,
   * besideSaves) that this session brought home, or chose not to, and the
   * version it saw of each. Swept with the copies: one newer than this after
   * a crash is recovered beside the document, never over anything.
   */
  beside?: Array<{ remote: string; seen: RemoteStat | null }>;
}

/** Session records under one directory, one JSON file each. */
export class SessionStore {
  // ⚠ A plain field, not a `private dir` constructor parameter property. Node's
  // type stripping (`--experimental-strip-types`, how this module's tests run
  // it) refuses parameter properties: they are the one TypeScript feature in
  // this file that would need real transpilation rather than erasure.
  private readonly dir: string;

  constructor(dir: string) {
    this.dir = dir;
  }

  private file(id: string): string {
    return nodePath.join(this.dir, id + '.json');
  }

  async put(s: OpenWithSession): Promise<void> {
    await fs.promises.mkdir(this.dir, { recursive: true });
    // Temp + rename, for the same reason the document itself gets one: a record
    // truncated by a crash is a record the sweep cannot read, which is a
    // scratch copy nobody will ever delete.
    const tmp = this.file(s.id) + '.part';
    await fs.promises.writeFile(tmp, JSON.stringify(s, null, 2));
    await fs.promises.rename(tmp, this.file(s.id));
  }

  async remove(id: string): Promise<void> {
    await fs.promises.rm(this.file(id), { force: true }).catch(() => undefined);
  }

  async list(): Promise<OpenWithSession[]> {
    let names: string[];
    try {
      names = await fs.promises.readdir(this.dir);
    } catch {
      return [];
    }
    const out: OpenWithSession[] = [];
    for (const n of names) {
      if (!n.endsWith('.json')) continue;
      const full = nodePath.join(this.dir, n);
      try {
        const s = JSON.parse(await fs.promises.readFile(full, 'utf8')) as OpenWithSession;
        if (s && typeof s.id === 'string' && typeof s.localPath === 'string') out.push(s);
        else await fs.promises.rm(full, { force: true }).catch(() => undefined);
      } catch {
        // A record that cannot be read is a record nothing can act on. Drop it,
        // rather than letting one bad file stop the sweep for every other
        // session.
        await fs.promises.rm(full, { force: true }).catch(() => undefined);
      }
    }
    return out;
  }
}

/**
 * The sessions this run inherited — a previous process's, or one so old that
 * this app cannot still be holding it.
 *
 * ⚠ `ownerPid !== currentPid` is the whole test in practice: the app takes a
 * single-instance lock, so a record from another pid cannot belong to a running
 * copy. The age ceiling is the second net, for a reused pid.
 */
export function staleSessions(
  sessions: readonly OpenWithSession[],
  opts: { currentPid: number; now?: number; maxAgeMs?: number },
): OpenWithSession[] {
  const now = opts.now ?? Date.now();
  const maxAge = opts.maxAgeMs ?? 24 * 60 * 60 * 1000;
  return sessions.filter((s) => {
    if (s.ownerPid !== opts.currentPid) return true;
    const started = Date.parse(s.createdAt || '');
    return Number.isFinite(started) && now - started > maxAge;
  });
}

/** True when the scratch copy holds an edit that never reached the local file —
 *  the answer to "may I just delete this?" after a crash. */
export function needsRecovery(s: OpenWithSession, current: RemoteStat | null): boolean {
  return hasChanged(s.seen, current);
}

/**
 * Scratch files on the server that no session record explains and that are old
 * enough to be nobody's working copy.
 *
 * The record store lives in the app's user data, so a reinstall, a new machine
 * or a cleared profile orphans copies the local sweep can never see. This is
 * the second sweep, the one that keeps `.filex-open` from growing forever.
 */
export function orphanScratchEntries(
  entries: readonly { basename: string; lastModified?: number }[],
  known: ReadonlySet<string>,
  opts: { now?: number; maxAgeMs?: number } = {},
): string[] {
  const now = opts.now ?? Date.now();
  const maxAge = opts.maxAgeMs ?? 7 * 24 * 60 * 60 * 1000;
  return entries
    .filter((e) => e.basename && !known.has(e.basename))
    .filter((e) => typeof e.lastModified === 'number' && now - e.lastModified > maxAge)
    .map((e) => e.basename);
}

/**
 * The documents being opened right now: between the double-click and the
 * editor window, while the working copy goes up (up to 256 MB).
 *
 * ⚠ "Is it already open?" only knew documents whose editor was up, so a second
 * double-click in that time opened a second session of the same document: two
 * working copies writing back to one path, the last save winning and the other
 * edit gone without a word. A path is taken here for as long as it is being
 * opened; one opened, or failed, may be opened again.
 */
export class OpeningDocs {
  private readonly keys = new Set<string>();
  private readonly platform: NodeJS.Platform;

  constructor(platform: NodeJS.Platform) {
    this.platform = platform;
  }

  /** One key per document — the app's one rule for it (localPathForm). */
  private key(p: string): string {
    return localPathForm(p, this.platform)?.key ?? '';
  }

  /** Takes the document; false when it is already being opened. */
  begin(p: string): boolean {
    const k = this.key(p);
    if (this.keys.has(k)) return false;
    this.keys.add(k);
    return true;
  }

  end(p: string): void {
    this.keys.delete(this.key(p));
  }
}
