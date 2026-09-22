// The sync engine's output, read the way the engine writes it.
//
// The watcher's stdout and stderr are the only channel between the engine and
// this app. Every badge, the explorer's bottom strip, the rail dot and the line
// under each folder in Settings is parsed out of them — HERE, in one place, so
// the string format lives in one file and the rest of the app gets typed data.
//
// ⚠ No `electron` import. Everything in this file is a rule about text, and a
// rule that cannot run under `node --test` is a rule nobody checks (same
// boundary as src/notifications.ts).

export type SyncPhase = 'inventory' | 'plan' | 'transfer' | 'settling';

/** One pair's live phase, parsed from the engine's progress lines. */
export interface SyncActivity {
  pairId: string;
  phase: SyncPhase;
  /** transfer only: actions done / planned. 0/0 elsewhere. */
  done: number;
  total: number;
}

/** What the supervisor has observed about one account's sync process. */
export interface SyncStatus {
  accountId: string;
  running: boolean;
  lastLine: string;
  lastRunAt: string | null;
  /** The most recent error still standing — see `errors`. null when every
   *  pair's last round went through. */
  lastError: string | null;
  /** The pair the engine is working on RIGHT NOW, or null between runs.
   *  One value, not a map: the engine walks its pairs sequentially. */
  active: SyncActivity | null;
  /** Errors still standing, by pair id ('*' for the process itself). A pair's
   *  entry goes away when a later round of THAT pair settles completely. */
  errors?: Record<string, string>;
  /** The server no longer accepts this account's token: the engine exited with
   *  SIGNED_OUT_EXIT, or (an older engine, which keeps looping) printed a 401.
   *  The supervisor stops the watcher and does not restart it. */
  signedOut?: boolean;
  /** Outside its sync window (`--window`), the engine starts no rounds and
   *  says so once: this is that window, e.g. '22:00-07:00'. Cleared when a
   *  round starts. The page still checks the clock — see pairView. */
  waitingWindow?: string | null;
}

/** `filex sync run` exits with this status when the server answers 401, and
 *  does not retry. */
export const SIGNED_OUT_EXIT = 3;

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
 * Splits a byte stream into whole lines.
 *
 * ⚠ A pipe delivers whatever the OS had buffered, not lines. Parsing each
 * `data` chunk on its own read `pair-1: transfer: 1` + `20/300` as a transfer
 * of 0/0 followed by a stray line, and a chunk boundary inside a multi-byte
 * character turned a Turkish file name in an error into replacement
 * characters. The decoder runs in streaming mode for the second reason.
 */
export class LineBuffer {
  private rest = '';
  private readonly decoder = new TextDecoder('utf-8');

  /** The complete lines this chunk finishes; the unterminated tail is kept. */
  push(chunk: Uint8Array | string): string[] {
    const text = typeof chunk === 'string' ? chunk : this.decoder.decode(chunk, { stream: true });
    if (!text) return [];
    const parts = (this.rest + text).split('\n');
    this.rest = parts.pop() ?? '';
    return parts.map(stripCR);
  }

  /** The last line when the stream ends without a newline — a process that
   *  dies mid-sentence still said something worth reading. */
  end(): string[] {
    const tail = this.rest + this.decoder.decode();
    this.rest = '';
    return tail ? [stripCR(tail)] : [];
  }
}

function stripCR(s: string): string {
  return s.endsWith('\r') ? s.slice(0, -1) : s;
}

export type EngineLine =
  /** `<pair>: <phase>: <detail>` — Engine.Progress, printed even under --quiet. */
  | { kind: 'progress'; pairId: string; phase: SyncPhase; detail: string }
  /** `<pair>: already in step` or `<pair>: N/M done — …`: the pair's round ended.
   *  `complete` is false when fewer actions succeeded than were planned. */
  | { kind: 'settled'; pairId: string; complete: boolean }
  /** stderr `<pair>: <error>` — that pair's whole round failed. */
  | { kind: 'pair-error'; pairId: string; message: string }
  /** Any other stderr line (`  ! <action failed>`, `filex: …`). */
  | { kind: 'error'; message: string }
  /** `sync: waiting for the sync window W` — outside the window, no rounds;
   *  `sync: the sync window W closed; …` — a round was cancelled by it (and
   *  prints no summary: this line is what ends the activity). */
  | { kind: 'window'; window: string; closed: boolean }
  /** Any other stdout line. */
  | { kind: 'info'; text: string };

