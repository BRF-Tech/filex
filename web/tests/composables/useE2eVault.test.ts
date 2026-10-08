// The explorer's vault mode (composables/useE2eVault) against a server in
// memory that keeps the vault API's contract (docs/E2E-VAULT-FORMAT.md →
// "API"): listing from the index, a change that takes the write lock and
// commits, somebody else's lock, a lock lost mid-change (nothing saved, and
// the strip lists what), a lock lost to idleness, the vault locking itself
// after 15 minutes, and the server never hearing a path below the vault.

import { afterEach, describe, expect, it, vi } from 'vitest';

import { createVault } from '@brftech/filex-core/src/lib/e2ecrypto';
import { emptyIndexState } from '@brftech/filex-core/src/lib/e2evault/vindex';
import { parseVaultPath } from '@brftech/filex-core/src/lib/e2evault/layout';
import { VAULT_IDLE_LOCK_MS, type Clock } from '@brftech/filex-core/src/lib/e2evault/lock';
import { useE2eVault, type VaultHost } from '@brftech/filex-core/src/composables/useE2eVault';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const ROOT = 'docs://Kasa';
const en = wordsIn('en');

function fakeClock(): Clock & { advance(ms: number): Promise<void> } {
  let now = 1_800_000_000_000;
  let seq = 0;
  const timers = new Map<number, { at: number; every: number; fn: () => void }>();
  return {
    now: () => now,
    setInterval: (fn, ms) => {
      timers.set(++seq, { at: now + ms, every: ms, fn });
      return seq;
    },
    clearInterval: (h) => void timers.delete(h as number),
    setTimeout: (fn, ms) => {
      timers.set(++seq, { at: now + ms, every: 0, fn });
      return seq;
    },
    clearTimeout: (h) => void timers.delete(h as number),
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
        await settle();
      }
      now = end;
    },
  };
}

async function settle() {
  for (let i = 0; i < 50; i++) await Promise.resolve();
  await new Promise((r) => setTimeout(r, 0));
}

