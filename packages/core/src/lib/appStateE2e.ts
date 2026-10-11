/**
 * appStateE2e - an app's own small store (`state.get` / `state.set`) while
 * its interface holds a file of an end-to-end encrypted folder in the clear
 * (filex 0.56, task #189; docs/E2E-ENCRYPTION.md → Editing in an app, and
 * docs/APP-PLUGINS-API.md → state).
 *
 * An interface keeps what it remembers in the person's account preferences
 * (lib/appState), which the SERVER stores. In an encrypted session that would
 * be a way for plaintext to reach the server as "settings" - a recent search,
 * a paragraph, a file name. So there it gets ANOTHER store, sealed in this
 * tab: the server keeps it and cannot read it, and it never mixes with the
 * app's ordinary one ("an encrypted session's settings are not a normal
 * session's", the maintainers' decision, 2026-10-09).
 *
 * ── Where ───────────────────────────────────────────────────────────────
 *
 *   the account document's `appStateE2e` (lib/prefs): a JSON object
 *   `{app: {ledger id: sealed}}` - one ledger per app and encrypted folder.
 *
 * ── Keys (per account, per app, per folder; nothing leaves the tab) ─────
 *
 *   account  the account this session is signed in as, as the session knows
 *            it (`GET /api/auth/me` → `user.id`, asked with the credential
 *            that reads and writes this very document - lib/prefs
 *            accountFetch), written `u<id>`. A folder key is SHARED by
 *            everybody who may open the folder, so without it the server
 *            could copy one member's sealed ledger into another member's
 *            preferences and it would open there. Where the host keeps no
 *            account document (lib/prefs not configured) nothing reaches a
 *            server: `local`. Not known (no session, the answer did not
 *            come): there is no sealed store - `appStateLedger` answers
 *            null, the app reads an empty store and cannot write one.
 *   root     the folder key as an HKDF key: a vault's IS one (lib/e2evault);
 *            for levels 1 and 2 (an AES-256-GCM key that cannot be exported)
 *            the 32 bytes AES-256 under it gives a fixed, labelled block
 *            (GCM of 32 zero bytes, IV "filex-state1") - a pseudorandom
 *            function of the folder key, never stored, never sent
 *   key      HKDF-SHA256(root, salt "filex-app-state",
 *                        info "filex-app-state-v1-key" 0x00 <account> 0x00 <app>)
 *            → AES-256-GCM
 *   id       HKDF-SHA256(root, same salt,
 *                        info "filex-app-state-v1-id" 0x00 <account> 0x00 <app>),
 *            16 bytes, base64url: names the ledger in the store and says
 *            nothing of the folder or the account
 *
 * ── A sealed ledger ─────────────────────────────────────────────────────
 *
 *   base64url( 0x01 ‖ IV (12, random) ‖ AES-256-GCM(JSON {key: value}) )
 *   associated data "filex-app-state-v1" 0x00 <account> 0x00 <app> 0x00 <id>:
 *   a ledger moved to another account's, another app's or another folder's
 *   place does not open.
 *
 * ── Rules ───────────────────────────────────────────────────────────────
 *
 *   - an entry is held to the ordinary rules (lib/appState stateEntry: a key
 *     of 1-128 characters, a value of at most 8 KiB as JSON);
 *   - the app's share (`limits.app_state_max_bytes`, 16 KiB) is measured on
 *     what is STORED - the sealed ledgers, encryption and base64 included -
 *     the number the server measures (handlers/userprefs.go), so an app keeps
 *     a little less here (about 12 KiB of JSON) and hears "too large" at once;
 *   - a ledger that is not there, or does not open (another key, damage, a
 *     newer format), reads as EMPTY - no error: the app starts afresh;
 *   - mid re-key the previous key's ledger is read until the first write,
 *     which seals under the new key and drops the old one.
 */
import { appStateMax, AppStateTooLarge, jsonBytes, stateEntry } from './appState';
import { accountFetch, currentPrefs, prefsConfigured, savePref } from './prefs';
import { b64urlDecode, b64urlEncode } from './e2enames';

const enc = new TextEncoder();
const dec = new TextDecoder();

/** A copy as the view WebCrypto takes everywhere. */
function bytes(b: Uint8Array): Uint8Array<ArrayBuffer> {
  return new Uint8Array(b);
}

const ROOT_IV = bytes(enc.encode('filex-state1')); // 12 bytes
const HKDF_SALT = bytes(enc.encode('filex-app-state'));
const INFO_KEY = 'filex-app-state-v1-key';
const INFO_ID = 'filex-app-state-v1-id';
const AAD_LABEL = 'filex-app-state-v1';
const SEALED_VERSION = 1;
const IV_BYTES = 12;
const TAG_BYTES = 16;
const ID_BYTES = 16;

