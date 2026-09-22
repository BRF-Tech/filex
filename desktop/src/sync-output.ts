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
  lastError: string | null;
  /** The pair the engine is working on RIGHT NOW, or null between runs.
   *  One value, not a map: the engine walks its pairs sequentially. */
  active: SyncActivity | null;
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
  /** Any other stdout line. */
  | { kind: 'info'; text: string };

const PROGRESS_RE = /^(\S+): (inventory|plan|transfer|settling): (.*)$/;
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
  const s = SETTLED_RE.exec(line);
  if (s) {
    return { kind: 'settled', pairId: s[1], complete: s[2] !== undefined || s[3] === s[4] };
  }
  return { kind: 'info', text: line };
}

/**
 * One account's watcher, as the app sees it: feed it the process's output and
 * read `status`.
 */
export class SyncStatusTracker {
  readonly status: SyncStatus;
  private readonly out = new LineBuffer();
  private readonly err = new LineBuffer();
  // ⚠ A plain field, not a `private readonly now` constructor parameter: the
  // test runner strips types and nothing else, and a parameter property is
  // code, not a type (ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX).
  private readonly now: () => Date;

  constructor(accountId: string, now: () => Date = () => new Date()) {
    this.now = now;
    this.status = {
      accountId,
      running: true,
      lastLine: 'starting…',
      lastRunAt: null,
      lastError: null,
      active: null,
    };
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
    if (!stopping && code !== 0) {
      this.status.lastError =
        this.status.lastError ?? `sync stopped unexpectedly (exit ${code ?? signal ?? 'unknown'})`;
    }
  }

  private apply(raw: string, stream: 'out' | 'err'): boolean {
    const ev = parseEngineLine(raw, stream);
    if (!ev) return false;
    const st = this.status;
    switch (ev.kind) {
      case 'progress': {
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
        break;
      case 'pair-error':
        st.lastError = ev.message;
        // `<pair-id>: <error>` means that pair's run died — it is not active.
        if (st.active?.pairId === ev.pairId) st.active = null;
        return true;
      case 'error':
        st.lastError = ev.message;
        return true;
      case 'info':
        break;
    }
    st.lastLine = raw.trim();
    st.lastRunAt = this.now().toISOString();
    return true;
  }
}
