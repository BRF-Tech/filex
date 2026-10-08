// What the sync supervisor knows about one account's engine, and the rules
// that fold the engine's EVENTS into it.
//
// The watcher's output is the ONLY channel between the engine and this app:
// every badge, the explorer's bottom strip, the rail dot and the line under
// each folder in Settings comes from it — folded HERE, in one place, so the
// rest of the app gets typed data.
//
// ⚠ No `electron` import (same reason as notifications.ts): this module holds
// the rules, and a module that imports electron cannot run under `node --test`.
// ⚠ No relative imports either: `node --test` strips types but does not map
// `./x.js` onto `./x.ts`, so a test importing this file would fail to load.
//
// ⚠⚠ Since 0.54 (#213) the app starts the engine with `--json` and reads
// EVENTS, one JSON object per line on stdout (backend/cmd/filex/syncevents.go,
// docs/DESKTOP.md "The engine's event stream"):
//
//   {"event":"pass","pair":"pair-1","code":"pass.done","params":{…},"message":"…"}
//
// `message` is the sentence a person reads, said BY THE ENGINE in the
// ACCOUNT's language (#191): the app starts it without `--lang`, the engine
// reads the account's language from the server when it starts (a language
// pack's included), and a change of the account's language restarts that
// account's watchers (main.ts applyAccountLocale). This module
// keeps it as it is and never parses it; what it acts on is `event`, `code`
// and the typed `params`. Until 0.54 it read the engine's English lines with
// regular expressions: a sentence reworded in Go silently broke the status
// under a folder, and the engine's errors reached a Turkish window in English.
//
//   hello     the stream opens: params.protocol, version, lang
//   progress  a phase of a pass (params.phase, done/total, here/listed, bytes)
//   hold      items held for a decision (params.count)
//   pass      a pass finished (params.failed > 0: it had errors, which follow)
//   error     a failure: of one action, of the pass, of the pair's lock
//   note      worth reading in a terminal, not state
//   lock      lock.busy (another process syncs the pair) / lock.acquired
//   local     local.watched / local.too_large / local.unavailable
//   live      how SERVER changes reach the engine (params.state)
//   window    outside the sync window (params.opens_at: when it opens)
//   fatal     the engine stopped: params.exit; code signed_out = HTTP 401
//
// A line on stdout that is not an event is ignored. Anything on stderr is not
// the engine's stream — a Go panic, an argument the engine refused before it
// could speak (an engine older than this app: "unknown flag: --json") — and
// is kept verbatim as the account's error. And the exit status
// SIGNED_OUT_EXIT (3) when the server refused the token.

/** The version of the stream's shape this app reads (the hello event). */
export const ENGINE_PROTOCOL = 1;

/**
 * How SERVER-side changes reach the engine (the `live` event's state):
 *
 *   - `connected` — subscribed to the server's change stream; a save in the
 *     browser is on disk within about a second.
 *   - `polling`   — the server cannot announce changes (older than 0.43, or its
 *     realtime is off): server-side edits arrive at the next check.
 *   - `offline`   — the server is unreachable right now; the engine retries.
 */
export type LiveState = 'connected' | 'polling' | 'offline';

/** One event of `filex sync run --json`. */
export interface EngineEvent {
  event: string;
  pair?: string;
  code: string;
  params: Record<string, unknown>;
  /** The engine's sentence, in the language the app named. Shown, never
   *  parsed. */
  message: string;
}

/** One stdout line as an event, or null when it is not one. */
export function parseEvent(line: string): EngineEvent | null {
  const t = line.trim();
  if (!t.startsWith('{')) return null;
  let v: unknown;
  try {
    v = JSON.parse(t);
  } catch {
    return null;
  }
  if (!v || typeof v !== 'object') return null;
  const o = v as Record<string, unknown>;
  if (typeof o.event !== 'string' || typeof o.code !== 'string') return null;
  return {
    event: o.event,
    pair: typeof o.pair === 'string' && o.pair !== '' ? o.pair : undefined,
    code: o.code,
    params: o.params && typeof o.params === 'object' ? (o.params as Record<string, unknown>) : {},
    message: typeof o.message === 'string' ? o.message : '',
  };
}

