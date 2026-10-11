/**
 * coeditOutbox - the changes of an editing session that have not reached
 * the relay yet (task #189, filex 0.56), kept in this browser so a lost
 * connection, a closed tab or a crash loses none of them.
 *
 * lib/coeditSession puts every `changes` entry an app appends here before it
 * sends it, and takes it out once the relay placed it. What is left when the
 * session is joined again - after the connection came back, or the page was
 * opened again - is sent then: at once when nobody changed the document
 * since, after the person's yes when somebody did or the session ended (the
 * question's words are the server's).
 *
 * Where: IndexedDB, this browser's own (`filex-coedit`), one record per
 * change, found by the file's path and taken only for the same file (`doc`,
 * the server's session key of it) and the same person (`owner`, an opaque
 * id the server hands this page). A change of an end-to-end
 * encrypted folder is kept SEALED with the folder key (AES-256-GCM, its
 * authenticated data naming the file): it is the file's plaintext, and it
 * never sits in the browser's storage in the clear. A browser without
 * IndexedDB (a private window may refuse it) keeps them in memory: a lost
 * connection still loses nothing, a closed tab then does.
 *
 * One tab's changes are that tab's to send while it is open: each record
 * names the member that made it, and the page holds a lock of that member's
 * name for as long as it is in the session (the Web Locks API, released by
 * the browser when the tab closes or crashes). Another tab of the same
 * person opening the same file leaves a record whose member is still held
 * alone - it would be sent twice otherwise.
 */
import { b64ToBytes, bytesToB64 } from './e2ecrypto';

/** One change waiting for the relay. */
export interface OutboxRecord {
  /** Assigned by the store. */
  id?: number;
  /** The file's session key (the server's `doc`). */
  doc: string;
  /** Who made it (the server's opaque `owner`): nobody else is offered it. */
  owner: string;
  /** The session it was made in. */
  sid: string;
  /** The member that made it: while that member's page is open (it holds
   *  the member's lock), the record is that page's to send. */
  client: string;
  /** The file's path as the host named it: how the next opening finds the
   *  records before it knows `doc` and `owner` (it checks them after). */
  path: string;
  /** The last `changes` entry the page had read when it made it. */
  changesBase: number;
  /** When, Unix ms (this browser's clock). */
  at: number;
  /** The change's body: JSON, or (sealed) base64 of IV || AES-GCM. */
  body: string;
  sealed: boolean;
}

/** Where the records are kept. */
export interface OutboxStore {
  add(r: OutboxRecord): Promise<number>;
  remove(id: number): Promise<void>;
  /** The records kept under a path, oldest first. */
  listPath(path: string): Promise<OutboxRecord[]>;
  /** Hold the member's lock while this page is in the session; the call
   *  that releases it. */
  hold(client: string): () => void;
  /** Whether a page that is open holds the member's lock. */
  live(client: string): Promise<boolean>;
}

/** In memory: tests, and a browser that refuses IndexedDB. */
export function memoryOutbox(): OutboxStore {
  const rows = new Map<number, OutboxRecord>();
  const held = new Map<string, number>();
  let seq = 0;
  return {
    async add(r) {
      const id = ++seq;
      rows.set(id, { ...r, id });
      return id;
    },
    async remove(id) {
      rows.delete(id);
    },
    async listPath(path) {
      return [...rows.values()].filter((r) => r.path === path).sort((a, b) => (a.id ?? 0) - (b.id ?? 0));
    },
    hold(client) {
      held.set(client, (held.get(client) ?? 0) + 1);
      let done = false;
      return () => {
        if (done) return;
        done = true;
        const n = (held.get(client) ?? 1) - 1;
        if (n > 0) held.set(client, n);
        else held.delete(client);
      };
    },
    async live(client) {
      return held.has(client);
    },
  };
}

const DB_NAME = 'filex-coedit';
const STORE = 'outbox';
const LOCK_PREFIX = 'filex-coedit-member:';

/** The browser's lock manager; undefined where there is none. */
function lockManager(): LockManager | undefined {
  try {
    return typeof navigator !== 'undefined' && navigator.locks ? navigator.locks : undefined;
  } catch {
    return undefined;
  }
}

function request<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

/**
 * This browser's IndexedDB, falling back to memory when it is not there or
 * refuses (a private window, a blocked origin) - the session goes on either
 * way.
 */
