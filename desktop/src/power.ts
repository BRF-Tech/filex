// Staying awake while sync moves files, and not swapping the app under it.
//
// Measured on a real first sync: 11,704 files / 52.6 GiB took about nine hours
// over an 18 Mbit uplink, and Windows slept for 1 h 40 min of them overnight —
// no request left the machine while it slept. The engine resumed fine after
// the wake; the hours were simply lost.
//
// ⚠ No `electron` import — main.ts hands in `powerSaveBlocker` — so node:test
// can drive the rules (same boundary as src/notifications.ts).

/** The slice of a SyncStatus this module reads. */
export interface BusyStatus {
  running: boolean;
  active: unknown;
}

/**
 * Some watcher is working on a pair right now — listing, planning,
 * transferring or settling. Between rounds (the 30 s wait) nothing is busy,
 * and a watcher that is gone is not busy whatever it last said.
 */
export function syncBusy(statuses: readonly BusyStatus[]): boolean {
  return statuses.some((s) => s.running && s.active != null);
}

/** Electron's powerSaveBlocker, as far as this module uses it. */
export interface PowerBlocker {
  start(type: 'prevent-app-suspension'): number;
  stop(id: number): void;
}

/**
 * Holds ONE 'prevent-app-suspension' blocker while sync is busy.
 *
 * 'prevent-app-suspension', not 'prevent-display-sleep': the screen may go
 * dark and lock as usual; only the system's idle sleep waits. It does not
 * override a closed lid, a power button or an empty battery — nor should it.
 */
export class SleepGuard {
  private id: number | null = null;
  private readonly blocker: PowerBlocker;
  private readonly log: (msg: string) => void;

  constructor(blocker: PowerBlocker, log: (msg: string) => void = () => {}) {
    this.blocker = blocker;
    this.log = log;
  }

  get holding(): boolean {
    return this.id !== null;
  }

  /** Busy takes the blocker (once), idle lets it go. True when that changed. */
  update(busy: boolean): boolean {
    if (busy && this.id === null) {
      this.id = this.blocker.start('prevent-app-suspension');
      this.log('sync is moving files: the computer will not idle-sleep until it is done');
      return true;
    }
    if (!busy && this.id !== null) {
      this.release();
      return true;
    }
    return false;
  }

  /** Lets go, whatever sync is doing — on quit, and before an update install. */
  release(): void {
    if (this.id === null) return;
    this.blocker.stop(this.id);
    this.id = null;
    this.log('sync is idle: the computer may sleep again');
  }
}

/**
 * Whether the downloaded update may be installed NOW, silently.
 *
 * The human must be away and no window open — the app disappears and comes
 * back during the swap — and, since the swap stops every watcher first, the
 * engine must not be in the middle of a round: an overnight first sync is
 * exactly the "idle machine, no window" this check used to fire on. The
 * install still happens on quit if no such moment comes.
 */
export function quietMomentForUpdate(o: {
  ready: boolean;
  applying: boolean;
  windowOpen: boolean;
  idleSeconds: number;
  idleThreshold: number;
  syncBusy: boolean;
}): boolean {
  if (o.applying || !o.ready) return false;
  if (o.windowOpen) return false;
  if (o.idleSeconds < o.idleThreshold) return false;
  return !o.syncBusy;
}
