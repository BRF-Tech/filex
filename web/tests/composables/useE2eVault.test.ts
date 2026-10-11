// The explorer's vault mode (composables/useE2eVault) against a server in
// memory that keeps the vault API's contract (docs/E2E-VAULT-FORMAT.md →
// "API"): listing from the index, a change that takes the write lock and
// commits, somebody else's lock, a lock lost mid-change (nothing saved, and
// the strip lists what), a lock lost to idleness, the vault locking itself
// after 15 minutes, and the server never hearing a path below the vault -
// and (0.56) an app's interface reading and saving a file of the vault.

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

/**
 * The host's HTTP as setup() gives it. ⚠ `downloadUrl` must be the manager's
 * download: the vault reads its index through it, and a stub that returned
 * the bare path read nothing - the vault opened unreadable and every write
 * was refused.
 */
const HTTP: VaultHost['http'] = {
  endpoints: { manager: '/api/files/manager' },
  authHeaders: async (extra = {}) => ({ ...extra }),
  credentialsMode: () => 'same-origin',
  downloadUrl: (p) => `/api/files/manager?action=download&path=${encodeURIComponent(p)}`,
};

/** The server's sentences for what the browser says on its own clock, as
 *  `state` answers them (backend vaultWords) - never composed in the tab. */
const SRV_WORDS = {
  app_lock_soon: 'An app has changes in “Kasa” that are not saved, and the vault locks itself in a minute. Save them, or keep the vault open.',
  app_lock_soon_keep: 'Keep it open',
  app_lock_kept: '“Kasa” stays open: it locks itself 15 minutes after the last thing done in it.',
};

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
    /** The `message` of that refusal (the server's sentence; '' says none). */
    failIndexSays: '' as string,
    seen: [] as string[],
    /** Who owns the vault folder, as `state` and `create` answer it. */
    owner: { owner_id: 7, owner_name: 'Dana Reyes', owner_self: true } as Record<string, unknown>,
    /** What `state` says the browser may say on its own clock (0.56). */
    words: { ...SRV_WORDS } as Record<string, string>,
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
      case '/create':
        return json(201, { generation: 1, ...srv.owner });
      case '/state':
        return json(200, {
          ...srv.owner,
          vault_id: 'x',
          pack_log2: made.marker.vault!.pack,
          generation: srv.generation,
          lock: srv.lock ? { holder: srv.lock.holder, since: srv.lock.since, expires_at: '', mine: srv.lock.token === token } : null,
          words: srv.words,
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
        if (srv.failIndexWith) return json(409, { error: srv.failIndexWith, message: srv.failIndexSays, reason: 'taken', holder: { name: 'Ayşe', client: 'desktop', label: 'filex, macOS' } });
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

  // 0.55: every row inside a vault is the vault folder's, and the server says
  // whose (`state`, `create`). The rows carried no owner, and the Owner column
  // read "System" for a file the person had just uploaded (0.54 screenshots).
  it("every row carries the vault folder's owner, as the server said it", async () => {
    const { vault } = await setup();
    expect(await vault.mkdir(ROOT, 'Belgeler')).toBe(true);
    const file = new File([new TextEncoder().encode('gizli not')], 'not.txt', { lastModified: 1_791_277_200_000 });
    expect(await vault.upload(`${ROOT}/Belgeler`, [file])).toBe(true);
    const rows = [...vault.rows(ROOT)!, ...vault.rows(`${ROOT}/Belgeler`)!];
    expect(rows.map((r) => r.basename)).toEqual(['Belgeler', 'not.txt']);
    for (const r of rows) {
      expect(r).toMatchObject({ owner_id: 7, owner_name: 'Dana Reyes', owner_self: true });
    }
  });

  it('somebody else asking is not told the vault is theirs', async () => {
    const made = await createVault('correct horse battery', { packLog2: 22 });
    const { srv, handler } = fakeServer(made);
    srv.owner = { owner_id: 9, owner_name: 'Ayşe' };
    vi.stubGlobal('fetch', vi.fn(handler));
    const v = useE2eVault({
      http: HTTP,
      headersNow: () => ({}),
      t: (k) => k,
      toast: () => undefined,
      clientKind: () => 'web',
      dropKeys: () => undefined,
      onLocked: () => undefined,
      onChanged: () => undefined,
      clock: fakeClock(),
    });
    live.push({ vault: v, srv });
    await v.open(ROOT, made.marker, made.fmk, { perm: 'editor' });
    expect(await v.mkdir(ROOT, 'Ortak')).toBe(true);
    const [row] = v.rows(ROOT)!;
    expect(row).toMatchObject({ basename: 'Ortak', owner_id: 9, owner_name: 'Ayşe' });
    expect((row as Record<string, unknown>).owner_self).toBeUndefined();
  });

  it("a vault this tab made takes its owner from create's answer", async () => {
    const made = await createVault('correct horse battery', { packLog2: 22 });
    const { srv, handler } = fakeServer(made);
    vi.stubGlobal('fetch', vi.fn(handler));
    const v = useE2eVault({
      http: HTTP,
      headersNow: () => ({}),
      t: (k) => k,
      toast: () => undefined,
      clientKind: () => 'web',
      dropKeys: () => undefined,
      onLocked: () => undefined,
      onChanged: () => undefined,
      clock: fakeClock(),
    });
    live.push({ vault: v, srv });
    await v.create(ROOT, made.marker, made.index);
    await v.open(ROOT, made.marker, made.fmk, { justMade: true, perm: 'owner' });
    expect(srv.seen.some((x) => x.startsWith('/api/files/e2e/vault/state')), 'no state asked').toBe(false);
    expect(await v.mkdir(ROOT, 'Belgeler')).toBe(true);
    expect(v.rows(ROOT)![0]).toMatchObject({ basename: 'Belgeler', owner_id: 7, owner_name: 'Dana Reyes', owner_self: true });
  });

  it('a vault this tab made starts at generation 1 without asking', async () => {
    const made = await createVault('correct horse battery');
    const { handler } = fakeServer(made);
    const spy = vi.fn(handler);
    vi.stubGlobal('fetch', spy);
    const v = useE2eVault({
      http: HTTP,
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

// filex 0.56 (task #189) - an app's interface editing a file of the vault
// (lib/appPlaintext createAppVaultDoor -> useE2eVault readForApp /
// saveForApp). The engine reads the file from its packs and writes the app's
// save as the vault's next generation, under the write lock, all in this tab:
// the server sees packs and an index - never the file's name, a path below
// the vault, or a byte of what the app saved. One writer at a time: a save
// while somebody else writes, or after this session's write lock ended, is
// refused and said; a file changed or gone since the app read it is not
// written over or made again.
//
// Red before 0.56: the engine had no readForApp / saveForApp (TypeError).
describe('an app editing a file of the vault (filex 0.56)', () => {
  const FILE = `${ROOT}/Bütçe/plan 2027.txt`;
  const NAME = 'plan 2027.txt';
  const BODY = 'gizli bütçe: 2027 planı';
  const enc = (s: string) => new TextEncoder().encode(s);
  const dec = (b: Uint8Array) => new TextDecoder().decode(b);
  /** `needle` appears in `hay`, byte for byte. */
  const holds = (hay: Uint8Array, needle: Uint8Array) => {
    outer: for (let i = 0; i + needle.length <= hay.length; i++) {
      for (let j = 0; j < needle.length; j++) if (hay[i + j] !== needle[j]) continue outer;
      return true;
    }
    return false;
  };

  async function withFile(extra: Partial<VaultHost> = {}) {
    const ctx = await setup(extra);
    expect(await ctx.vault.mkdir(ROOT, 'Bütçe')).toBe(true);
    expect(await ctx.vault.upload(`${ROOT}/Bütçe`, [new File([enc(BODY)], NAME, { lastModified: 1_791_277_200_000 })])).toBe(true);
    return ctx;
  }

  async function readIt(vault: ReturnType<typeof useE2eVault>) {
    const r = await vault.readForApp(FILE, 1 << 20);
    if (!r.ok) throw new Error(`read refused: ${r.why}`);
    return r;
  }

  it('hands over the plaintext and its version, and writes the save over it as the next generation', async () => {
    const { vault, srv } = await withFile();
    const r = await readIt(vault);
    expect(dec(r.bytes)).toBe(BODY);
    const gen = srv.generation;
    const text = 'yeni içerik: kayıt sonrası';
    const saved = await vault.saveForApp(FILE, enc(text), r.sig);
    expect(saved).toMatchObject({ ok: true, size: enc(text).length });
    expect(srv.generation, 'one commit').toBe(gen + 1);
    expect(dec(await vault.readBytes(FILE))).toBe(text);
    expect(vault.rows(`${ROOT}/Bütçe`)!.map((x) => x.basename), 'written over the file, not beside it').toEqual([NAME]);
    // The next save names the version this one wrote.
    if (!saved.ok) throw new Error('save');
    expect(saved.sig).not.toBe(r.sig);
    expect((await vault.saveForApp(FILE, enc('üçüncü'), saved.sig)).ok).toBe(true);
    expect(dec(await vault.readBytes(FILE))).toBe('üçüncü');
  });

  it('the server hears no name and no path below the vault, and holds no byte of what the app saved', async () => {
    const { vault, srv } = await withFile();
    const r = await readIt(vault);
    expect((await vault.saveForApp(FILE, enc('yeni içerik: kayıt sonrası'), r.sig)).ok).toBe(true);
    for (const u of srv.seen) {
      expect(u).not.toContain('Bütçe');
      expect(u).not.toContain('plan 2027');
    }
    for (const [k, b] of srv.files) {
      expect(holds(b, enc('kayıt sonrası')), k).toBe(false);
      expect(holds(b, enc('gizli bütçe')), k).toBe(false);
    }
  });

  it('a file written meanwhile is not saved over: changed, nothing committed, and said', async () => {
    const { vault, srv, calls } = await withFile();
    const first = await readIt(vault);
    // Another save lands first (the same file open in a second frame).
    expect((await vault.saveForApp(FILE, enc('önce gelen'), first.sig)).ok).toBe(true);
    const gen = srv.generation;
    expect(await vault.saveForApp(FILE, enc('sonra gelen'), first.sig)).toEqual({ ok: false, why: 'changed' });
    expect(srv.generation, 'nothing committed').toBe(gen);
    expect(dec(await vault.readBytes(FILE))).toBe('önce gelen');
    expect(calls.toasts.at(-1)).toBe(en('e2e.vault.err.changed', { name: NAME }));
  });

  it('an app that saves what it did not read names the version by the row: its size and time', async () => {
    const { vault } = await withFile();
    const [row] = vault.rows(`${ROOT}/Bütçe`)!;
    const asListed = `${row.size}:${row.last_modified}`;
    expect((await vault.saveForApp(FILE, enc('v2'), asListed)).ok).toBe(true);
    expect(await vault.saveForApp(FILE, enc('v3'), asListed)).toEqual({ ok: false, why: 'changed' });
  });

  it('a file that is gone is not made again', async () => {
    const { vault, srv } = await withFile();
    const r = await readIt(vault);
    expect(await vault.remove([FILE])).toBe(true);
    const gen = srv.generation;
    expect(await vault.saveForApp(FILE, enc('geri gelmesin'), r.sig)).toEqual({ ok: false, why: 'gone' });
    expect(srv.generation).toBe(gen);
    expect(vault.rows(`${ROOT}/Bütçe`)).toEqual([]);
    expect(await vault.readForApp(FILE, 1 << 20)).toEqual({ ok: false, why: 'gone' });
  });

  it("somebody else writes the vault: held, nothing written, said in the server's words", async () => {
    const { vault, srv, clock, calls } = await withFile();
    const r = await readIt(vault);
    // The upload's write lock ends (idle) and another session takes it.
    srv.lock = null;
    srv.ended = 'idle';
    await clock.advance(15_000);
    expect(vault.strips[ROOT].mode).toBe('read');
    srv.lock = { token: 'theirs', holder: { name: 'Ayşe', client: 'desktop', label: 'filex, macOS' }, since: '2026-10-10T09:00:00Z', mine: false };
    const gen = srv.generation;
    expect(await vault.saveForApp(FILE, enc('yazılmayacak'), r.sig)).toEqual({ ok: false, why: 'held' });
    expect(srv.generation).toBe(gen);
    expect(calls.toasts.at(-1)).toBe('Ayşe is writing this vault. It stays readable; try again when they are done.');
    expect(vault.strips[ROOT].holder?.name).toBe('Ayşe');
  });

  it("the write lock lost before the commit: nothing written, and the person told in the server's words", async () => {
    const { vault, srv, calls } = await withFile();
    const r = await readIt(vault);
    srv.failIndexWith = 'VAULT_LOCK_LOST';
    srv.failIndexSays = 'Somebody else took over writing, so the vault went back to read-only.';
    const gen = srv.generation;
    expect(await vault.saveForApp(FILE, enc('kaybolacak'), r.sig)).toEqual({ ok: false, why: 'lock_lost' });
    expect(srv.generation, 'nothing committed').toBe(gen);
    expect(vault.strips[ROOT].unsaved).toEqual([NAME]);
    expect(vault.strips[ROOT].mode).toBe('read');
    expect(calls.toasts.at(-1)).toBe(`Somebody else took over writing, so the vault went back to read-only. ${en('e2e.vault.unsaved', { names: NAME })}`);
    srv.failIndexWith = '';
    expect(dec(await vault.readBytes(FILE))).toBe(BODY);
  });

  it('the vault locked meanwhile: nothing handed over, nothing taken', async () => {
    const { vault, srv } = await withFile();
    const r = await readIt(vault);
    vault.lock(ROOT);
    await settle();
    const gen = srv.generation;
    expect(await vault.readForApp(FILE, 1 << 20)).toEqual({ ok: false, why: 'locked' });
    expect(await vault.saveForApp(FILE, enc('kilitliyken'), r.sig)).toEqual({ ok: false, why: 'locked' });
    expect(srv.generation).toBe(gen);
  });

  it('a file larger than the app may be handed is not fetched', async () => {
    const { vault, srv } = await withFile();
    const before = srv.seen.length;
    expect(await vault.readForApp(FILE, 4)).toEqual({ ok: false, why: 'too_large' });
    expect(srv.seen.slice(before).filter((u) => u.includes('action=download')), 'no pack read').toEqual([]);
  });

  it("an app's read is activity: the vault's own 15-minute lock waits for it", async () => {
    const { vault, srv, clock } = await withFile();
    // The upload's write lock ends; from then the vault's clock runs.
    srv.lock = null;
    srv.ended = 'idle';
    await clock.advance(15_000);
    await clock.advance(VAULT_IDLE_LOCK_MS - 60_000);
    expect(vault.isOpen(ROOT)).toBe(true);
    await readIt(vault);
    await clock.advance(VAULT_IDLE_LOCK_MS - 60_000);
    expect(vault.isOpen(ROOT), 'the read reset the clock').toBe(true);
    await clock.advance(120_000);
    expect(vault.isOpen(ROOT)).toBe(false);
  });
});

// filex 0.56 (task #189): a minute before the vault locks itself, the
// explorer is told - with the server's words from `state` - so it can warn
// about an app's unsaved changes and keep the vault open. Red before 0.56: no
// onLockSoon, no words.
describe('the vault says, a minute before, that it locks itself (filex 0.56)', () => {
  it("tells the explorer a minute before, in the server's words; keeping it open puts the lock off", async () => {
    const soon: Array<[string, unknown]> = [];
    const { vault, clock, calls } = await setup({ onLockSoon: (root, words) => void soon.push([root, words]) });
    await clock.advance(VAULT_IDLE_LOCK_MS - 61_000);
    expect(soon).toEqual([]);
    await clock.advance(1000);
    expect(soon).toEqual([[ROOT, SRV_WORDS]]);
    expect(vault.isOpen(ROOT)).toBe(true);
    // The warning's action: activity in the vault.
    vault.touch(ROOT);
    await clock.advance(61_000);
    expect(vault.isOpen(ROOT), 'kept open').toBe(true);
    expect(calls.locked).toEqual([]);
  });

  it('not while this session holds the write lock (the vault does not lock then)', async () => {
    const soon: unknown[] = [];
    const { vault, clock } = await setup({ onLockSoon: (root) => void soon.push(root) });
    await vault.mkdir(ROOT, 'A');
    await clock.advance(VAULT_IDLE_LOCK_MS);
    expect(soon).toEqual([]);
  });
});
