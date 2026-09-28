/**
 * appState — what an app's own interface keeps for this person, and which
 * app versions the person has been told about.
 *
 * ⚠ An interface runs on an OPAQUE origin (sandbox without allow-same-origin):
 * localStorage, IndexedDB and cookies throw there, by design — they would be
 * shared by every app, or reach filex's own. So `state.get` / `state.set` over
 * the bridge land here, keyed by the app's name, in the ACCOUNT's preference
 * document (lib/prefs, `appState`), the same place a palette lives: it follows
 * the person to the next browser. Where the host keeps no account document (an
 * embed that wires none), it lasts as long as the page — never in another
 * app's reach either way.
 */
import { LIMITS } from '@brftech/filex-app-ui/protocol';
import { currentPrefs, savePref } from './prefs';

/** One app's whole store, as JSON. The account document is 64 KiB in all. */
export const MAX_APP_STATE_BYTES = 16 << 10;

type Store = Record<string, Record<string, unknown>>;

function read(key: 'appState' | 'appsSeen'): Record<string, unknown> {
  const raw = currentPrefs()[key];
  if (!raw) return {};
  try {
    const v = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

/** The value an app kept under `key`; undefined when none. */
export function appStateGet(app: string, key: string): unknown {
  const mine = (read('appState') as Store)[app];
  return mine && Object.prototype.hasOwnProperty.call(mine, key) ? mine[key] : undefined;
}

export class AppStateTooLarge extends Error {}

/** Keep `value` (JSON) under `key` for this app; undefined removes it. */
export function appStateSet(app: string, key: string, value: unknown): void {
  if (!key || key.length > 128 || key === '__proto__' || key === 'constructor' || key === 'prototype') {
    throw new AppStateTooLarge('a state key is 1-128 characters');
  }
  const encoded = value === undefined ? undefined : JSON.stringify(value);
  if (encoded !== undefined && encoded.length > LIMITS.maxStateBytes) {
    throw new AppStateTooLarge(`a state value is at most ${LIMITS.maxStateBytes} bytes as JSON`);
  }
  const all = read('appState') as Store;
  const mine: Record<string, unknown> = { ...(all[app] ?? {}) };
  if (encoded === undefined) delete mine[key];
  else mine[key] = JSON.parse(encoded);
  if (JSON.stringify(mine).length > MAX_APP_STATE_BYTES) {
    throw new AppStateTooLarge(`an app keeps at most ${MAX_APP_STATE_BYTES} bytes`);
  }
  const next: Store = { ...all, [app]: mine };
  if (!Object.keys(mine).length) delete next[app];
  savePref('appState', JSON.stringify(next));
}

/** The version of `app` this person was last told about ('' = never). */
export function appSeenVersion(app: string): string {
  const v = read('appsSeen')[app];
  return typeof v === 'string' ? v : '';
}

/** Remember that this person has seen `app` at `version`. */
export function markAppSeen(app: string, version: string): void {
  const all = read('appsSeen');
  if (all[app] === version) return;
  savePref('appsSeen', JSON.stringify({ ...all, [app]: version }));
}
