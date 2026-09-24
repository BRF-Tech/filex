export interface CachedArchiveEntry {
  name: string;
  size: number;
  mtime?: string;
  is_dir?: boolean;
}

export interface ArchivePreviewCache {
  recall(path: string): CachedArchiveEntry[] | undefined;
  remember(path: string, entries: CachedArchiveEntry[]): void;
  forget(path: string): void;
  clear(): void;
}

export const ARCHIVE_PREVIEW_CACHE_TTL_MS = 2 * 60 * 1000;

export function createArchivePreviewCache(
  ttlMs = ARCHIVE_PREVIEW_CACHE_TTL_MS,
  now: () => number = Date.now,
): ArchivePreviewCache {
  type Cached = {
    entries: CachedArchiveEntry[];
    expiresAt: number;
    timer: ReturnType<typeof setTimeout>;
  };
  const values = new Map<string, Cached>();

  function forget(path: string): void {
    const cached = values.get(path);
    if (cached) clearTimeout(cached.timer);
    values.delete(path);
  }

  return {
    recall(path) {
      const cached = values.get(path);
      if (!cached) return undefined;
      if (cached.expiresAt <= now()) {
        forget(path);
        return undefined;
      }
      return cached.entries.map((entry) => ({ ...entry }));
    },
    remember(path, entries) {
      forget(path);
      const cached: Cached = {
        entries: entries.map((entry) => ({ ...entry })),
        expiresAt: now() + ttlMs,
        timer: setTimeout(() => {
          if (values.get(path) === cached) values.delete(path);
        }, ttlMs),
      };
      values.set(path, cached);
    },
    forget,
    clear() {
      for (const cached of values.values()) clearTimeout(cached.timer);
      values.clear();
    },
  };
}