/** One ledger's key, the id it is stored under, and the account both are
 *  bound to. */
export interface AppStateLedgerKey {
  id: string;
  key: CryptoKey;
  account: string;
}

/** The account label where no account document is kept (nothing is sent). */
const LOCAL_ACCOUNT = 'local';

/**
 * The account this session is signed in as, as the session knows it (see the
 * header): `u<id>`, `local` where the host keeps no account document, null
 * when it is not known.
 */
async function sessionAccount(): Promise<string | null> {
  if (!prefsConfigured()) return LOCAL_ACCOUNT;
  const res = await accountFetch('/api/auth/me', { method: 'GET' });
  if (!res || !res.ok) return null;
  try {
    const id = ((await res.json()) as { user?: { id?: unknown } | null } | null)?.user?.id;
    if (typeof id === 'number' && Number.isSafeInteger(id) && id > 0) return `u${id}`;
    if (typeof id === 'string' && /^[A-Za-z0-9_-]{1,64}$/.test(id)) return `u${id}`;
  } catch {
    /* not an answer */
  }
  return null;
}

/** The ledger an encrypted session reads and writes, and - while its folder
 *  is mid re-key - the previous key's, read until the first write. */
export interface AppStateLedger extends AppStateLedgerKey {
  previous?: AppStateLedgerKey | null;
}

/** The folder key as the HKDF key the ledger keys come from. */
async function rootOf(fmk: CryptoKey): Promise<CryptoKey> {
  if (fmk.algorithm?.name === 'HKDF') return fmk;
  const block = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: ROOT_IV }, fmk, new Uint8Array(32)));
  try {
    return await crypto.subtle.importKey('raw', block.subarray(0, 32), 'HKDF', false, ['deriveKey', 'deriveBits']);
  } finally {
    block.fill(0);
  }
}

function hkdf(label: string, account: string, app: string): HkdfParams {
  return { name: 'HKDF', hash: 'SHA-256', salt: HKDF_SALT, info: bytes(enc.encode(`${label}\u0000${account}\u0000${app}`)) };
}

/**
 * Per folder key, while it lives: the session's account (asked once - a
 * folder key is the ring's for one unlock, and signing out locks the ring)
 * and the ledger keys derived from it, per app. An account that could not be
 * learned is asked again next time.
 */
interface Derived {
  account: Promise<string | null>;
  byApp: Map<string, Promise<AppStateLedgerKey>>;
}
const derived = new WeakMap<CryptoKey, Derived>();

function derivedFor(fmk: CryptoKey): Derived {
  let d = derived.get(fmk);
  if (!d) {
    const fresh: Derived = { account: sessionAccount().catch(() => null), byApp: new Map() };
    void fresh.account.then((a) => {
      if (a === null && derived.get(fmk) === fresh) derived.delete(fmk);
    });
    derived.set(fmk, fresh);
    d = fresh;
  }
  return d;
}

function ledgerKeyOf(fmk: CryptoKey, account: string, app: string): Promise<AppStateLedgerKey> {
  const { byApp } = derivedFor(fmk);
  const slot = `${account}\u0000${app}`;
  let got = byApp.get(slot);
  if (!got) {
    got = (async () => {
      const root = await rootOf(fmk);
      const key = await crypto.subtle.deriveKey(hkdf(INFO_KEY, account, app), root, { name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
      const id = new Uint8Array(await crypto.subtle.deriveBits(hkdf(INFO_ID, account, app), root, ID_BYTES * 8));
      return { id: b64urlEncode(id), key, account };
    })();
    byApp.set(slot, got);
    got.catch(() => byApp.delete(slot));
  }
  return got;
}

/**
 * The ledger of `app` for the encrypted folder whose key is `fmk` (and its
 * previous key, mid re-key), bound to the account this session is signed in
 * as - what lib/appPlaintext's door answers for a file of that folder. null
 * when the account is not known: no sealed store (an empty one to read).
 */
export async function appStateLedger(fmk: CryptoKey, app: string, previous?: CryptoKey | null): Promise<AppStateLedger | null> {
  const account = await derivedFor(fmk).account;
  if (account === null) return null;
  const now = await ledgerKeyOf(fmk, account, app);
  return previous ? { ...now, previous: await ledgerKeyOf(previous, account, app) } : now;
}

type SealedStore = Record<string, Record<string, unknown>>;

function readStore(): SealedStore {
  const raw = currentPrefs().appStateE2e;
  if (!raw) return {};
  try {
    const v = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as SealedStore) : {};
  } catch {
    return {};
  }
}

/** One app's ledgers, as a copy this page may change. */
function ledgersOf(store: SealedStore, app: string): Record<string, unknown> {
  const mine = store[app];
  return mine && typeof mine === 'object' && !Array.isArray(mine) ? { ...mine } : {};
}

function aad(app: string, lk: AppStateLedgerKey): Uint8Array<ArrayBuffer> {
  return bytes(enc.encode(`${AAD_LABEL}\u0000${lk.account}\u0000${app}\u0000${lk.id}`));
}

async function seal(app: string, lk: AppStateLedgerKey, entries: Record<string, unknown>): Promise<string> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES));
  const plain = bytes(enc.encode(JSON.stringify(entries)));
  try {
    const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: aad(app, lk) }, lk.key, plain));
    const out = new Uint8Array(1 + IV_BYTES + ct.length);
    out[0] = SEALED_VERSION;
    out.set(iv, 1);
    out.set(ct, 1 + IV_BYTES);
    return b64urlEncode(out);
  } finally {
    plain.fill(0);
  }
}