const PROGRESS_RE = /^(\S+): (inventory|plan|transfer|settling): (.*)$/;
const WINDOW_WAIT_RE = /^sync: waiting for the sync window (\S+)$/;
const WINDOW_CLOSED_RE = /^sync: the sync window (\S+) closed\b/;
const SETTLED_RE = /^(\S+): (?:(already in step)$|(\d+)\/(\d+) done\b)/;
const PAIR_ERROR_RE = /^(\S+): /;

/** One line of engine output, classified. null for a blank line. */
export function parseEngineLine(raw: string, stream: 'out' | 'err'): EngineLine | null {
  const line = raw.trim();
  if (!line) return null;
  if (stream === 'err') {
    const pe = PAIR_ERROR_RE.exec(line);
    return pe ? { kind: 'pair-error', pairId: pe[1], message: line } : { kind: 'error', message: line };
  }
  const p = PROGRESS_RE.exec(line);
  if (p) return { kind: 'progress', pairId: p[1], phase: p[2] as SyncPhase, detail: p[3] };
  const ww = WINDOW_WAIT_RE.exec(line);
  if (ww) return { kind: 'window', window: ww[1], closed: false };
  const wc = WINDOW_CLOSED_RE.exec(line);
  if (wc) return { kind: 'window', window: wc[1], closed: true };
  const s = SETTLED_RE.exec(line);
  if (s) {
    return { kind: 'settled', pairId: s[1], complete: s[2] !== undefined || s[3] === s[4] };
  }
  return { kind: 'info', text: line };
}

/** The key for an error that belongs to no pair — `filex: …` as it exits. */
const PROCESS = '*';

/**
 * One account's watcher, as the app sees it: feed it the process's output and
 * read `status`.
 *
 * ⚠ An error is a statement about one ROUND of one pair, not a verdict on the
 * process. It used to be sticky — any stderr line set lastError and nothing
 * cleared it until the watcher restarted — so one network blip kept the rail
 * dot red and the folder saying "HTTP 502" all day while every later round
 * went through.
 */
export class SyncStatusTracker {
  readonly status: SyncStatus;
  private readonly out = new LineBuffer();
  private readonly err = new LineBuffer();
  // ⚠ A plain field, not a `private readonly now` constructor parameter: the
  // test runner strips types and nothing else, and a parameter property is
  // code, not a type (ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX).
  private readonly now: () => Date;
  /** Standing errors in the order they were raised — the last one is shown. */
  private readonly errors = new Map<string, string>();
  /** The pair whose summary line came last: the engine prints a round's
   *  `  ! <action failed>` lines right AFTER that pair's summary. */
  private lastSettled: string | null = null;

  constructor(accountId: string, now: () => Date = () => new Date()) {
    this.now = now;
    this.status = {
      accountId,
      running: true,
      lastLine: 'starting…',
      lastRunAt: null,
      lastError: null,
      active: null,
      errors: {},
      waitingWindow: null,
    };
  }

  /**
   * Drops the errors of pairs that no longer exist. The watcher re-reads its
   * pair list between rounds and is not restarted for an unpaired folder, so
   * without this a removed pair's last error would stand until it restarted.
   */
  retainPairs(ids: ReadonlySet<string>): boolean {
    let changed = false;
    for (const key of [...this.errors.keys()]) {
      if (key !== PROCESS && !ids.has(key)) {
        this.errors.delete(key);
        changed = true;
      }
    }
    if (changed) this.publishErrors();
    return changed;
  }