/** The vault API and the download, in memory. */
function fakeServer(made: Awaited<ReturnType<typeof createVault>>) {
  const files = new Map<string, Uint8Array>();
  files.set('.filex-e2e.json', new TextEncoder().encode(JSON.stringify(made.marker)));
  files.set('v/idx/0000000000000001.fxi', made.index);
  const srv = {
    files,
    generation: 1,
    lock: null as null | { token: string; holder: { name: string; client: string; label: string }; since: string; mine: boolean },
    ended: '' as string,
    failIndexWith: '' as string,
    seen: [] as string[],
  };
  const json = (status: number, body?: unknown) =>
    new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  const handler = async (url: string, init: RequestInit = {}) => {
    const u = new URL(url, 'http://filex.test');
    srv.seen.push(decodeURIComponent(u.pathname + u.search));
    const hdr = (init.headers ?? {}) as Record<string, string>;
    const token = hdr['X-Filex-Vault-Lock'];
    const held = () => {
      if (!srv.lock || srv.lock.token !== token) return json(409, { error: 'VAULT_LOCK_LOST', message: 'lost', reason: srv.ended || 'expired' });
      return null;
    };
    if (u.pathname === '/api/files/manager' && u.searchParams.get('action') === 'download') {
      const p = String(u.searchParams.get('path'));
      const rel = p.slice(ROOT.length + 1);
      const b = files.get(rel);
      if (!b) return json(404, { error: 'not_found' });
      const range = hdr.Range;
      if (range) {
        const m = /^bytes=(\d+)-(\d+)$/.exec(range)!;
        return new Response(b.slice(Number(m[1]), Number(m[2]) + 1), { status: 206 });
      }
      return new Response(b.slice(), { status: 200 });
    }
    const sub = u.pathname.replace('/api/files/e2e/vault', '');
    const body = typeof init.body === 'string' ? JSON.parse(init.body) : null;
    switch (sub) {
      case '/state':
        return json(200, {
          vault_id: 'x',
          pack_log2: made.marker.vault!.pack,
          generation: srv.generation,
          lock: srv.lock ? { holder: srv.lock.holder, since: srv.lock.since, expires_at: '', mine: srv.lock.token === token } : null,
        });
      case '/list': {
        const kind = u.searchParams.get('kind');
        const items = [...files.keys()]
          .map((k) => ({ k, p: parseVaultPath(k) }))
          .filter(({ p }) => p.kind === (kind === 'index' ? 'index' : 'pack'))
          .map(({ k, p }) =>
            kind === 'index'
              ? { generation: p.gen, size: files.get(k)!.length, mtime: '1970-01-01T00:00:00Z' }
              : { id: p.id, size: files.get(k)!.length, mtime: '1970-01-01T00:00:00Z' },
          );
        // `now`: the server's clock, which retention is measured against.
        return json(200, { items, next: null, now: new Date().toISOString() });
      }
      case '/lock':
        // The server's sentence (srvtext server.e2e.vault.locked), as the
        // real handler words it for the reader.
        if (srv.lock)
          return json(409, {
            error: 'VAULT_LOCKED',
            message: `${srv.lock.holder?.name ?? '?'} is writing this vault. It stays readable; try again when they are done.`,
            holder: srv.lock.holder,
            since: srv.lock.since,
            retry_after: 30,
          });
        srv.lock = { token: 'tok-' + Math.random(), holder: { name: 'me', client: body.client, label: body.label }, since: '2026-10-06T10:00:00Z', mine: true };
        srv.ended = '';
        return json(200, { token: srv.lock.token, generation: srv.generation, lease_seconds: 60, idle_seconds: 180, expires_at: '' });
      case '/lock/renew':
        return held() ?? json(200, { expires_at: '', idle_until: '' });
      case '/lock/release':
        if (srv.lock && srv.lock.token === token) srv.lock = null;
        return json(204);
      case '/lock/break':
        srv.lock = null;
        srv.ended = 'broken';
        return json(204);
      case '/pack': {
        const refused = held();
        if (refused) return refused;
        const id = String(u.searchParams.get('id'));
        files.set(`v/p/${id.slice(0, 2)}/${id}.fxp`, new Uint8Array(init.body as Uint8Array));
        return json(201);
      }
      case '/index': {
        const refused = held();
        if (refused) return refused;
        if (srv.failIndexWith) return json(409, { error: srv.failIndexWith, message: '', reason: 'taken', holder: { name: 'Ayşe', client: 'desktop', label: 'filex, macOS' } });
        const gen = Number(u.searchParams.get('generation'));
        if (gen !== srv.generation + 1) return json(409, { error: 'VAULT_GENERATION', message: '', latest: srv.generation });
        files.set(`v/idx/${gen.toString(16).padStart(16, '0')}.fxi`, new Uint8Array(init.body as Uint8Array));
        srv.generation = gen;
        return json(201, { generation: gen });
      }
      case '/delete': {
        const refused = held();
        if (refused) return refused;
        for (const id of body.packs as string[]) files.delete(`v/p/${id.slice(0, 2)}/${id}.fxp`);
        for (const g of body.indexes as number[]) files.delete(`v/idx/${g.toString(16).padStart(16, '0')}.fxi`);
        return json(200, { deleted: { packs: body.packs.length, indexes: body.indexes.length } });
      }
      default:
        return json(404, { error: 'not_found' });
    }
  };
  return { srv, handler };
}

/**
 * What a test opened. A commit sets the collection going in the background
 * (docs/E2E-VAULT-FORMAT.md → "Garbage collection": after each commit), and
 * it outlives the test's last await: afterEach closes every vault and lets
 * that work finish against the server in memory before fetch is given back -
 * else it reaches the real network after the test (tests/helpers/noNetwork).
 */
const live: Array<{ vault: ReturnType<typeof useE2eVault>; srv: ReturnType<typeof fakeServer>['srv'] }> = [];