/** A sealed ledger's entries; null when it does not open (another key,
 *  damage, a format this build does not write). */
async function unseal(app: string, lk: AppStateLedgerKey, sealed: unknown): Promise<Record<string, unknown> | null> {
  if (typeof sealed !== 'string') return null;
  const b = b64urlDecode(sealed);
  if (!b || b.length < 1 + IV_BYTES + TAG_BYTES || b[0] !== SEALED_VERSION) return null;
  let plain: Uint8Array;
  try {
    plain = new Uint8Array(
      await crypto.subtle.decrypt(
        { name: 'AES-GCM', iv: bytes(b.subarray(1, 1 + IV_BYTES)), additionalData: aad(app, lk) },
        lk.key,
        bytes(b.subarray(1 + IV_BYTES)),
      ),
    );
  } catch {
    return null;
  }
  try {
    const v = JSON.parse(dec.decode(plain));
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  } finally {
    plain.fill(0);
  }
}

/** The ledger's entries: under its key, else (mid re-key) the previous
 *  key's; empty when there is none or it does not open. */
async function entriesOf(app: string, ledger: AppStateLedger): Promise<Record<string, unknown>> {
  const mine = ledgersOf(readStore(), app);
  if (mine[ledger.id] !== undefined) return (await unseal(app, ledger, mine[ledger.id])) ?? {};
  const prev = ledger.previous;
  if (prev && mine[prev.id] !== undefined) return (await unseal(app, prev, mine[prev.id])) ?? {};
  return {};
}

/** The value `app` kept under `key` in this ledger; undefined when none. */
export async function sealedStateGet(app: string, ledger: AppStateLedger, key: string): Promise<unknown> {
  const entries = await entriesOf(app, ledger);
  return Object.prototype.hasOwnProperty.call(entries, key) ? entries[key] : undefined;
}

/** One write at a time per app: a write reads the ledger, changes it and
 *  seals it again, so two at once would lose one. */
const writing = new Map<string, Promise<void>>();

/**
 * Keep `value` (JSON) under `key` in this ledger; undefined removes it.
 * Throws AppStateTooLarge for an entry the rules refuse and for an app whose
 * sealed ledgers would be over its share - nothing is written then.
 */
export async function sealedStateSet(app: string, ledger: AppStateLedger, key: string, value: unknown): Promise<void> {
  const encoded = stateEntry(key, value);
  const before = writing.get(app) ?? Promise.resolve();
  const run = before.catch(() => undefined).then(() => write(app, ledger, key, encoded));
  writing.set(app, run);
  try {
    await run;
  } finally {
    if (writing.get(app) === run) writing.delete(app);
  }
}

async function write(app: string, ledger: AppStateLedger, key: string, encoded: string | undefined): Promise<void> {
  const entries = { ...(await entriesOf(app, ledger)) };
  if (encoded === undefined) delete entries[key];
  else entries[key] = JSON.parse(encoded);
  const mine = ledgersOf(readStore(), app);
  if (ledger.previous) delete mine[ledger.previous.id];
  if (Object.keys(entries).length) mine[ledger.id] = await seal(app, ledger, entries);
  else delete mine[ledger.id];
  const max = appStateMax();
  if (max !== undefined && jsonBytes(JSON.stringify(mine)) > max) {
    throw new AppStateTooLarge(`an app keeps at most ${max} bytes, sealed, in an encrypted folder`);
  }
  const next: SealedStore = { ...readStore(), [app]: mine };
  if (!Object.keys(mine).length) delete next[app];
  savePref('appStateE2e', JSON.stringify(next));
}