  /** Feeds one chunk of stdout or stderr. True when the status changed — the
   *  caller repaints the app on that, and only on that. */
  feed(chunk: Uint8Array | string, stream: 'out' | 'err'): boolean {
    const buf = stream === 'out' ? this.out : this.err;
    let changed = false;
    for (const line of buf.push(chunk)) changed = this.apply(line, stream) || changed;
    return changed;
  }

  /** The streams closed: read what was still buffered. */
  end(): boolean {
    let changed = false;
    for (const line of this.out.end()) changed = this.apply(line, 'out') || changed;
    for (const line of this.err.end()) changed = this.apply(line, 'err') || changed;
    return changed;
  }

  /**
   * The process is gone. A watcher is meant to run forever, so exiting on its
   * own means the server went away, the token stopped working or the binary
   * crashed — say so instead of leaving a panel that claims all is well. A
   * stop the app asked for (`stopping`) is not an error.
   */
  exited(code: number | null, stopping: boolean, signal?: string | null): void {
    this.status.running = false;
    this.status.active = null;
    if (code === SIGNED_OUT_EXIT) {
      this.status.signedOut = true;
      if (this.status.lastError === null) {
        this.raise(PROCESS, 'signed out: the server no longer accepts this token (HTTP 401)');
      }
      return;
    }
    if (!stopping && code !== 0 && this.status.lastError === null) {
      this.raise(PROCESS, `sync stopped unexpectedly (exit ${code ?? signal ?? 'unknown'})`);
    }
  }

  private apply(raw: string, stream: 'out' | 'err'): boolean {
    const ev = parseEngineLine(raw, stream);
    if (!ev) return false;
    const st = this.status;
    if (stream === 'err' && isUnauthorizedLine(raw)) st.signedOut = true;
    switch (ev.kind) {
      case 'progress': {
        st.waitingWindow = null; // a round started: we are inside the window
        const tr = ev.phase === 'transfer' ? /^(\d+)\/(\d+)/.exec(ev.detail) : null;
        st.active = {
          pairId: ev.pairId,
          phase: ev.phase,
          done: tr ? Number(tr[1]) : 0,
          total: tr ? Number(tr[2]) : 0,
        };
        break;
      }
      case 'settled':
        if (st.active?.pairId === ev.pairId) st.active = null;
        this.lastSettled = ev.pairId;
        if (ev.complete) {
          this.clear(ev.pairId);
        } else if (!this.errors.has(ev.pairId)) {
          // Fewer actions went through than were planned. The engine names
          // each failure on stderr, and those lines can arrive before or after
          // this one; until they do, the summary itself is the news.
          this.raise(ev.pairId, raw.trim());
        }
        break;
      case 'pair-error':
        // `<pair-id>: <error>` means that pair's run died — it is not active.
        if (st.active?.pairId === ev.pairId) st.active = null;
        this.raise(ev.pairId, ev.message);
        return true;
      case 'error':
        // `  ! <action failed>` belongs to the round being reported: the
        // active pair when stderr overtook the summary, otherwise the pair
        // whose summary came last.
        this.raise(st.active?.pairId ?? this.lastSettled ?? PROCESS, ev.message);
        return true;
      case 'window':
        // Either way nothing is being worked on until the window opens.
        st.active = null;
        st.waitingWindow = ev.window;
        break;
      case 'info':
        break;
    }
    st.lastLine = raw.trim();
    st.lastRunAt = this.now().toISOString();
    return true;
  }

  private raise(key: string, message: string): void {
    this.errors.delete(key); // re-insert: the newest error is the one shown
    this.errors.set(key, message);
    this.publishErrors();
  }

  private clear(key: string): void {
    if (this.errors.delete(key)) this.publishErrors();
  }

  private publishErrors(): void {
    this.status.errors = Object.fromEntries(this.errors);
    const all = [...this.errors.values()];
    this.status.lastError = all.length ? all[all.length - 1]! : null;
  }
}
