// The vault's two clocks (docs/E2E-VAULT-FORMAT.md → "The write lock",
// "The idle lock"): the holder's side of the server's write lock (heartbeat,
// lost after 45 seconds without a renewal, lost at once when the server says
// so, released on the way out) and the client's own vault lock (15 minutes
// with nothing done in an open vault, not while it writes).

import { describe, expect, it, vi } from 'vitest';

import {
  VAULT_HEARTBEAT_MS,
  VAULT_IDLE_LOCK_MS,
  VAULT_LOST_AFTER_MS,
  VaultIdleLock,
  VaultWriteLock,
  type Clock,
} from '../../../packages/core/src/lib/e2evault/lock';
import { VaultApiError } from '../../../packages/core/src/lib/e2evault/api';

/** A clock the test moves by hand: timers fire when `advance` passes them. */
function fakeClock(): Clock & { advance(ms: number): Promise<void>; at: number } {
  let now = 1_000_000;
  let seq = 0;
  const timers = new Map<number, { at: number; every: number; fn: () => void }>();
  const clock = {
    get at() {
      return now;
    },
    now: () => now,
    setInterval: (fn: () => void, ms: number) => {
      const id = ++seq;
      timers.set(id, { at: now + ms, every: ms, fn });
      return id;
    },
    clearInterval: (h: unknown) => void timers.delete(h as number),
    setTimeout: (fn: () => void, ms: number) => {
      const id = ++seq;
      timers.set(id, { at: now + ms, every: 0, fn });
      return id;
    },
    clearTimeout: (h: unknown) => void timers.delete(h as number),
    async advance(ms: number) {
      const end = now + ms;
      for (;;) {
        const next = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        const [id, t] = next;
        now = t.at;
        if (t.every) t.at += t.every;
        else timers.delete(id);
        t.fn();
        // Let the promise a timer started settle before the next one.
        for (let i = 0; i < 12; i++) await Promise.resolve();
      }
      now = end;
    },
  };
  return clock;
}

function lockApi(over: Partial<Record<'lock' | 'renew' | 'release' | 'releaseKeepalive', unknown>> = {}) {
  return {
    lock: vi.fn(async () => ({ token: 'tok', generation: 3, lease_seconds: 60, idle_seconds: 180, expires_at: '' })),
    renew: vi.fn(async () => ({ expires_at: '', idle_until: '' })),
    release: vi.fn(async () => undefined),
    releaseKeepalive: vi.fn(),
    ...over,
  } as never as ConstructorParameters<typeof VaultWriteLock>[0]['api'] & {
    lock: ReturnType<typeof vi.fn>;
    renew: ReturnType<typeof vi.fn>;
    release: ReturnType<typeof vi.fn>;
    releaseKeepalive: ReturnType<typeof vi.fn>;
  };
}