async function setup(extra: Partial<VaultHost> = {}) {
  const made = await createVault('correct horse battery', { packLog2: 22 });
  const { srv, handler } = fakeServer(made);
  vi.stubGlobal('fetch', vi.fn(handler));
  const clock = fakeClock();
  const calls = { dropped: [] as string[], locked: [] as Array<[string, string]>, changed: 0, toasts: [] as string[] };
  const host: VaultHost = {
    http: {
      endpoints: { manager: '/api/files/manager' },
      authHeaders: async (extra = {}) => ({ ...extra }),
      credentialsMode: () => 'same-origin',
      downloadUrl: (p) => `/api/files/manager?action=download&path=${encodeURIComponent(p)}`,
    },
    headersNow: () => ({}),
    t: (k, v) => en(k, v),
    toast: (m) => void calls.toasts.push(m),
    clientKind: () => 'web',
    dropKeys: (r) => void calls.dropped.push(r),
    onLocked: (r, why) => void calls.locked.push([r, why]),
    onChanged: () => void calls.changed++,
    clock,
    ...extra,
  };
  const vault = useE2eVault(host);
  live.push({ vault, srv });
  await vault.open(ROOT, made.marker, made.fmk, { perm: 'owner' });
  return { vault, srv, clock, calls, made };
}

afterEach(async () => {
  const opened = live.splice(0);
  for (const { vault } of opened) vault.closeAll('silent');
  // Until no request has come for a few rounds (the collection lists, deletes
  // and may repack), at most 200 rounds.
  let quiet = 0;
  let seen = -1;
  for (let i = 0; i < 200 && quiet < 5; i++) {
    await settle();
    const now = opened.reduce((n, { srv }) => n + srv.seen.length, 0);
    quiet = now === seen ? quiet + 1 : 0;
    seen = now;
  }
  vi.unstubAllGlobals();
});

