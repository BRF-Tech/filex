/**
 * e2evault/lock — the holder's side of the write lock, and the client's own
 * vault lock (docs/E2E-VAULT-FORMAT.md → "The write lock", "The idle lock").
 *
 * The write lock (the server's):
 *   - taken at the first action that writes, held while the lease (60 s) has
 *     not run out and the session is not idle;
 *   - a heartbeat renews it every 15 seconds, with `active: true` while
 *     something that will write is being prepared;
 *   - lost when a renewal or a write answers VAULT_LOCK_LOST, or when no
 *     renewal has succeeded for 45 seconds: the holder stops at once;
 *   - released after the last commit; when the page closes, with
 *     `fetch(..., {keepalive: true})`, best effort.
 *
 * The vault lock (the client's alone, decided 2026-10-06):
 *   - after a further 15 minutes in which the open vault sees no activity at
 *     all (no listing, no opening, no download, no write), the client drops
 *     the folder master key and every key derived from it and asks for the
 *     password again. A vault that was opened and never written starts this
 *     clock when it is opened. Fixed in format 1, not a setting.
 *
 * Clocks and timers are injected, so a test runs them without waiting.
 */
import { VaultApiError, type VaultApi, type VaultClientKind, type VaultHolder, type VaultLockGrant, type VaultLostReason } from './api';

/** Renew every 15 seconds. */
export const VAULT_HEARTBEAT_MS = 15_000;
/** No successful renewal for this long: the lock is lost. */
export const VAULT_LOST_AFTER_MS = 45_000;
/** The vault lock: 15 minutes with no activity at all after the write lock ended. */
export const VAULT_IDLE_LOCK_MS = 15 * 60_000;
/** Ask `state` this often while a vault is open. */
export const VAULT_STATE_POLL_MS = 30_000;
/** Commit at least this often during a long change in which packs were stored. */
export const VAULT_COMMIT_EVERY_MS = 30_000;

export interface Clock {
  now(): number;
  setInterval(fn: () => void, ms: number): unknown;
  clearInterval(h: unknown): void;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(h: unknown): void;
}

export const realClock: Clock = {
  now: () => Date.now(),
  setInterval: (fn, ms) => setInterval(fn, ms),
  clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>),
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

/** Why the write lock ended: the server's reasons, or the connection. */
export type LockEnd = VaultLostReason | 'connection';

export interface WriteLockOptions {
  api: Pick<VaultApi, 'lock' | 'renew' | 'release' | 'releaseKeepalive'>;
  path: string;
  client: VaultClientKind;
  label: string;
  clock?: Clock;
  /** The lock ended without this session releasing it. Called once. With
   *  `taken` or `broken` the server names who holds it now, or broke it. */
  onLost(reason: LockEnd, holder?: VaultHolder | null): void;
  /** Headers for the keepalive release (the page is closing; nothing can await). */
  headersNow?: () => Record<string, string>;
}

/** One session's hold on one vault's write lock. */
export class VaultWriteLock {
  private token: string | null = null;
  private timer: unknown = null;
  private lastOk = 0;
  private lastActive = 0;
  private idleMs = 0;
  private busyCount = 0;
  private pendingActive = false;
  private ended = false;
  private readonly clock: Clock;

  constructor(private readonly opts: WriteLockOptions) {
    this.clock = opts.clock ?? realClock;
  }

  get held(): boolean {
    return !!this.token && !this.ended;
  }

  /** The token, for the `X-Filex-Vault-Lock` header of every vault write. */
  get tokenValue(): string | null {
    return this.held ? this.token : null;
  }

  /**
   * When the server ends the lock for idleness, by this session's clock:
   * the last write or active renewal plus the person's idle time.
   */
  get idleDeadline(): number {
    return this.held ? this.lastActive + this.idleMs : 0;
  }

  /** The person's idle time the server read when the lock was taken. */
  get idleSeconds(): number {
    return Math.round(this.idleMs / 1000);
  }

  /** Take the lock. Throws VaultApiError 409 VAULT_LOCKED when someone holds it. */
  async acquire(): Promise<VaultLockGrant> {
    const grant = await this.opts.api.lock(this.opts.path, this.opts.client, this.opts.label);
    const now = this.clock.now();
    this.token = grant.token;
    this.ended = false;
    this.lastOk = now;
    this.lastActive = now;
    this.idleMs = Math.max(60, grant.idle_seconds || 180) * 1000;
    this.timer = this.clock.setInterval(() => void this.beat(), VAULT_HEARTBEAT_MS);
    return grant;
  }

