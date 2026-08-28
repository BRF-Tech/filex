// Dragging files OUT of the app — onto the desktop, into Explorer, into
// another program.
//
// The web half of filex can only ever drag ONE file out (Chromium carries a
// single `DownloadURL` per drag). Folders and multi-selections need a native OS
// drag, and a native OS drag needs real paths: the shell copies the bytes at
// DROP time, reading them off the disk. There is no lazy or virtual-file API to
// borrow — the Win32 `IDataObject` that lets WinRAR extract on drop is not
// reachable from Chromium — so before the gesture can start, the files have to
// BE here.
//
// There are therefore two ways out, and this module implements both:
//
//   • **Prepared copies** — the bytes are fetched into a cache first and the OS
//     is handed real, complete files. Correct for every drop target, including
//     applications that read the file the instant they receive it. Used for
//     anything already local and for small selections (prepared the moment they
//     are selected), so the common case costs nothing.
//   • **Placeholders** — an empty file with the right name is handed to the
//     drag, and it copies in microseconds. We then find out WHERE it landed
//     (see dropwatch.ts) and download the real bytes into that folder. No
//     waiting and no size ceiling: a 100 GB file drags out as fast as a 1 KB
//     one, and the transfer runs afterwards with its own progress.
//
// ⚠ The placeholder route trades one thing away: a drop onto an APPLICATION
// (a chat window, an editor) writes nothing to disk, so there is no landing
// place to find and the app is left with the empty file. That is exactly why
// small selections keep using prepared copies — those are the ones people drop
// into programs — and why a placeholder drop we cannot locate is REPORTED
// rather than quietly forgotten.
//
// ⚠⚠ It always drags a COPY, never the file inside a synced folder, even when
// that mirror is right there and identical. A native drag can be completed by
// the target as a MOVE, and moving the mirror out of a synced folder deletes it
// — locally and then, on the next sync run, on the server. A temp copy makes
// the worst case a wasted temp file. When the mirror exists it is still the
// fast path: the cache entry is copied from disk instead of downloaded.

import { net } from 'electron';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';

export interface DragItem {
  path: string; // wire path: `<depo>://rel`
  basename: string;
  type: 'file' | 'dir';
}

export interface DragProgress {
  done: number;
  total: number;
  name?: string;
  finished?: boolean;
  error?: string;
}

export interface PrepareContext {
  accountId: string;
  serverUrl: string;
  token: string;
  /** The sync engine's local mirror for a wire path, if it keeps one. */
  mirrorFor?: (remote: string) => string | null;
  onProgress?: (p: DragProgress) => void;
}

/** Ceilings, so a mis-click on a huge folder cannot quietly fill the disk. */
const MAX_BYTES = 4 * 1024 * 1024 * 1024; // 4 GiB per drag
const MAX_FILES = 5000;
/** Cache entries older than this are swept at boot. */
const CACHE_TTL_MS = 7 * 24 * 60 * 60 * 1000;

export class DragOutCache {
  constructor(private readonly root: string) {}

  /** Where the cache lives — the placeholder route puts its throwaway folders
   *  under here too, and the drop watcher must ignore that whole subtree. */
  get rootDir(): string {
    return this.root;
  }

  /** Where this remote path's copy lives. Hashed: a wire path is not a
   *  filename, and two depolar can hold the same name. */
  private entryDir(accountId: string, remote: string): string {
    const h = crypto.createHash('sha1').update(remote).digest('hex').slice(0, 16);
    return path.join(this.root, accountId, h);
  }

  /**
   * Deletes cache entries nobody has dragged in a week. Called at boot; a
   * failure here is never worth surfacing — it is a cache.
   */
  async sweep(): Promise<void> {
    const cutoff = Date.now() - CACHE_TTL_MS;
    let accounts: string[] = [];
    try {
      accounts = await fs.promises.readdir(this.root);
    } catch {
      return;
    }
    for (const acc of accounts) {
      const dir = path.join(this.root, acc);
      let entries: string[] = [];
      try {
        entries = await fs.promises.readdir(dir);
      } catch {
        continue;
      }
      for (const e of entries) {
        const p = path.join(dir, e);
        try {
          const st = await fs.promises.stat(p);
          if (st.mtimeMs < cutoff) await fs.promises.rm(p, { recursive: true, force: true });
        } catch {
          /* gone already, or in use — leave it */
        }
      }
    }
  }