describe('the write lock (holder side)', () => {
  it('takes the lock and renews it every 15 seconds, active only while something will write', async () => {
    const clock = fakeClock();
    const api = lockApi();
    const lost = vi.fn();
    const lock = new VaultWriteLock({ api, path: 'docs://Kasa', client: 'web', label: 'Firefox, Linux', clock, onLost: lost });
    const grant = await lock.acquire();
    expect(grant.token).toBe('tok');
    expect(api.lock).toHaveBeenCalledWith('docs://Kasa', 'web', 'Firefox, Linux');
    expect(lock.held).toBe(true);
    expect(lock.tokenValue).toBe('tok');
    expect(lock.idleSeconds).toBe(180);

    await clock.advance(VAULT_HEARTBEAT_MS);
    expect(api.renew).toHaveBeenLastCalledWith('docs://Kasa', 'tok', false);
    const done = lock.busy();
    await clock.advance(VAULT_HEARTBEAT_MS);
    expect(api.renew).toHaveBeenLastCalledWith('docs://Kasa', 'tok', true);
    done();
    await clock.advance(VAULT_HEARTBEAT_MS);
    expect(api.renew).toHaveBeenLastCalledWith('docs://Kasa', 'tok', false);
    expect(lost).not.toHaveBeenCalled();
  });

  it('counts the idle time from the last write or active renewal', async () => {
    const clock = fakeClock();
    const lock = new VaultWriteLock({ api: lockApi(), path: 'docs://Kasa', client: 'web', label: '', clock, onLost: vi.fn() });
    await lock.acquire();
    const t0 = clock.at;
    expect(lock.idleDeadline).toBe(t0 + 180_000);
    await clock.advance(20_000);
    lock.wrote();
    expect(lock.idleDeadline).toBe(t0 + 20_000 + 180_000);
  });

  it('a renewal answered VAULT_LOCK_LOST ends it at once, with the server reason', async () => {
    const clock = fakeClock();
    const api = lockApi({
      renew: vi.fn(async () => {
        throw new VaultApiError(409, 'VAULT_LOCK_LOST', 'lost', { reason: 'idle' });
      }),
    });
    const lost = vi.fn();
    const lock = new VaultWriteLock({ api, path: 'docs://Kasa', client: 'web', label: '', clock, onLost: lost });
    await lock.acquire();
    await clock.advance(VAULT_HEARTBEAT_MS);
    expect(lost).toHaveBeenCalledTimes(1);
    expect(lost).toHaveBeenCalledWith('idle', null);
    expect(lock.held).toBe(false);
    expect(lock.tokenValue).toBeNull();
    // No more renewals after the end.
    const calls = api.renew.mock.calls.length;
    await clock.advance(VAULT_HEARTBEAT_MS * 4);
    expect(api.renew.mock.calls.length).toBe(calls);
  });

  it('no renewal for 45 seconds: lost ("the connection was lost")', async () => {
    const clock = fakeClock();
    const api = lockApi({
      renew: vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      }),
    });
    const lost = vi.fn();
    const lock = new VaultWriteLock({ api, path: 'docs://Kasa', client: 'web', label: '', clock, onLost: lost });
    await lock.acquire();
    await clock.advance(VAULT_LOST_AFTER_MS - 1);
    expect(lost).not.toHaveBeenCalled();
    await clock.advance(VAULT_HEARTBEAT_MS);
    expect(lost).toHaveBeenCalledWith('connection', null);
  });

  it('a write answered VAULT_LOCK_LOST ends it too', async () => {
    const lost = vi.fn();
    const lock = new VaultWriteLock({ api: lockApi(), path: 'docs://Kasa', client: 'web', label: '', clock: fakeClock(), onLost: lost });
    await lock.acquire();
    expect(lock.lostBy(new VaultApiError(409, 'VAULT_LOCK_LOST', '', { reason: 'broken', holder: { name: 'Admin', client: 'web' } }))).toBe(true);
    expect(lost).toHaveBeenCalledWith('broken', { name: 'Admin', client: 'web' });
    expect(lock.lostBy(new VaultApiError(409, 'VAULT_GENERATION', ''))).toBe(false);
  });

  it('release, and the keepalive release when the page closes', async () => {
    const api = lockApi();
    const lock = new VaultWriteLock({ api, path: 'docs://Kasa', client: 'desktop', label: '', clock: fakeClock(), onLost: vi.fn(), headersNow: () => ({ 'X-CSRF-TOKEN': 'c' }) });
    await lock.acquire();
    await lock.release();
    expect(api.release).toHaveBeenCalledWith('docs://Kasa', 'tok');
    expect(lock.held).toBe(false);

    const again = new VaultWriteLock({ api, path: 'docs://Kasa', client: 'web', label: '', clock: fakeClock(), onLost: vi.fn(), headersNow: () => ({ 'X-CSRF-TOKEN': 'c' }) });
    await again.acquire();
    again.releaseOnUnload();
    expect(api.releaseKeepalive).toHaveBeenCalledWith('docs://Kasa', 'tok', { 'X-CSRF-TOKEN': 'c' });
  });

  it('VAULT_LOCKED is the caller\'s to show: the lock is not held', async () => {
    const api = lockApi({
      lock: vi.fn(async () => {
        throw new VaultApiError(409, 'VAULT_LOCKED', 'held', { holder: { name: 'Ayşe', client: 'web' }, since: '2026-10-06T10:00:00Z', retry_after: 40 });
      }),
    });
    const lock = new VaultWriteLock({ api, path: 'docs://Kasa', client: 'web', label: '', clock: fakeClock(), onLost: vi.fn() });
    await expect(lock.acquire()).rejects.toMatchObject({ code: 'VAULT_LOCKED' });
    expect(lock.held).toBe(false);
  });
});

describe('the vault lock (15 minutes, the client\'s own)', () => {
  it('a vault opened and never written locks 15 minutes after it was opened', async () => {
    const clock = fakeClock();
    const onLock = vi.fn();
    const idle = new VaultIdleLock(onLock, clock);
    idle.start();
    expect(idle.deadline).toBe(clock.at + VAULT_IDLE_LOCK_MS);
    await clock.advance(VAULT_IDLE_LOCK_MS - 1);
    expect(onLock).not.toHaveBeenCalled();
    await clock.advance(1);
    expect(onLock).toHaveBeenCalledTimes(1);
  });

  it('any activity (a listing, an opening, a download, a write) starts it again', async () => {
    const clock = fakeClock();
    const onLock = vi.fn();
    const idle = new VaultIdleLock(onLock, clock);
    idle.start();
    await clock.advance(10 * 60_000);
    idle.touch();
    await clock.advance(10 * 60_000);
    expect(onLock).not.toHaveBeenCalled();
    await clock.advance(5 * 60_000);
    expect(onLock).toHaveBeenCalledTimes(1);
  });

  it('does not run while this session holds the write lock, and starts again when it ends', async () => {
    const clock = fakeClock();
    const onLock = vi.fn();
    const idle = new VaultIdleLock(onLock, clock);
    idle.start();
    idle.pause();
    expect(idle.deadline).toBe(0);
    await clock.advance(60 * 60_000);
    expect(onLock).not.toHaveBeenCalled();
    idle.resume();
    expect(idle.deadline).toBe(clock.at + VAULT_IDLE_LOCK_MS);
    await clock.advance(VAULT_IDLE_LOCK_MS);
    expect(onLock).toHaveBeenCalledTimes(1);
  });

  it('a tab woken from the background locks at once when the time has passed', () => {
    let now = 0;
    const clock: Clock = {
      now: () => now,
      setInterval: () => 1,
      clearInterval: () => undefined,
      // A timer that never fires: a throttled background tab.
      setTimeout: () => 1,
      clearTimeout: () => undefined,
    };
    const onLock = vi.fn();
    const idle = new VaultIdleLock(onLock, clock);
    idle.start();
    now = VAULT_IDLE_LOCK_MS + 5_000;
    idle.check();
    expect(onLock).toHaveBeenCalledTimes(1);
  });

  it('the 15 minutes are fixed in format 1', () => {
    expect(VAULT_IDLE_LOCK_MS).toBe(15 * 60_000);
    expect(VAULT_HEARTBEAT_MS).toBe(15_000);
    expect(VAULT_LOST_AFTER_MS).toBe(45_000);
  });
});
