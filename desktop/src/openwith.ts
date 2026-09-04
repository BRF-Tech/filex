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
 * ⚠ Widening this list is a one-line change HERE, but it is not a one-line
 * change in the product: electron-builder's mac/linux `fileAssociations` and
 * build/installer.nsh's Windows ProgId list have to name the same extensions,
 * or the app appears in "Open with" on one OS and not the others.
 */
export const OFFICE_EXTENSIONS = [
  'docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt', 'odt', 'ods', 'odp', 'rtf',
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
  const win = platform === 'win32';
  const api = win ? nodePath.win32 : nodePath.posix;
  const norm = (p: string) => {
    const n = api.normalize(String(p ?? '')).replace(/[\\/]+$/, '');
    return win ? n.toLowerCase() : n;
  };
  const target = norm(localPath);
  if (!target) return null;

  let best: SyncTwin | null = null;
  let bestLen = -1;
  for (const p of pairs) {
    if (p.paused) continue;
    const local = norm(p.local);
    if (!local) continue;
    if (p.file) {
      if (local === target && local.length > bestLen) {
        best = { pairId: p.id, remote: p.remote };
        bestLen = local.length;
      }
      continue;
    }
    const prefix = local.endsWith(api.sep) ? local : local + api.sep;
    if (!target.startsWith(prefix)) continue;
    if (local.length <= bestLen) continue;
    // The relative part comes from the ORIGINAL path, case preserved: the
    // comparison is case-insensitive on Windows, the wire path is not.
    const relRaw = api.normalize(localPath).slice(prefix.length);
    const segs = relRaw.split(/[\\/]/).filter((s) => s && s !== '.' && s !== '..');
    if (!segs.length) continue;
    best = { pairId: p.id, remote: joinRemote(p.remote, segs) };
    bestLen = local.length;
  }
  return best;
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
  const dir = nodePath.dirname(target);
  const base = nodePath.basename(target);
  const dot = base.lastIndexOf('.');
  const stem = dot > 0 ? base.slice(0, dot) : base;
  const ext = dot > 0 ? base.slice(dot) : '';
  return nodePath.join(dir, stem + '.filex-recovered-' + stamp + ext);
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
 */
export async function writeBackAtomic(
  target: string,
  bytes: Buffer | Uint8Array,
  opts: { fallbackDir?: string; now?: Date } = {},
): Promise<void> {
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
      'the original file is no longer at ' + target + ' — the edit was not applied',
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
