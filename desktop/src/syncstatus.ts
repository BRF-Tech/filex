// What the sync supervisor knows about one account's engine, and the rules
// that turn the engine's output lines into it.
//
// The watcher's stdout and stderr are the ONLY channel between the engine and
// this app: every badge, the explorer's bottom strip, the rail dot and the line
// under each folder in Settings is parsed out of them — HERE, in one place, so
// the string format lives in one file and the rest of the app gets typed data.
//
// ⚠ No `electron` import (same reason as notifications.ts): this module holds
// the rules, and a module that imports electron cannot run under `node --test`.
// ⚠ No relative imports either: `node --test` strips types but does not map
// `./x.js` onto `./x.ts`, so a test importing this file would fail to load.
//
// The line formats are the contract with backend/cmd/filex (sync.go,
// synclive.go). Every line about a pair starts with `<pair-id>: `:
//
//   stdout  <pair>: inventory|plan|transfer|settling: …   progress
//           <pair>: transfer: D/T[ (B of S[, about E left])]  … with bytes and an estimate
//           <pair>: hold: N item(s) here are not on the server …   a hold (see hold.go)
//           <pair>: already in step                       a clean pass
//           <pair>: A/P done — …[, N failed]  (…)         a pass; "failed" = it had errors
//           <pair>: ~ …                                   raced: both versions kept next pass
//           <pair>: local: poll-only — <code> — <detail>  local file-system watching unavailable
//           <pair>: local: watched                        …available again
//           live: connected|polling|offline — …           how SERVER changes reach the engine
//           sync: waiting for the sync window W           outside --window: nothing runs
//           sync: the sync window W closed; …             a pass the window cut short
//   stderr  <pair>: ! <action error>                      one action of a pass failed
//           <pair>: note: …                               worth reading in a terminal, not an error
//           <pair>: <error>                               the pass could not run
//           filex: <error>                                the command itself stopped (main.go)
//           anything else (cobra's "Error: …")            not about one pair
//
// and the exit status SIGNED_OUT_EXIT (3) when the server refused the token.

/**
 * How SERVER-side changes reach the engine (`live: <state> — <detail>`):
 *
 *   - `connected` — subscribed to the server's change stream; a save in the
 *     browser is on disk within about a second.
 *   - `polling`   — the server cannot announce changes (older than 0.43, or its
 *     realtime is off): server-side edits arrive at the next check.
 *   - `offline`   — the server is unreachable right now; the engine retries.
 */
export type LiveState = 'connected' | 'polling' | 'offline';

const liveRe = /^live: (connected|polling|offline)(?: — (.*))?$/;

/** Parses one engine stdout line into a live-state update, or null. */
export function parseLiveLine(line: string): { live: LiveState; detail: string | null } | null {
  const m = liveRe.exec(line.trim());
  return m ? { live: m[1] as LiveState, detail: m[2] ?? null } : null;
}

/**
 * Why changes made on THIS computer in one pair are only found by the full
 * check (the engine could not watch its folder):
 *   - `too-large`   — macOS watches every file separately, and the folder is
 *     past the engine's budget;
 *   - `unavailable` — the operating system refused (a watch limit, a
 *     permission, no watcher at all).
 */
export interface LocalNote {
  code: 'too-large' | 'unavailable';
  detail: string | null;
}

/** One pair's health, as its card shows it. */
export interface PairHealth {
  /** Its last failure — until a later pass of the SAME pair completes clean. */
  error: string | null;
  /** The last thing the engine did for this pair (progress or result). */
  line: string | null;
  local: LocalNote | null;
}

export type SyncPhase = 'inventory' | 'plan' | 'transfer' | 'settling';

