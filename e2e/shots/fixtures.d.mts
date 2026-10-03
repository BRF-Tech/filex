// Types for fixtures.mjs, for the specs that import it (185-default-apps
// packs an app's ui.zip with zipStored). Without this file the e2e suite's
// own typecheck (`tsc -p e2e/tsconfig.json`) stops on the import.

/** encodePNG renders `pixel(x, y) -> [r, g, b]` into a PNG buffer. */
export function encodePNG(
  width: number,
  height: number,
  pixel: (x: number, y: number) => [number, number, number] | number[],
): Buffer;

/** zipStored packs `[{ name, data }]` (forward-slash names) into a zip buffer. */
export function zipStored(entries: Array<{ name: string; data: Buffer | string }>): Buffer;

/** One sync run, as `/api/admin/storages/:id/sync-runs` lists it. */
export interface SyncRun {
  status: string;
  started_at: string;
  finished_at?: string | null;
  [key: string]: unknown;
}

/** Ask a storage to sync and wait for THAT run to finish. */
export function syncAndWait(
  api: (token: string, path: string, init?: { method?: string }) => Promise<Response>,
  token: string,
  storageId: number,
  opts?: { timeoutMs?: number },
): Promise<SyncRun>;

/** Put a real .docx or .xlsx (by `dest`'s extension) at `dest`. */
export function writeOfficeFile(dest: string): void;

/** Materialise the screenshot world under `root`; returns where to open. */
export function seedFixtures(root: string): { photos: string; readme: string };