  /**
   * Materialises `items` locally and returns the paths to hand the OS drag.
   *
   * `ready: false` with an `error` is an honest refusal (too big, too many
   * files, server said no) — the caller keeps the ordinary HTML5 drag and the
   * user is told why, rather than being left with a gesture that does nothing.
   */
  async prepare(
    items: DragItem[],
    ctx: PrepareContext,
    signal?: AbortSignal,
  ): Promise<{ ready: boolean; paths: string[]; error?: string }> {
    const plan: Array<{ remote: string; local: string; size: number }> = [];
    const dirs: string[] = [];
    const roots: string[] = [];

    for (const item of items) {
      const base = this.entryDir(ctx.accountId, item.path);
      const target = path.join(base, safeName(item.basename));
      roots.push(target);
      if (item.type === 'dir') {
        dirs.push(target);
        try {
          await this.planDir(ctx, item.path, target, plan, dirs, signal);
        } catch (e) {
          return { ready: false, paths: [], error: String((e as Error)?.message ?? e) };
        }
      } else {
        plan.push({ remote: item.path, local: target, size: -1 });
      }
      if (plan.length > MAX_FILES) {
        return { ready: false, paths: [], error: `too many files to drag (${MAX_FILES} max)` };
      }
    }

    for (const d of dirs) await fs.promises.mkdir(d, { recursive: true });

    let bytes = 0;
    let done = 0;
    for (const f of plan) {
      if (signal?.aborted) return { ready: false, paths: [], error: 'cancelled' };
      ctx.onProgress?.({ done, total: plan.length, name: path.basename(f.local) });
      try {
        bytes += await this.materialise(ctx, f.remote, f.local, f.size);
      } catch (e) {
        ctx.onProgress?.({ done, total: plan.length, finished: true, error: String((e as Error)?.message ?? e) });
        return { ready: false, paths: [], error: String((e as Error)?.message ?? e) };
      }
      if (bytes > MAX_BYTES) {
        const msg = 'selection is too large to drag out (4 GB max)';
        ctx.onProgress?.({ done, total: plan.length, finished: true, error: msg });
        return { ready: false, paths: [], error: msg };
      }
      done++;
    }
    ctx.onProgress?.({ done, total: plan.length, finished: true });
    return { ready: true, paths: roots };
  }

  /** Are all of these already sitting in the cache? Then the drag is free. */
  async ready(items: DragItem[], accountId: string): Promise<string[] | null> {
    const out: string[] = [];
    for (const item of items) {
      const p = path.join(this.entryDir(accountId, item.path), safeName(item.basename));
      try {
        await fs.promises.stat(p);
      } catch {
        return null;
      }
      out.push(p);
    }
    return out;
  }

  /**
   * Downloads one remote file to an exact destination path, completing through
   * a `.filexpart` rename so nothing ever wears the final name half-written.
   * Used by the placeholder route, which writes into the user's own folder.
   */
  async downloadFileTo(ctx: PrepareContext, remote: string, dest: string): Promise<number> {
    await fs.promises.mkdir(path.dirname(dest), { recursive: true });
    const mirror = ctx.mirrorFor?.(remote) ?? null;
    if (mirror) {
      const mst = await fs.promises.stat(mirror).catch(() => null);
      if (mst?.isFile()) {
        await fs.promises.copyFile(mirror, dest);
        return mst.size;
      }
    }
    const url = new URL('/api/files/manager', ctx.serverUrl);
    url.searchParams.set('action', 'download');
    url.searchParams.set('path', remote);
    const res = await net.fetch(url.toString(), { headers: { Authorization: `Bearer ${ctx.token}` } });
    if (!res.ok || !res.body) throw new Error(`downloading ${remote} failed: server said ${res.status}`);
    const tmp = `${dest}.filexpart`;
    await pipeline(Readable.fromWeb(res.body as Parameters<typeof Readable.fromWeb>[0]), fs.createWriteStream(tmp));
    await fs.promises.rename(tmp, dest);
    return (await fs.promises.stat(dest)).size;
  }

