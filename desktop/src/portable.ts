// The Windows portable build: one .exe that runs from wherever it sits.
//
// An installed app belongs in %APPDATA% — nobody wants a program scattering
// folders across their desktop. A portable copy is the opposite case: it is
// run from a USB stick, a Downloads folder, a machine that is not the user's,
// and the promise it makes is that deleting it leaves NOTHING of theirs
// behind. So its data lives next to the executable, in one folder the user can
// see and delete.
//
// electron-builder's portable target unpacks the app into a temp directory and
// launches it from there, so `process.execPath` points somewhere that vanishes
// on exit — writing data beside THAT would lose it. The stub tells us where the
// .exe actually is through `PORTABLE_EXECUTABLE_DIR`, and that is the only
// signal used here.
//
// ⚠ Everything else in the app already routes through `app.getPath('userData')`
// — accounts, logs, the drag-out cache, the open-with session store. So this is
// one `app.setPath()` call, made before any of them has resolved a path. There
// is deliberately no second notion of "where our files go".

import fs from 'node:fs';
import path from 'node:path';

/** The folder that appears next to the .exe. Named so it is obvious what it is
 *  and safe to delete — it is the whole of what this copy leaves on a machine. */
export const PORTABLE_DATA_DIRNAME = 'filex-data';

export type PortableMode =
  /** Not a portable build (an install, a dev run, any non-Windows build). */
  | { portable: false }
  /** Portable, and the folder beside the .exe is ours to write. */
  | { portable: true; dataDir: string; syncDir: string }
  /**
   * Portable, but the .exe sits somewhere we cannot write — Program Files, a
   * read-only stick, a network share with no write access. The app falls back
   * to the ordinary userData directory rather than failing to start, and says
   * so in Settings: a portable copy that quietly stops being portable is worse
   * than one that admits it.
   */
  | { portable: true; dataDir: null; exeDir: string };

/**
 * Decides where a portable copy keeps its data, without touching Electron.
 *
 * Split out from the `app.setPath` call so the decision — including the
 * unwritable case, which is awkward to stage against a real disk — can be
 * tested directly.
 */
export function decidePortableDataDir(
  env: NodeJS.ProcessEnv,
  canWrite: (dir: string) => boolean,
  platform: NodeJS.Platform = process.platform,
): PortableMode {
  // The portable target is Windows-only, and a stray environment variable on
  // another OS must never quietly move somebody's account store.
  if (platform !== 'win32') return { portable: false };
  const exeDir = env.PORTABLE_EXECUTABLE_DIR;
  // ⚠ Absent means NOT portable — never "portable, guess a location". The
  // guess would be `process.execPath`, which under this target is the
  // extraction temp directory: it is deleted when the app exits, so the first
  // sign of trouble would be an account store that empties itself.
  if (!exeDir || !exeDir.trim()) return { portable: false };
  const dataDir = path.join(exeDir, PORTABLE_DATA_DIRNAME);
  if (!canWrite(dataDir)) return { portable: true, dataDir: null, exeDir };
  // ⚠ The sync engine keeps its OWN state — pairs, per-pair baselines, and the
  // local trash that holds real copies of files it deleted — and it keeps it in
  // `~/.filex/sync`, not under userData. On a portable copy that is somebody
  // else's home directory, and the trash makes it a privacy leak rather than
  // just untidiness. FILEX_SYNC_DIR moves it inside our folder; the CLI honours
  // it (backend/internal/filesync/store.go).
  return { portable: true, dataDir, syncDir: path.join(dataDir, 'sync') };
}

/**
 * Creates `dir` and proves it can actually be written to.
 *
 * ⚠ `fs.accessSync(W_OK)` is not enough on Windows: it reports the DACL, and a
 * read-only volume, a full disk or a share mounted without write access all
 * pass it and then fail on the first real write. The only honest test is a
 * write.
 */
export function directoryIsWritable(dir: string): boolean {
  const probe = path.join(dir, `.write-probe-${process.pid}`);
  try {
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(probe, '');
    return true;
  } catch {
    return false;
  } finally {
    try {
      fs.rmSync(probe, { force: true });
    } catch {
      /* the probe is a courtesy; its removal must never take the app down */
    }
  }
}

let cached: PortableMode | null = null;

/**
 * The decision for this process, made once.
 *
 * ⚠ Memoised because it probes the disk. Two callers ask (main.ts, to move
 * userData; sync.ts, to point the engine's store at the same folder) and both
 * must get the SAME answer — a second probe on a stick that has just been
 * write-protected would have half the app portable and half of it not.
 */
export function portableMode(): PortableMode {
  return (cached ??= decidePortableDataDir(process.env, directoryIsWritable));
}