/** One pair's live phase, parsed from the engine's progress lines. */
export interface SyncActivity {
  pairId: string;
  phase: SyncPhase;
  /** transfer only: actions done / planned. 0/0 elsewhere. */
  done: number;
  total: number;
  /** transfer only, once there are bytes to move: the engine's own figures,
   *  e.g. '1.2 GiB' of '52.6 GiB'. */
  bytesDone?: string;
  bytesTotal?: string;
  /** transfer only, once the engine has an estimate: its words ('8h 10m')
   *  and the same in seconds, for a window that words it in its language. */
  eta?: string;
  etaSeconds?: number;
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
  /** How changes reach the engine (see LiveState). null until the engine
   *  has said — an older bundled engine never does; it only ever polls. */
  live: LiveState | null;
  liveDetail: string | null;
  /** Per pair, keyed by pair id. */
  pairs: Record<string, PairHealth>;
  /** The server no longer accepts this account's token: the engine exited with
   *  SIGNED_OUT_EXIT, or (an older engine, which keeps looping) printed a 401.
   *  The supervisor stops the watcher and does not restart it. */
  signedOut?: boolean;
  /** Outside its sync window (`--window`) the engine runs nothing and says so
   *  once: this is that window, e.g. '22:00-07:00'. Cleared when a pass
   *  starts. The page still checks the clock — see folderView in
   *  sync-policy.ts. */
  waitingWindow?: string | null;
  /** Hold lines not yet handed to the supervisor (takeHolds): each is a cue to
   *  re-read the pair list, which carries the numbers the app shows. */
  holds?: Array<{ pairId: string; count: number }>;
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
    liveDetail: null,
    pairs: {},
    waitingWindow: null,
    holds: [],
  };
}

/** A pair's card: its own error (or the account's), line and local note. */
export function pairView(st: SyncStatus, pairId: string): PairHealth {
  const h = st.pairs[pairId];
  return {
    error: h?.error ?? st.lastError,
    line: h?.line ?? null,
    local: h?.local ?? null,
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

/** The hold lines seen since the last call. */
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
  if (code === SIGNED_OUT_EXIT) {
    st.signedOut = true;
    st.lastError = st.lastError ?? 'signed out: the server no longer accepts this token (HTTP 401)';
    return;
  }
  if (!stopping && code !== 0) {
    st.lastError = st.lastError ?? `sync stopped unexpectedly (exit ${code ?? signal ?? 'unknown'})`;
  }
}

/**
 * A 401 in the engine's own words: `HTTP 401: <message>` (cliclient's
 * APIError) or `(HTTP 401)` (the signed-out line). Anchored on what follows
 * the number, so a FILE called "HTTP 401.txt" in an error line does not sign
 * anybody out.
 */
const UNAUTHORIZED_RE = /\bHTTP 401(?=[:)]|$)/;

/** True for an engine error line that means "the server refused the token". */
export function isUnauthorizedLine(line: string): boolean {
  return UNAUTHORIZED_RE.test(line.trim());
}

/**
 * `transfer: <done>/<total>` and, from engines that know, ` (<bytes done> of
 * <bytes total>[, about <eta> left])`. The leading `done/total` never changes
 * shape — the explorer's strip reads just that.
 */
const TRANSFER_RE = /^(\d+)\/(\d+)(?:\s+\((.+?) of (.+?)(?:, about (.+?) left)?\))?\s*$/;

/** The engine's estimate ('8h 10m', '12m', '45s') in seconds, or null. */
export function parseEta(text: string): number | null {
  const m = /^(?:(\d+)h)?\s*(?:(\d+)m)?\s*(?:(\d+)s)?$/.exec(text.trim());
  if (!m || (m[1] === undefined && m[2] === undefined && m[3] === undefined)) return null;
  return Number(m[1] ?? 0) * 3600 + Number(m[2] ?? 0) * 60 + Number(m[3] ?? 0);
}

function transferActivity(pairId: string, detail: string): SyncActivity {
  const m = TRANSFER_RE.exec(detail);
  // A detail this parser does not know still has its counts in front.
  const head = m ?? /^(\d+)\/(\d+)/.exec(detail);
  const act: SyncActivity = {
    pairId,
    phase: 'transfer',
    done: head ? Number(head[1]) : 0,
    total: head ? Number(head[2]) : 0,
  };
  if (m && m[3] !== undefined && m[4] !== undefined) {
    act.bytesDone = m[3];
    act.bytesTotal = m[4];
    if (m[5] !== undefined) {
      act.eta = m[5];
      const secs = parseEta(m[5]);
      if (secs !== null) act.etaSeconds = secs;
    }
  }
  return act;
}

function health(st: SyncStatus, pairId: string): PairHealth {
  let h = st.pairs[pairId];
  if (!h) {
    h = { error: null, line: null, local: null };
    st.pairs[pairId] = h;
  }
  return h;
}