  /** Same, for a whole remote folder — the tree is rebuilt under `dest`. */
  async downloadTreeTo(
    ctx: PrepareContext,
    remoteDir: string,
    dest: string,
    signal?: AbortSignal,
  ): Promise<void> {
    if (signal?.aborted) throw new Error('cancelled');
    await fs.promises.mkdir(dest, { recursive: true });
    const url = new URL('/api/files/manager', ctx.serverUrl);
    url.searchParams.set('action', 'index');
    url.searchParams.set('path', remoteDir);
    const res = await net.fetch(url.toString(), { headers: { Authorization: `Bearer ${ctx.token}` } });
    if (!res.ok) throw new Error(`listing ${remoteDir} failed: server said ${res.status}`);
    const body = (await res.json()) as { files?: Array<{ basename: string; type: string }> };
    const base = remoteDir.endsWith('://') || remoteDir.endsWith('/') ? remoteDir : `${remoteDir}/`;
    for (const f of body.files ?? []) {
      if (!f.basename || f.basename === '.trash') continue;
      const childRemote = `${base}${f.basename}`;
      const childDest = path.join(dest, safeName(f.basename));
      if (f.type === 'dir') await this.downloadTreeTo(ctx, childRemote, childDest, signal);
      else await this.downloadFileTo(ctx, childRemote, childDest);
    }
  }

  /** Walks a remote folder, adding its files to the plan. */
  private async planDir(
    ctx: PrepareContext,
    remoteDir: string,
    localDir: string,
    plan: Array<{ remote: string; local: string; size: number }>,
    dirs: string[],
    signal?: AbortSignal,
  ): Promise<void> {
    if (signal?.aborted) throw new Error('cancelled');
    const url = new URL('/api/files/manager', ctx.serverUrl);
    url.searchParams.set('action', 'index');
    url.searchParams.set('path', remoteDir);
    const res = await net.fetch(url.toString(), { headers: { Authorization: `Bearer ${ctx.token}` } });
    if (!res.ok) throw new Error(`listing ${remoteDir} failed: server said ${res.status}`);
    const body = (await res.json()) as { files?: Array<{ basename: string; type: string; size?: number }> };
    const base = remoteDir.endsWith('://') || remoteDir.endsWith('/') ? remoteDir : `${remoteDir}/`;
    for (const f of body.files ?? []) {
      if (!f.basename || f.basename === '.trash') continue;
      const childRemote = `${base}${f.basename}`;
      const childLocal = path.join(localDir, safeName(f.basename));
      if (f.type === 'dir') {
        dirs.push(childLocal);
        await this.planDir(ctx, childRemote, childLocal, plan, dirs, signal);
      } else {
        plan.push({ remote: childRemote, local: childLocal, size: typeof f.size === 'number' ? f.size : -1 });
      }
      if (plan.length > MAX_FILES) throw new Error(`too many files to drag (${MAX_FILES} max)`);
    }
  }

  /**
   * Puts one file in the cache and returns how many bytes that cost.
   *
   * An entry already there with the expected size is reused — this is what
   * makes the second drag of anything instant. Otherwise the local mirror is
   * copied when the sync engine has one (no network), and only failing that
   * does the file come down from the server.
   */
  private async materialise(ctx: PrepareContext, remote: string, local: string, size: number): Promise<number> {
    const have = await fs.promises.stat(local).catch(() => null);
    if (have?.isFile() && (size < 0 || have.size === size)) {
      // Touch it so the sweeper counts it as recently used.
      const now = new Date();
      await fs.promises.utimes(local, now, now).catch(() => undefined);
      return have.size;
    }
    await fs.promises.mkdir(path.dirname(local), { recursive: true });

    const mirror = ctx.mirrorFor?.(remote) ?? null;
    if (mirror) {
      const mst = await fs.promises.stat(mirror).catch(() => null);
      if (mst?.isFile() && (size < 0 || mst.size === size)) {
        await fs.promises.copyFile(mirror, local);
        await fs.promises.utimes(local, mst.atime, mst.mtime).catch(() => undefined);
        return mst.size;
      }
    }

    const url = new URL('/api/files/manager', ctx.serverUrl);
    url.searchParams.set('action', 'download');
    url.searchParams.set('path', remote);
    const res = await net.fetch(url.toString(), { headers: { Authorization: `Bearer ${ctx.token}` } });
    if (!res.ok || !res.body) throw new Error(`downloading ${remote} failed: server said ${res.status}`);
    // ⚠ Written to `.part` and renamed: a half-written file left at the real
    // path would be handed to the OS by the next drag and copied as if whole.
    const tmp = `${local}.part`;
    await pipeline(Readable.fromWeb(res.body as Parameters<typeof Readable.fromWeb>[0]), fs.createWriteStream(tmp));
    await fs.promises.rename(tmp, local);
    const st = await fs.promises.stat(local);
    return st.size;
  }
}