describe('useE2eVault', () => {
  it('opens read-only at the latest generation, and lists the tree', async () => {
    const { vault } = await setup();
    expect(vault.isOpen(ROOT)).toBe(true);
    expect(vault.rows(ROOT)).toEqual([]);
    expect(vault.strips[ROOT].mode).toBe('read');
    expect(vault.strips[ROOT].generation).toBe(1);
    expect(vault.rootOf(`${ROOT}/a/b`)).toBe(ROOT);
    expect(vault.rootOf('docs://Kasa2/x')).toBeNull();
  });

  it('a change takes the write lock, commits, and the rows are the new tree', async () => {
    const { vault, srv } = await setup();
    expect(await vault.mkdir(ROOT, 'Belgeler')).toBe(true);
    expect(srv.generation).toBe(2);
    expect(vault.strips[ROOT].mode).toBe('write');
    expect(vault.rows(ROOT)!.map((r) => [r.basename, r.type])).toEqual([['Belgeler', 'dir']]);
    const file = new File([new TextEncoder().encode('gizli not')], 'not.txt', { lastModified: 1_791_277_200_000 });
    expect(await vault.upload(`${ROOT}/Belgeler`, [file])).toBe(true);
    const rows = vault.rows(`${ROOT}/Belgeler`)!;
    expect(rows.map((r) => r.basename)).toEqual(['not.txt']);
    expect(rows[0].path).toBe(`${ROOT}/Belgeler/not.txt`);
    expect(rows[0].vault_root).toBe(ROOT);
    expect(new TextDecoder().decode(await vault.readBytes(`${ROOT}/Belgeler/not.txt`))).toBe('gizli not');
    // A taken name gets a number: nothing in a vault is overwritten unasked.
    await vault.upload(`${ROOT}/Belgeler`, [file]);
    expect(vault.rows(`${ROOT}/Belgeler`)!.map((r) => r.basename)).toEqual(['not (2).txt', 'not.txt']);
  });

  // docs/E2E-VAULT-FORMAT.md → "What the holder does": a name taken is asked
  // about. The explorer asks with its "already there" dialog (askName); a
  // vault keeps no earlier version, so nothing is replaced.
  it('an upload whose name is taken asks first: the free name, or not at all', async () => {
    const questions: Array<{ name: string; folder: string; suggested: string }> = [];
    let answer = true;
    const { vault, srv } = await setup({
      askName: async (q) => {
        questions.push(q);
        return answer;
      },
    });
    await vault.mkdir(ROOT, 'Belgeler');
    const one = new File(['bir'], 'not.txt');
    expect(await vault.upload(`${ROOT}/Belgeler`, [one])).toBe(true);
    expect(questions).toEqual([]);
    const gen = srv.generation;

    answer = false;
    expect(await vault.upload(`${ROOT}/Belgeler`, [new File(['iki'], 'not.txt')])).toBe(false);
    expect(questions).toEqual([{ name: 'not.txt', folder: 'Kasa / Belgeler', suggested: 'not (2).txt' }]);
    expect(srv.generation, 'declined: nothing committed').toBe(gen);
    expect(vault.rows(`${ROOT}/Belgeler`)!.map((r) => r.basename)).toEqual(['not.txt']);

    answer = true;
    expect(await vault.upload(`${ROOT}/Belgeler`, [new File(['iki'], 'not.txt'), new File(['üç'], 'yeni.txt')])).toBe(true);
    expect(questions).toHaveLength(2);
    expect(vault.rows(`${ROOT}/Belgeler`)!.map((r) => r.basename)).toEqual(['not (2).txt', 'not.txt', 'yeni.txt']);
    expect(new TextDecoder().decode(await vault.readBytes(`${ROOT}/Belgeler/not.txt`)), 'the first one kept').toBe('bir');
    expect(new TextDecoder().decode(await vault.readBytes(`${ROOT}/Belgeler/not (2).txt`))).toBe('iki');
  });

  it('rename, move, copy and delete, inside the vault', async () => {
    const { vault } = await setup();
    await vault.mkdir(ROOT, 'A');
    await vault.mkdir(ROOT, 'B');
    await vault.upload(`${ROOT}/A`, [new File(['x'], 'x.txt')]);
    expect(await vault.rename(`${ROOT}/A/x.txt`, 'y.txt')).toBe(true);
    expect(await vault.copy([`${ROOT}/A/y.txt`], `${ROOT}/B`)).toBe(true);
    expect(await vault.move([`${ROOT}/A/y.txt`], ROOT)).toBe(true);
    expect(vault.rows(ROOT)!.map((r) => r.basename)).toEqual(['A', 'B', 'y.txt']);
    expect(vault.rows(`${ROOT}/B`)!.map((r) => r.basename)).toEqual(['y.txt']);
    expect(new TextDecoder().decode(await vault.readBytes(`${ROOT}/B/y.txt`))).toBe('x');
    expect(await vault.remove([`${ROOT}/A`, `${ROOT}/y.txt`])).toBe(true);
    expect(vault.rows(ROOT)!.map((r) => r.basename)).toEqual(['B']);
  });

  it('somebody else holds the lock: nothing is written, the strip says who', async () => {
    const { vault, srv, calls } = await setup();
    srv.lock = { token: 'theirs', holder: { name: 'Ayşe', client: 'desktop', label: 'filex, macOS' }, since: '2026-10-06T09:00:00Z', mine: false };
    expect(await vault.mkdir(ROOT, 'X')).toBe(false);
    expect(srv.generation).toBe(1);
    expect(vault.strips[ROOT].holder?.name).toBe('Ayşe');
    // ⚠ 0.54 (#209): the server's sentence, not a client copy of it
    // (e2e.vault.locked_by is gone).
    expect(calls.toasts.at(-1)).toBe('Ayşe is writing this vault. It stays readable; try again when they are done.');
  });

  it('a lock lost mid-change: nothing committed, and the strip lists what was not saved', async () => {
    const { vault, srv } = await setup();
    await vault.mkdir(ROOT, 'A');
    srv.failIndexWith = 'VAULT_LOCK_LOST';
    expect(await vault.upload(ROOT, [new File(['1'], 'rapor.pdf'), new File(['2'], 'liste.csv')])).toBe(false);
    expect(srv.generation).toBe(2);
    expect(vault.strips[ROOT].unsaved).toEqual(['rapor.pdf', 'liste.csv']);
    expect(vault.strips[ROOT].mode).toBe('read');
    expect(vault.strips[ROOT].lost).toBe('taken');
    expect(vault.strips[ROOT].lostTo).toBe('Ayşe');
    expect(vault.rows(ROOT)!.map((r) => r.basename)).toEqual(['A']);
  });

  it('a lock broken by the owner reaches the holder at its next heartbeat', async () => {
    const { vault, srv, clock } = await setup();
    await vault.mkdir(ROOT, 'A');
    srv.lock = null;
    srv.ended = 'idle';
    await clock.advance(15_000);
    expect(vault.strips[ROOT].mode).toBe('read');
    expect(vault.strips[ROOT].lost).toBe('idle');
    expect(vault.strips[ROOT].lostMinutes).toBe(3);
  });

  it('a vault opened and never written locks itself 15 minutes later: keys dropped, lock screen', async () => {
    const { vault, clock, calls } = await setup();
    await clock.advance(VAULT_IDLE_LOCK_MS - 1000);
    expect(vault.isOpen(ROOT)).toBe(true);
    vault.touch(ROOT); // a listing
    await clock.advance(VAULT_IDLE_LOCK_MS - 1000);
    expect(vault.isOpen(ROOT)).toBe(true);
    await clock.advance(2000);
    expect(vault.isOpen(ROOT)).toBe(false);
    expect(calls.dropped).toEqual([ROOT]);
    expect(calls.locked).toEqual([[ROOT, 'idle']]);
  });

  it('while it writes, the vault does not lock itself; 15 minutes after the write lock ends, it does', async () => {
    const { vault, srv, clock, calls } = await setup();
    await vault.mkdir(ROOT, 'A');
    await clock.advance(VAULT_IDLE_LOCK_MS + 60_000);
    expect(vault.isOpen(ROOT)).toBe(true);
    // The server ends the write lock (idle); the heartbeat hears it.
    srv.lock = null;
    srv.ended = 'idle';
    await clock.advance(15_000);
    expect(vault.strips[ROOT].mode).toBe('read');
    await clock.advance(VAULT_IDLE_LOCK_MS);
    expect(vault.isOpen(ROOT)).toBe(false);
    expect(calls.locked).toEqual([[ROOT, 'idle']]);
  });

  it('take over: break the other lock, then take it', async () => {
    const { vault, srv } = await setup();
    srv.lock = { token: 'theirs', holder: { name: 'Ayşe', client: 'web', label: '' }, since: '', mine: false };
    expect(await vault.takeOver(ROOT)).toBe(true);
    expect(srv.lock?.token).not.toBe('theirs');
    expect(vault.strips[ROOT].mode).toBe('write');
  });

  it('the server never hears a path below the vault', async () => {
    const { vault, srv } = await setup();
    await vault.mkdir(ROOT, 'Gizli klasör');
    await vault.upload(`${ROOT}/Gizli klasör`, [new File(['s'], 'maaşlar.xlsx')]);
    await vault.readBytes(`${ROOT}/Gizli klasör/maaşlar.xlsx`);
    await vault.rename(`${ROOT}/Gizli klasör/maaşlar.xlsx`, 'zamlar.xlsx');
    for (const s of srv.seen) {
      expect(s).not.toContain('Gizli');
      expect(s).not.toContain('maaş');
      expect(s).not.toContain('zamlar');
    }
  });

  it('Lock: the write lock goes back and the session ends', async () => {
    const { vault, srv, calls } = await setup();
    await vault.mkdir(ROOT, 'A');
    vault.lock(ROOT);
    await settle();
    expect(vault.isOpen(ROOT)).toBe(false);
    expect(srv.lock).toBeNull();
    expect(calls.dropped).toEqual([ROOT]);
    expect(calls.locked).toEqual([[ROOT, 'manual']]);
  });

  it('a vault this tab made starts at generation 1 without asking', async () => {
    const made = await createVault('correct horse battery');
    const { handler } = fakeServer(made);
    const spy = vi.fn(handler);
    vi.stubGlobal('fetch', spy);
    const v = useE2eVault({
      http: { endpoints: { manager: '/api/files/manager' }, authHeaders: async () => ({}), credentialsMode: () => 'same-origin', downloadUrl: (p) => p },
      headersNow: () => ({}),
      t: (k) => k,
      toast: () => undefined,
      clientKind: () => 'web',
      dropKeys: () => undefined,
      onLocked: () => undefined,
      onChanged: () => undefined,
      clock: fakeClock(),
    });
    await v.open('docs://Yeni', made.marker, made.fmk, { initial: emptyIndexState(1) });
    expect(spy).not.toHaveBeenCalled();
    expect(v.strips['docs://Yeni'].generation).toBe(1);
  });
});