  /**
   * Something that will write is under way (an upload or a save being
   * prepared): heartbeats say `active: true` until `done` is called.
   */
  busy(): () => void {
    this.busyCount++;
    this.pendingActive = true;
    let once = false;
    return () => {
      if (once) return;
      once = true;
      this.busyCount = Math.max(0, this.busyCount - 1);
    };
  }

  /** A vault write succeeded: it counts as activity on the server. */
  wrote(): void {
    if (!this.held) return;
    const now = this.clock.now();
    this.lastActive = now;
    this.lastOk = now;
  }

  /** A write answered VAULT_LOCK_LOST: end now. */
  lostBy(err: unknown): boolean {
    if (err instanceof VaultApiError && err.code === 'VAULT_LOCK_LOST') {
      this.end((err.reason as LockEnd) || 'expired', err.holder);
      return true;
    }
    return false;
  }

  /** One heartbeat. Exposed for tests and for an immediate renewal. */
  async beat(): Promise<void> {
    if (!this.held || !this.token) return;
    const active = this.busyCount > 0 || this.pendingActive;
    this.pendingActive = false;
    try {
      await this.opts.api.renew(this.opts.path, this.token, active);
      if (!this.held) return;
      const now = this.clock.now();
      this.lastOk = now;
      if (active) this.lastActive = now;
    } catch (err) {
      if (!this.held) return;
      if (this.lostBy(err)) return;
      if (this.clock.now() - this.lastOk >= VAULT_LOST_AFTER_MS) this.end('connection');
    }
  }

  /** Release after the last commit. Answers whether or not it still held. */
  async release(): Promise<void> {
    const token = this.token;
    this.stop();
    this.token = null;
    if (!token) return;
    try {
      await this.opts.api.release(this.opts.path, token);
    } catch {
      /* the lease covers it */
    }
  }

  /** The page is closing. */
  releaseOnUnload(): void {
    const token = this.token;
    this.stop();
    this.token = null;
    if (token) this.opts.api.releaseKeepalive(this.opts.path, token, this.opts.headersNow?.() ?? {});
  }

  /** Stop without telling anybody (the vault was closed some other way). */
  dispose(): void {
    this.stop();
    this.token = null;
  }

  private stop(): void {
    if (this.timer !== null) this.clock.clearInterval(this.timer);
    this.timer = null;
  }

  private end(reason: LockEnd, holder: VaultHolder | null = null): void {
    if (this.ended) return;
    this.ended = true;
    this.stop();
    this.token = null;
    this.opts.onLost(reason, holder);
  }
}

/**
 * The client's vault lock: one clock that a vault's every activity resets,
 * and that does not run while this session holds the write lock (it starts
 * when the writer goes back to read-only, or when a vault that never wrote
 * was opened).
 */
export class VaultIdleLock {
  private timer: unknown = null;
  private lastActivity = 0;
  private paused = false;
  private readonly clock: Clock;

  constructor(
    private readonly onLock: () => void,
    clock?: Clock,
    private readonly ms = VAULT_IDLE_LOCK_MS,
  ) {
    this.clock = clock ?? realClock;
  }

  /** The vault was opened: the clock starts. */
  start(): void {
    this.lastActivity = this.clock.now();
    this.paused = false;
    this.arm();
  }

  /** A listing, an opening, a download or a write. */
  touch(): void {
    this.lastActivity = this.clock.now();
    if (!this.paused) this.arm();
  }

  /** This session holds the write lock: the vault lock waits. */
  pause(): void {
    this.paused = true;
    this.disarm();
  }

  /** The write lock ended: 15 minutes from now (or from the last activity). */
  resume(): void {
    this.paused = false;
    this.lastActivity = this.clock.now();
    this.arm();
  }

  /** When the vault locks itself, by this clock; 0 while it waits. */
  get deadline(): number {
    return this.paused || this.timer === null ? 0 : this.lastActivity + this.ms;
  }

  stop(): void {
    this.disarm();
  }

  /** The page came back (a tab woken from the background): lock now if due. */
  check(): void {
    if (this.paused || this.timer === null) return;
    if (this.clock.now() - this.lastActivity >= this.ms) {
      this.disarm();
      this.onLock();
    }
  }

  private arm(): void {
    this.disarm();
    const left = Math.max(0, this.lastActivity + this.ms - this.clock.now());
    this.timer = this.clock.setTimeout(() => {
      this.timer = null;
      if (this.paused) return;
      // A late timer (a sleeping laptop) still locks; an early one re-arms.
      if (this.clock.now() - this.lastActivity >= this.ms) this.onLock();
      else this.arm();
    }, left);
  }

  private disarm(): void {
    if (this.timer !== null) this.clock.clearTimeout(this.timer);
    this.timer = null;
  }
}