export function browserOutbox(): OutboxStore {
  const fallback = memoryOutbox();
  // This page's own members, known without asking the lock manager.
  const mine = memoryOutbox();
  let opening: Promise<IDBDatabase | null> | null = null;
  const db = (): Promise<IDBDatabase | null> => {
    if (!opening) {
      opening = new Promise((resolve) => {
        try {
          if (typeof indexedDB === 'undefined') return resolve(null);
          const req = indexedDB.open(DB_NAME, 1);
          req.onupgradeneeded = () => {
            const s = req.result.createObjectStore(STORE, { keyPath: 'id', autoIncrement: true });
            s.createIndex('by_path', 'path');
          };
          req.onsuccess = () => {
            // A database of this name without the store (made by something
            // else): memory, rather than a failure on every change.
            const d = req.result;
            if (d.objectStoreNames.contains(STORE)) return resolve(d);
            d.close();
            resolve(null);
          };
          req.onerror = () => resolve(null);
          req.onblocked = () => resolve(null);
        } catch {
          resolve(null);
        }
      });
    }
    return opening;
  };
  return {
    async add(r) {
      const d = await db();
      if (!d) return fallback.add(r);
      const tx = d.transaction(STORE, 'readwrite');
      const { id: _drop, ...row } = r;
      void _drop;
      return Number(await request(tx.objectStore(STORE).add(row)));
    },
    async remove(id) {
      const d = await db();
      if (!d) return fallback.remove(id);
      await request(d.transaction(STORE, 'readwrite').objectStore(STORE).delete(id));
    },
    async listPath(path) {
      const d = await db();
      if (!d) return fallback.listPath(path);
      const rows = (await request(d.transaction(STORE, 'readonly').objectStore(STORE).index('by_path').getAll(path))) as OutboxRecord[];
      return rows.sort((a, b) => (a.id ?? 0) - (b.id ?? 0));
    },
    hold(client) {
      const here = mine.hold(client);
      const locks = lockManager();
      if (!locks || !client) return here;
      let release: () => void = () => {};
      const done = new Promise<void>((resolve) => (release = resolve));
      locks.request(LOCK_PREFIX + client, () => done).catch(() => undefined);
      return () => {
        here();
        release();
      };
    },
    async live(client) {
      if (!client) return false;
      if (await mine.live(client)) return true;
      const locks = lockManager();
      if (!locks) return false;
      try {
        // Taken at once when nobody holds it (then released again): the
        // member's page is gone. Not available: a page that is open has it.
        return await locks.request(LOCK_PREFIX + client, { ifAvailable: true }, (lock) => lock === null);
      } catch {
        return false;
      }
    },
  };
}

/* ── sealing with the folder key (an encrypted folder's change) ─────────── */

const IV_LEN = 12;
const LABEL = 'filex-coedit-outbox-v1';
const enc = new TextEncoder();

function aadOf(doc: string): ArrayBuffer {
  return new Uint8Array(enc.encode(JSON.stringify([LABEL, doc]))).buffer as ArrayBuffer;
}

/** A change's body sealed with the folder key, for the browser's storage. */
export async function sealOutboxBody(fmk: CryptoKey, doc: string, body: unknown): Promise<string> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const plain = enc.encode(JSON.stringify(body));
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: aadOf(doc) }, fmk, plain));
  plain.fill(0);
  const out = new Uint8Array(IV_LEN + ct.length);
  out.set(iv, 0);
  out.set(ct, IV_LEN);
  return bytesToB64(out);
}

/** The body again; undefined when it does not open (another key, another file, damaged). */
export async function openOutboxBody(keys: CryptoKey[], doc: string, sealed: string): Promise<unknown> {
  let bytes: Uint8Array;
  try {
    bytes = b64ToBytes(sealed);
  } catch {
    return undefined;
  }
  if (bytes.length <= IV_LEN) return undefined;
  for (const k of keys) {
    try {
      const plain = await crypto.subtle.decrypt(
        { name: 'AES-GCM', iv: bytes.slice(0, IV_LEN), additionalData: aadOf(doc) },
        k,
        bytes.slice(IV_LEN),
      );
      return JSON.parse(new TextDecoder().decode(plain));
    } catch {
      /* the next key (a folder mid re-key) */
    }
  }
  return undefined;
}
