/**
 * uploadResume — where a browser upload writes down that it is not finished.
 *
 * A staged upload survives a dropped connection because the SERVER holds the
 * bytes and can be asked for its offset. What the browser has to survive is
 * itself: a reloaded tab, a closed laptop, a crashed renderer. None of those
 * reach the server, and without a note on disk the next visit has no idea an
 * upload id ever existed — so the same 4 GB starts again at zero, which is the
 * exact complaint this work exists to answer.
 *
 * The note is deliberately small and deliberately a hint:
 *
 *   - it stores the upload id, not the bytes. localStorage cannot hold a File,
 *     and the browser will not hand a File back without a fresh user gesture.
 *     Recovery is therefore "pick the same file again and it continues", not
 *     "it silently continues" — the honest shape, and the one the platform
 *     allows.
 *   - `offset` here is never used as a resume point. It is shown to the user
 *     ("resuming at 62%") and used to decide a session is worth asking about;
 *     the byte to continue from always comes from GET /api/files/upload/{id}.
 *
 * The fingerprint pins a record to one file: name, size and lastModified. A
 * different file with the same name must not inherit the session, because
 * appending its tail to the previous head is the one way a resumable upload
 * corrupts data.
 *
 * This lives in `packages/core` because it is not browser-chrome: the web
 * explorer, the desktop app's explorer and the work.example.com / fishapp embeds all
 * mount the same component, and an upload that resumes in one of them and not
 * the others would be two products again.
 */

const STORE_KEY = 'filex:uploads:v1';

/** How long an unfinished record is worth keeping. The server sweeps its own
 *  staging after FILEX_UPLOAD_STAGING_TTL (24 h by default); a note that
 *  outlives the bytes it describes only produces a confusing "resuming…" that
 *  immediately restarts. */
export const RESUME_TTL_MS = 24 * 60 * 60 * 1000;

export interface ResumeRecord {
  /** Server-side staged upload id. */
  uploadId: string;
  /** Destination directory, qualified (`adapter://sub/dir`). */
  path: string;
  name: string;
  size: number;
  /** File.lastModified — part of the identity, not metadata. */
  lastModified: number;
  chunkSize: number;
  /** Last offset the server acknowledged. Display + triage only. */
  offset: number;
  updatedAt: number;
}

/** Identity of one (destination, file) pair. */
export function uploadFingerprint(
  path: string,
  file: { name: string; size: number; lastModified?: number },
): string {
  return [path, file.name, file.size, file.lastModified ?? 0].join('\u0000');
}

type Store = Record<string, ResumeRecord>;

/** A Storage-shaped thing. Injected so tests (and any surface without
 *  localStorage — SSR, a locked-down embed) do not have to fake globals. */
export interface ResumeStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** The default backing store, or null where the platform has none. Reading
 *  localStorage throws in a partitioned/blocked context, so the probe is a real
 *  try, not a typeof check. */
export function defaultResumeStorage(): ResumeStorage | null {
  try {
    if (typeof localStorage === 'undefined') return null;
    localStorage.getItem(STORE_KEY);
    return localStorage;
  } catch {
    return null;
  }
}

function read(store: ResumeStorage | null): Store {
  if (!store) return {};
  try {
    const raw = store.getItem(STORE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Store;
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    // Corrupt payload: an upload that starts over is a far smaller problem
    // than one that throws on every page load.
    return {};
  }
}

function write(store: ResumeStorage | null, data: Store): void {
  if (!store) return;
  try {
    store.setItem(STORE_KEY, JSON.stringify(data));
  } catch {
    /* quota / private mode — resume degrades, uploads still work */
  }
}

/** Drop records older than RESUME_TTL_MS. Returns what survived. */
export function pruneResume(store: ResumeStorage | null, now = Date.now()): Store {
  const data = read(store);
  let changed = false;
  for (const [key, rec] of Object.entries(data)) {
    if (!rec?.uploadId || now - (rec.updatedAt ?? 0) > RESUME_TTL_MS) {
      delete data[key];
      changed = true;
    }
  }
  if (changed) write(store, data);
  return data;
}

/** The record for this exact (destination, file), or null. */
export function loadResume(
  store: ResumeStorage | null,
  key: string,
  now = Date.now(),
): ResumeRecord | null {
  const rec = pruneResume(store, now)[key];
  return rec ?? null;
}

export function saveResume(
  store: ResumeStorage | null,
  key: string,
  rec: Omit<ResumeRecord, 'updatedAt'>,
  now = Date.now(),
): void {
  const data = read(store);
  data[key] = { ...rec, updatedAt: now };
  write(store, data);
}

export function clearResume(store: ResumeStorage | null, key: string): void {
  const data = read(store);
  if (key in data) {
    delete data[key];
    write(store, data);
  }
}

/** Every unfinished upload this browser knows about, newest first. Surfaces
 *  use it to say "you have an unfinished upload" instead of forgetting. */
export function listResume(store: ResumeStorage | null, now = Date.now()): ResumeRecord[] {
  return Object.values(pruneResume(store, now)).sort((a, b) => b.updatedAt - a.updatedAt);
}
