// The snap's plugs that the person connects, and what the app says until they
// have.
//
// A strict snap reaches what its connected interfaces allow, and two of this
// snap's plugs never connect by themselves. The Snap Store declined to
// auto-connect them (forum.snapcraft.io/t/53584, 2026-10-17): each one grants
// more than the app's own files, so connecting it is the person's choice.
//
//   password-manager-service  the keyring (src/keychain.ts → keychainAdvice)
//   removable-media           everything under /media, /run/media and /mnt
//
// So the app asks snapd (`snapctl is-connected <plug>`) instead of guessing
// from a failure, and when a plug is not connected it says what connecting it
// allows BEFORE it shows the command that does it.
//
// ⚠ An answer that cannot be read is not "disconnected". Only exit codes 0
// and 1 are snapctl's yes and no; anything else (another code, no snapctl, a
// timeout) leaves the app doing what it did before it asked: the keyring
// advice stays the snap one, and a folder is paired as chosen.
//
// ⚠ No `electron` import, so node:test can drive it; the child process is a
// parameter, so no test ever runs a real snapctl.

import { execFile } from 'node:child_process';
import path from 'node:path';

import { snapConnectCommand } from './keychain.ts';

export type PlugState = 'connected' | 'disconnected' | 'unknown';

/**
 * Runs `snapctl <args>` and resolves its exit code, or null when it did not
 * run to an exit code of its own (no snapctl, killed by the timeout). Never
 * rejects.
 */
export type SnapctlRun = (args: readonly string[]) => Promise<number | null>;

/** `snapctl is-connected`'s exit code, read: 0 connected, 1 not connected. */
export function plugStateOf(code: number | null | undefined): PlugState {
  if (code === 0) return 'connected';
  if (code === 1) return 'disconnected';
  return 'unknown';
}

/**
 * The exit code an `execFile` callback's error stands for: none is 0, a
 * process that exited non-zero carries its code as a number. A string code
 * (`ENOENT`: no snapctl) or none at all (killed by the timeout) is not an
 * exit code.
 */
export function exitCodeOf(err: unknown): number | null {
  if (!err) return 0;
  const code = (err as { code?: unknown }).code;
  return typeof code === 'number' ? code : null;
}

/** snapd answers in milliseconds; a snapctl that hangs is no answer. */
const SNAPCTL_TIMEOUT_MS = 5000;

export const runSnapctl: SnapctlRun = (args) =>
  new Promise((resolve) => {
    try {
      execFile('snapctl', [...args], { timeout: SNAPCTL_TIMEOUT_MS, windowsHide: true }, (err) => resolve(exitCodeOf(err)));
    } catch {
      resolve(null);
    }
  });

/** Whether this snap's `plug` is connected, as snapd says it. */
export async function snapPlugState(plug: string, run: SnapctlRun = runSnapctl): Promise<PlugState> {
  try {
    return plugStateOf(await run(['is-connected', plug]));
  } catch {
    return 'unknown';
  }
}

/** What the `removable-media` interface opens to a snap. */
export const REMOVABLE_MEDIA_ROOTS = ['/media', '/run/media', '/mnt'] as const;

/**
 * True when `dir` is one of the removable-media roots or lies under one.
 * Normalized first: `/media/../home/me` is in the home folder, and
 * `/run//media/usb/` is under `/run/media`. `/mediafiles` is not `/media`.
 */
export function onRemovableMedia(dir: string): boolean {
  if (typeof dir !== 'string' || !dir.startsWith('/')) return false;
  const p = path.posix.normalize(dir).replace(/\/+$/, '') || '/';
  return REMOVABLE_MEDIA_ROOTS.some((root) => p === root || p.startsWith(root + '/'));
}

/** A plug to connect before something can go ahead, and the command for it. */
export interface PlugAdvice {
  plug: string;
  /** `sudo snap connect <this snap>:<plug>`, named by snapd (src/keychain.ts → snapConnectCommand). */
  command: string | null;
}

/**
 * What follows from the `removable-media` plug's state for a sync folder
 * there: advice only when snapd said it is NOT connected. Connected goes
 * ahead, and so does an answer that could not be read - as it did before
 * the app asked.
 */
export function removableMediaAdvice(plug: PlugState, env: Record<string, string | undefined>): PlugAdvice | null {
  if (plug !== 'disconnected') return null;
  return { plug: 'removable-media', command: snapConnectCommand(env, 'removable-media') };
}

/**
 * Whether a sync folder chosen at `dir` can be used as it is, or what the
 * person has to connect first. snapctl is asked only in the snap, and only
 * for a folder under the removable-media roots.
 */
export async function syncFolderAdvice(
  dir: string,
  channel: string | null,
  env: Record<string, string | undefined>,
  run: SnapctlRun = runSnapctl,
): Promise<PlugAdvice | null> {
  if (channel !== 'snap' || !onRemovableMedia(dir)) return null;
  return removableMediaAdvice(await snapPlugState('removable-media', run), env);
}