/**
 * A drag that handed the OS placeholders and is now waiting to learn where they
 * landed. Held by the main process for the life of one drag.
 */
export interface PlaceholderSession {
  /** Absolute paths given to the OS drag (all inside `dir`). */
  paths: string[];
  /** The throwaway folder holding them. */
  dir: string;
  /** What each placeholder stands for. */
  items: DragItem[];
}

/**
 * Creates one empty stand-in per item and returns the paths to drag.
 *
 * Empty is the point: the shell copies it instantly, so the drag starts with no
 * transfer at all. A folder gets an empty folder, which copies just as fast.
 */
export async function createPlaceholders(
  root: string,
  accountId: string,
  items: DragItem[],
): Promise<PlaceholderSession> {
  const dir = path.join(root, 'pending', accountId, crypto.randomUUID());
  await fs.promises.mkdir(dir, { recursive: true });
  const paths: string[] = [];
  for (const item of items) {
    const target = path.join(dir, safeName(item.basename));
    if (item.type === 'dir') await fs.promises.mkdir(target, { recursive: true });
    else await fs.promises.writeFile(target, '');
    paths.push(target);
  }
  return { paths, dir, items };
}

/**
 * Replaces the placeholders the shell copied into `dropDir` with the real
 * thing: each stand-in is removed and the actual bytes are written in its
 * place.
 *
 * ⚠ Downloads land on `<name>.filexpart` and are renamed only when complete.
 * Writing straight to the final name would leave a half file that looks
 * finished to anyone who opens the folder while it is still arriving.
 */
export async function fulfilDrop(
  cache: DragOutCache,
  dropDir: string,
  items: DragItem[],
  ctx: PrepareContext,
  signal?: AbortSignal,
): Promise<{ ok: boolean; written: string[]; error?: string }> {
  const written: string[] = [];
  let done = 0;
  for (const item of items) {
    if (signal?.aborted) return { ok: false, written, error: 'cancelled' };
    const name = safeName(item.basename);
    const dest = path.join(dropDir, name);
    ctx.onProgress?.({ done, total: items.length, name });
    try {
      // The empty stand-in goes first: the real content takes its place, and a
      // failure must not leave a zero-byte file wearing the right name.
      await fs.promises.rm(dest, { recursive: true, force: true });
      if (item.type === 'dir') await cache.downloadTreeTo(ctx, item.path, dest, signal);
      else await cache.downloadFileTo(ctx, item.path, dest);
      written.push(dest);
    } catch (e) {
      const msg = String((e as Error)?.message ?? e);
      ctx.onProgress?.({ done, total: items.length, name, finished: true, error: msg });
      return { ok: false, written, error: msg };
    }
    done++;
  }
  ctx.onProgress?.({ done, total: items.length, finished: true });
  return { ok: true, written };
}

/** One path segment, with the characters Windows actually refuses taken
 *  out. Spaces and dots stay: the file that lands on the desktop should
 *  carry the name it has on the server. */
function safeName(name: string): string {
  const cleaned = String(name ?? '')
    .replace(/[\\/:*?"<>|]/g, '_')
    .replace(/[\u0000-\u001f]/g, '')
    .replace(/^\.+$/, '_')
    .trim();
  return cleaned || 'file';
}
