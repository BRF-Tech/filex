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
 *
 * ⚠ filex 0.56 (task #189): while the interface holds a file of an encrypted
 * folder in the clear, its `state.get` / `state.set` go to ANOTHER store,
 * sealed in this tab with a key derived from the folder's (lib/appStateE2e).
 * This one is the ordinary session's, and the two never mix. The rules of an
 * entry (stateEntry) and the app's share (appStateMax) are this file's, for
 * both.
 */
import { LIMITS } from '@brftech/filex-app-ui/protocol';
import { currentPrefs, savePref } from './prefs';
import { serverLimit } from './serverRules';

/**
 * One app's whole store, as JSON bytes: the SERVER's number
 * (`capabilities.limits.app_state_max_bytes`), which PUT /api/me/prefs
 * enforces (handlers/userprefs.go, #211 - it used to live here alone). Said
 * here only so the app hears "too large" at once instead of the whole
 * account document being refused; a server that publishes no number holds
 * the document to its own 64 KiB and nothing is checked here.
 */
export function appStateMax(): number | undefined {
  return serverLimit('app_state_max_bytes');
}

const utf8 = new TextEncoder();

/** The size of a JSON text as the server measures it (UTF-8 bytes). */
export function jsonBytes(text: string): number {
  return utf8.encode(text).length;
}

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

/**
 * One `state.set` entry as the rules take it: a key of 1-128 characters
 * (never an object's own), a value of at most LIMITS.maxStateBytes as JSON.
 * Answers the value's JSON, undefined for a removal; throws AppStateTooLarge.
 */
export function stateEntry(key: string, value: unknown): string | undefined {
  if (!key || key.length > 128 || key === '__proto__' || key === 'constructor' || key === 'prototype') {
    throw new AppStateTooLarge('a state key is 1-128 characters');
  }
  const encoded = value === undefined ? undefined : JSON.stringify(value);
  if (encoded !== undefined && encoded.length > LIMITS.maxStateBytes) {
    throw new AppStateTooLarge(`a state value is at most ${LIMITS.maxStateBytes} bytes as JSON`);
  }
  return encoded;
}

/** Keep `value` (JSON) under `key` for this app; undefined removes it. */
export function appStateSet(app: string, key: string, value: unknown): void {
  const encoded = stateEntry(key, value);
  const all = read('appState') as Store;
  const mine: Record<string, unknown> = { ...(all[app] ?? {}) };
  if (encoded === undefined) delete mine[key];
  else mine[key] = JSON.parse(encoded);
  const max = appStateMax();
  if (max !== undefined && jsonBytes(JSON.stringify(mine)) > max) {
    throw new AppStateTooLarge(`an app keeps at most ${max} bytes`);
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