// `<pair-id>: rest`. Cobra's own prefix is "Error: …" and the command's own
// exit line is "filex: …" (backend/cmd/filex/main.go) — never a pair.
//
// ⚠ "filex: …" read as a pair called `filex` put the reason the watcher DIED
// under a folder nobody has, so every real folder said only "stopped", and
// the next reconcile dropped that pair — and with it the one line that said
// why.
const pairLineRe = /^(\S+): (.*)$/;
const progressRe = /^(inventory|plan|transfer|settling): (.*)$/;
// A pass's summary. ⚠ "failed" is the engine saying the pass had errors; the
// error TEXT comes on stderr, which may be read before or after this line.
const summaryRe = /^(?:already in step$|\d+\/\d+ done\b(.*)$)/;
const localRe = /^local: (?:(watched)|poll-only — (too-large|unavailable)(?: — (.*))?)$/;
const holdRe = /^hold: (\d+)\b/;
const windowWaitRe = /^sync: waiting for the sync window (\S+)$/;
const windowClosedRe = /^sync: the sync window (\S+) closed\b/;

function pairOf(t: string): { id: string; rest: string } | null {
  const m = pairLineRe.exec(t);
  if (!m || m[1] === 'Error' || m[1] === 'live' || m[1] === 'sync' || m[1] === 'filex') return null;
  return { id: m[1], rest: m[2] };
}

/** Folds one engine output line into the status. */
export function absorbLine(st: SyncStatus, line: string, isErr: boolean, now = new Date()): void {
  const t = line.trim();
  if (!t) return;
  const p = pairOf(t);

  if (isErr) {
    if (isUnauthorizedLine(t)) st.signedOut = true;
    if (!p) {
      st.lastError = t;
      return;
    }
    // `<pair-id>: <error>` means that pair's run died — it is not active.
    if (st.active?.pairId === p.id) st.active = null;
    if (p.rest.startsWith('note: ')) return;
    health(st, p.id).error = p.rest.startsWith('! ') ? p.rest.slice(2) : p.rest;
    return;
  }

  if (t.startsWith('live: ')) {
    // The engine's own account of how changes reach it. Kept apart from
    // lastLine, which is what the engine last DID — a state line there would
    // hide the sync result it came after.
    const lv = parseLiveLine(t);
    if (lv) {
      st.live = lv.live;
      st.liveDetail = lv.detail;
    }
    return;
  }
  // Outside the sync window nothing runs; a pass the window cut short prints
  // no summary, so this line is what ends its activity (and the sleep guard
  // holding with it).
  const ww = windowWaitRe.exec(t) ?? windowClosedRe.exec(t);
  if (ww) {
    st.active = null;
    st.waitingWindow = ww[1];
    st.lastLine = t;
    st.lastRunAt = now.toISOString();
    return;
  }
  st.lastLine = t;
  st.lastRunAt = now.toISOString();
  if (!p) return;

  const lm = localRe.exec(p.rest);
  if (lm) {
    health(st, p.id).local = lm[1] ? null : { code: lm[2] as LocalNote['code'], detail: lm[3] ?? null };
    return;
  }
  const hm = holdRe.exec(p.rest);
  if (hm) {
    // Only the number is read: the pair list (`hold_new` / `held`) is what
    // the app shows, and this line is the cue to re-read it.
    (st.holds ??= []).push({ pairId: p.id, count: Number(hm[1]) });
    return;
  }
  const h = health(st, p.id);
  if (!p.rest.startsWith('~ ')) h.line = p.rest;

  const pr = progressRe.exec(p.rest);
  if (pr) {
    st.waitingWindow = null; // a pass started: we are inside the window
    st.active =
      pr[1] === 'transfer'
        ? transferActivity(p.id, pr[2])
        : { pairId: p.id, phase: pr[1] as SyncPhase, done: 0, total: 0 };
    return;
  }
  const sm = summaryRe.exec(p.rest);
  if (sm) {
    if (st.active?.pairId === p.id) st.active = null;
    // The engine works again, whatever stopped it before.
    st.lastError = null;
    if (!/, \d+ failed\b/.test(sm[1] ?? '')) h.error = null;
  }
}

/**
 * Turns pipe reads into whole lines. A pipe delivers bytes, not lines: under
 * load one read can end in the middle of a line, and each half parsed on its
 * own is nonsense — half a summary clears nothing, half an error line becomes
 * the error text. The partial tail is carried to the next read.
 *
 * ⚠ It takes the raw bytes and decodes them itself, in streaming mode: a read
 * that ends inside a multi-byte character (the "—" separators, a Turkish file
 * name) must not turn it into two U+FFFD. And a Windows line ending is not
 * part of the line.
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