function num(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/**
 * Why changes made on THIS computer in one pair are only found by the full
 * check (the engine could not watch its folder):
 *   - `too-large`   — macOS watches every file separately, and the folder is
 *     past the engine's budget;
 *   - `unavailable` — the operating system refused (a watch limit, a
 *     permission, no watcher at all).
 * message is the engine's sentence for it.
 */
export interface LocalNote {
  code: 'too-large' | 'unavailable';
  detail: string | null;
  message: string;
}

/**
 * Another process on this computer holds the pair's lock — the other copy of
 * this app (an installed one and the Microsoft Store one both sign in with the
 * same accounts), or `filex sync run` in a terminal — so this engine leaves the
 * pair alone. The engine keeps trying and takes the pair over by itself once
 * that process stops (backend/cmd/filex/synclock.go); nothing is restarted
 * here. detail names the other process (the lock's own words); message is
 * the engine's sentence.
 */
export interface PairBusy {
  detail: string;
  message: string;
}

/** One pair's health, as its card shows it. */
export interface PairHealth {
  /** Its last failure (the engine's sentence) — until a later pass of the
   *  SAME pair completes clean. */
  error: string | null;
  /** The last thing the engine said it did for this pair. */
  line: string | null;
  local: LocalNote | null;
  /** Set while another process syncs this pair (see PairBusy). */
  busy: PairBusy | null;
  /** A pass of this pair has finished since the engine started (a pass
   *  event): until then it is not known to be in step, and its line must not
   *  say "watching for changes" (sync-policy.ts folderView). */
  passed?: boolean;
}

export type SyncPhase = 'inventory' | 'plan' | 'transfer' | 'settling';

/** One pair's live phase, from the engine's progress events. */
export interface SyncActivity {
  pairId: string;
  phase: SyncPhase;
  /** transfer: actions done / planned; settling: changes recorded / to
   *  record; plan: 0 / the changes to make; inventory: 0/0. */
  done: number;
  total: number;
  /** inventory only: the items found on this computer, and the server items
   *  listed so far — a large tree lists for minutes. */
  here?: number;
  listed?: number;
  /** transfer only, once there are bytes to move. */
  bytesDone?: number;
  bytesTotal?: number;
  /** transfer only, once the engine has an estimate. */
  etaSeconds?: number;
  /** The engine's sentence for this step, with every figure above in it. */
  message: string;
}

/** Outside its sync window (`--window`) the engine runs nothing and says so
 *  once. */
export interface WindowWait {
  /** The window, e.g. '22:00-07:00' (the engine's canonical form). */
  window: string;
  /** When it opens next (epoch ms), from the engine — the app does no clock
   *  arithmetic of its own on a window (B17). null: the engine did not say. */
  opensAt: number | null;
  /** The engine's sentence. */
  message: string;
}

/** What the supervisor has observed about one account's sync process. */
export interface SyncStatus {
  accountId: string;
  running: boolean;
  lastLine: string;
  lastRunAt: string | null;
  /** An error that is not about any one pair — the engine could not start,
   *  the token was refused, the process died. Shown under every folder of the
   *  account, cleared as soon as any pass completes again. */
  lastError: string | null;
  /** The pair the engine is working on RIGHT NOW, or null between runs.
   *  One value, not a map: the engine walks its pairs sequentially. */
  active: SyncActivity | null;
  /** How changes reach the engine (see LiveState). null until it has said. */
  live: LiveState | null;
  /** The engine's sentence for the live state. */
  liveMessage: string | null;
  /** Per pair, keyed by pair id. */
  pairs: Record<string, PairHealth>;
  /** The server no longer accepts this account's token: the engine exited with
   *  SIGNED_OUT_EXIT, or said `signed_out`. The supervisor stops the watcher
   *  and does not restart it. */
  signedOut?: boolean;
  /** Outside its sync window the engine runs nothing (WindowWait). Cleared
   *  when a pass starts. */
  waitingWindow?: WindowWait | null;
  /** Hold events not yet handed to the supervisor (takeHolds): each is a cue
   *  to re-read the pair list, which carries the numbers the app shows. */
  holds?: Array<{ pairId: string; count: number }>;
  /** The engine stopped on its own: its exit code (or signal), so the page
   *  can say so in its own language; lastError has the engine's reason. */
  exited?: string | null;
  /** When the supervisor starts it again (epoch ms), while it waits to. */
  restartAt?: number | null;
  /** The stream's version (the hello event); null until the engine said. */
  protocol?: number | null;
  /** How many events the engine has written since it started. An engine
   *  that exits having written none never spoke the stream at all: older
   *  than this app (it refused `--json`), or stopped before it could. */
  events?: number;
  /** Set by markExited for exactly that engine (see events). */
  noStream?: boolean;
}

/** `filex sync run` exits with this status when the server answers 401, and
 *  does not retry. */
export const SIGNED_OUT_EXIT = 3;

export function newStatus(accountId: string): SyncStatus {
  return {
    accountId,
    running: true,
    lastLine: 'starting…',
    lastRunAt: null,
    lastError: null,
    active: null,
    live: null,
    liveMessage: null,
    pairs: {},
    waitingWindow: null,
    holds: [],
    protocol: null,
    events: 0,
  };
}

/** A pair's card: its own error (or the account's), line and local note. */
export function pairView(st: SyncStatus, pairId: string): PairHealth {
  const h = st.pairs[pairId];
  return {
    error: h?.error ?? st.lastError,
    line: h?.line ?? null,
    local: h?.local ?? null,
    busy: h?.busy ?? null,
    passed: h?.passed === true,
  };
}

/** Whether anything about this account is failing right now (the rail dot). */
export function anyError(st: SyncStatus): boolean {
  return st.lastError !== null || Object.values(st.pairs).some((h) => h.error !== null);
}

/**
 * Whether a watcher's refusal (HTTP 401) is still news about its account: only
 * while its status is the account's CURRENT one. Signing in again starts a new
 * watcher with the new token and the old one's last words — often exactly
 * that 401 — can arrive after; they must not sign out the account that has
 * just been fixed.
 */
export function refusalApplies(current: SyncStatus | undefined, mine: SyncStatus): boolean {
  return current === mine && mine.signedOut === true;
}

/** The hold events seen since the last call. */
export function takeHolds(st: SyncStatus): Array<{ pairId: string; count: number }> {
  const out = st.holds ?? [];
  st.holds = [];
  return out;
}

/**
 * Drops the state of pairs that no longer exist. The watcher re-reads its pair
 * list between passes and is not restarted for an unpaired folder, so without
 * this a removed pair's last error would stand until it restarted. True when
 * anything was dropped.
 */
export function retainPairs(st: SyncStatus, ids: ReadonlySet<string>): boolean {
  let changed = false;
  for (const id of Object.keys(st.pairs)) {
    if (!ids.has(id)) {
      delete st.pairs[id];
      changed = true;
    }
  }
  return changed;
}

/**
 * The process is gone. A watcher is meant to run forever, so exiting on its
 * own means the server went away, the token stopped working or the binary
 * crashed — say so instead of leaving a panel that claims all is well. A stop
 * the app asked for (`stopping`) is not an error.
 */
export function markExited(st: SyncStatus, code: number | null, stopping: boolean, signal?: string | null): void {
  st.running = false;
  st.active = null;
  st.live = null;
  st.liveMessage = null;
  // A pair this engine was waiting for is not being waited for any more, and
  // a pass the NEXT engine has not run is not known to be in step.
  for (const h of Object.values(st.pairs)) {
    h.busy = null;
    h.passed = false;
  }
  if (code === SIGNED_OUT_EXIT) {
    st.signedOut = true;
    // The engine said why in its signed_out event; the page words the state
    // itself ('signed-out'), this only keeps the account marked as failing.
    st.lastError = st.lastError ?? 'HTTP 401';
    return;
  }
  if (!stopping && code !== 0) {
    st.exited = String(code ?? signal ?? 'unknown');
    // ⚠ Not one event in its whole life: this engine does not speak the
    // stream (see SyncStatus.events). Its stderr — "unknown flag: --json"
    // from an engine older than this app — is the reason, kept in lastError.
    if (!st.events) st.noStream = true;
    st.lastError = st.lastError ?? unexpectedExitLine(st.exited);
  }
}

/** The line markExited writes when the engine stopped without a word of its
 *  own: no reason, only the code the page already words in its language
 *  (sync-policy.ts folderView tells the two apart). */
export function unexpectedExitLine(exited: string): string {
  return `sync stopped unexpectedly (exit ${exited})`;
}

function health(st: SyncStatus, pairId: string): PairHealth {
  let h = st.pairs[pairId];
  if (!h) {
    h = { error: null, line: null, local: null, busy: null };
    st.pairs[pairId] = h;
  }
  return h;
}

const PHASES: ReadonlySet<string> = new Set(['inventory', 'plan', 'transfer', 'settling']);
const LIVE_STATES: ReadonlySet<string> = new Set(['connected', 'polling', 'offline']);

/** A progress event as the pair's activity. The inventory's two reports add
 *  up: the count here stays while the server is listed (the engine carries it
 *  in both). */
function activity(pairId: string, ev: EngineEvent): SyncActivity | null {
  const phase = str(ev.params.phase);
  if (!PHASES.has(phase)) return null;
  const a: SyncActivity = {
    pairId,
    phase: phase as SyncPhase,
    done: num(ev.params.done) ?? 0,
    total: num(ev.params.total) ?? 0,
    message: ev.message,
  };
  const here = num(ev.params.here);
  const listed = num(ev.params.listed);
  const bytesDone = num(ev.params.bytes_done);
  const bytesTotal = num(ev.params.bytes_total);
  const eta = num(ev.params.eta_seconds);
  if (here !== undefined) a.here = here;
  if (listed !== undefined) a.listed = listed;
  if (bytesDone !== undefined) a.bytesDone = bytesDone;
  if (bytesTotal !== undefined) a.bytesTotal = bytesTotal;
  if (eta !== undefined) a.etaSeconds = eta;
  return a;
}

/** Folds one engine event into the status. */
export function absorbEvent(st: SyncStatus, ev: EngineEvent, now = new Date()): void {
  st.events = (st.events ?? 0) + 1;
  switch (ev.event) {
    case 'hello':
      st.protocol = num(ev.params.protocol) ?? null;
      return;
    case 'live': {
      // The engine's own account of how changes reach it. Kept apart from
      // lastLine, which is what the engine last DID.
      const s = str(ev.params.state);
      if (LIVE_STATES.has(s)) {
        st.live = s as LiveState;
        st.liveMessage = ev.message;
      }
      return;
    }
    case 'window': {
      // Outside the sync window nothing runs; a pass the window cut short
      // ends no other way, so this is what ends its activity (and the sleep
      // guard holding with it).
      st.active = null;
      const opens = Date.parse(str(ev.params.opens_at));
      st.waitingWindow = {
        window: str(ev.params.window),
        opensAt: Number.isFinite(opens) ? opens : null,
        message: ev.message,
      };
      st.lastLine = ev.message;
      st.lastRunAt = now.toISOString();
      return;
    }
    case 'fatal':
      st.active = null;
      if (ev.code === 'signed_out') st.signedOut = true;
      st.lastError = ev.message;
      return;
  }

  const id = ev.pair;
  if (!id) {
    // An error about no one pair is every pair's.
    if (ev.event === 'error') st.lastError = ev.message;
    return;
  }
  const h = health(st, id);
  switch (ev.event) {
    case 'lock':
      // ⚠ Not an error: the folder IS being synced, by another process.
      h.busy = ev.code === 'lock.busy' ? { detail: str(ev.params.detail), message: ev.message } : null;
      if (st.active?.pairId === id) st.active = null;
      return;
    case 'error':
      // A failed action, a pass that could not run, a lock that could not be
      // had: that pair's error until its next clean pass.
      if (st.active?.pairId === id) st.active = null;
      h.error = ev.message;
      return;
    case 'local': {
      const s = str(ev.params.state);
      h.local =
        s === 'too-large' || s === 'unavailable'
          ? { code: s, detail: str(ev.params.detail) || null, message: ev.message }
          : null;
      return;
    }
    case 'hold':
      // Only the number is read: the pair list (`hold_new` / `held`) is what
      // the app shows, and this event is the cue to re-read it.
      (st.holds ??= []).push({ pairId: id, count: num(ev.params.count) ?? 0 });
      return;
    case 'progress': {
      const a = activity(id, ev);
      if (!a) return;
      // A pass of this pair runs here: whatever held it before, this engine
      // holds it now — and we are inside the window.
      h.busy = null;
      h.line = ev.message;
      st.waitingWindow = null;
      st.active = a;
      st.lastLine = ev.message;
      st.lastRunAt = now.toISOString();
      return;
    }
    case 'pass':
      h.busy = null;
      h.passed = true;
      h.line = ev.message;
      if (st.active?.pairId === id) st.active = null;
      st.lastLine = ev.message;
      st.lastRunAt = now.toISOString();
      // The engine works again, whatever stopped it before. ⚠ A pass with
      // failures keeps the pair's error: its "error" events come right after
      // it, on the same ordered stream.
      st.lastError = null;
      if (!(num(ev.params.failed) ?? 0)) h.error = null;
      return;
    default:
      // note, and anything a newer engine adds: worth reading in a terminal.
      return;
  }
}

/** Folds one stdout line: an event, or nothing. */
export function absorbStdout(st: SyncStatus, line: string, now = new Date()): void {
  const ev = parseEvent(line);
  if (ev) absorbEvent(st, ev, now);
}

/** Folds one stderr line. The engine reports on its stream; stderr carries
 *  only what it could not say there — a panic, or the refusal of an engine
 *  that does not know `--json` — and that is the account's error, as it is. */
export function absorbStderr(st: SyncStatus, line: string): void {
  const t = line.trim();
  if (t) st.lastError = t;
}

/**
 * Turns pipe reads into whole lines. A pipe delivers bytes, not lines: under
 * load one read can end in the middle of a line, and each half parsed on its
 * own is nonsense — half an event is no JSON at all. The partial tail is
 * carried to the next read.
 *
 * ⚠ It takes the raw bytes and decodes them itself, in streaming mode: a read
 * that ends inside a multi-byte character (a Turkish message, a file name)
 * must not turn it into two U+FFFD. And a Windows line ending is not part of
 * the line.
 */
export class LineReader {
  /** A tail longer than this is handed on as a line of its own: a process
   *  that writes without ever ending a line must not grow this without
   *  bound. No engine line comes near it. */
  static readonly MAX_LINE = 64 * 1024;
  private carry = '';
  private readonly decoder = new TextDecoder('utf-8');
  private readonly onLine: (line: string) => void;
  constructor(onLine: (line: string) => void) {
    this.onLine = onLine;
  }
  push(chunk: Uint8Array | string): void {
    const text = typeof chunk === 'string' ? chunk : this.decoder.decode(chunk, { stream: true });
    if (!text) return;
    const parts = (this.carry + text).split('\n');
    this.carry = parts.pop() ?? '';
    for (const line of parts) this.onLine(stripCR(line));
    if (this.carry.length > LineReader.MAX_LINE) {
      this.onLine(this.carry);
      this.carry = '';
    }
  }
  /** Whatever was left without a newline (the process exited mid-line). */
  flush(): void {
    const tail = this.carry + this.decoder.decode();
    this.carry = '';
    if (tail) this.onLine(stripCR(tail));
  }
}

function stripCR(s: string): string {
  return s.endsWith('\r') ? s.slice(0, -1) : s;
}
