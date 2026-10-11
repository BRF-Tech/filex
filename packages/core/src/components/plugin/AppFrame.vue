<script setup lang="ts">
/**
 * AppFrame — the ONE frame an app's own interface runs in, wherever it is
 * opened: a dialog (`modal`), a tab of its own (`page`), a section of the
 * details panel (`inspector`), the app's home screen (`home`), or a file's
 * viewer (`viewer`). The web app, the desktop app and every embed draw this
 * same component (docs/APP-PLUGINS-API.md → An app's own interface).
 *
 * ⚠⚠ What makes the frame a sandbox, each rule measured in Chrome, Firefox
 * and WebKit before it was written (design report, 2026-09-27):
 *
 *  - The element is built by hand with `sandbox="allow-scripts"` set BEFORE
 *    `src` and before it is in the document. A sandbox added afterwards
 *    applies from the NEXT navigation only: the first document runs with
 *    filex's own origin and reads `sessionStorage['filex.bearer']` (lesson
 *    #635). A template `<iframe :src sandbox>` leaves that order to the
 *    renderer, so it is not used.
 *  - No `allow-same-origin`, ever: with it, an interface served from filex's
 *    own origin IS filex (measured: the bearer read in all three).
 *  - `referrerpolicy="no-referrer"` and no `allow` attribute: no camera, no
 *    microphone, no clipboard, no fullscreen for the frame itself.
 *  - The bridge (lib/appBridge) takes the interface's hello only from THIS
 *    element's window, answers with a MessagePort once, and closes it when
 *    the frame loads a second document. Reloading = a new element.
 *
 * A newer version approved while the frame is open (realtime `app.updated`,
 * lib/appUpdates) is said above the frame with "Reload" — which asks about
 * unsaved changes first — and told to the interface (`app.updated`). The
 * first opening after an approval says "X was updated to 1.3.0" once per
 * person (account preferences, lib/appState).
 *
 * Every call the interface makes is decided here: what it may read and save
 * (the files it was opened with, the grants it holds, read-only), what it may
 * ask of the module. The server's policy gives the interface no connection
 * of its own (`connect-src 'none'`), no storage and no cookie — ⚠ not a
 * promise that it cannot send anything out: WebRTC ignores a page's policy
 * (closed by Connection-Allowlist in Chrome, by the bootstrap in Firefox;
 * docs/APP-PLUGINS.md → What a sandbox cannot promise).
 *
 * `ui.print` (0.55, task #189): a sandboxed frame may not open the print
 * dialog, so the interface hands over a PDF and filex prints it from the
 * server's print page (lib/printPdf) — never from a frame of this page,
 * whose policy frames no `blob:` (backend internal/secheaders). filex asks
 * every time, and the row's Allow IS the print page's own button (a frame of
 * it in the row): only a click in that page counts as the person's there
 * (security review sec055 S9).
 *
 * A file of an end-to-end encrypted folder (filex 0.56, task #189) is
 * handed to an app granted `files:e2e-plaintext` IN THE CLEAR when the host
 * gives this frame the folder's door (`e2e`, lib/appPlaintext): `file.read`
 * fetches the ciphertext through the server's app door (`e2e=1`), decrypts
 * it in this tab and TRANSFERS the plaintext to the interface (this page
 * keeps no copy, and no `blob:` address is made of it); `file.save`
 * encrypts what the interface hands over in this tab and sends only the
 * ciphertext, over the file as it was opened (`expect`). While the frame
 * holds such a file, nothing leaves it toward the server but that encrypted
 * save: `file.saveAs`, `engine.call` and `job.submit` answer `unavailable`,
 * and `state.get` / `state.set` use the app's ENCRYPTED store for that
 * folder (lib/appStateE2e, keys derived from the folder key through the
 * door), never its ordinary one (lib/appState) - the two do not mix.
 *
 * Editing together (`coedit.*`, filex 0.56, task #189): an app granted
 * `files:co-edit` joins the file's session through the server's relay, and
 * THIS page holds the session's key, seals what the app appends, reads the
 * whole log through the reader that refuses a log the writers did not write
 * and hands the app the entries (lib/coeditSession). The app never holds a
 * key and never touches the network; closing the frame leaves the session.
 * In an encrypted folder the session's key is sealed with the folder key
 * here (the door's `folderKeys`); the changes not placed yet are kept in
 * this browser (lib/coeditOutbox) and the question about an earlier
 * opening's is asked above the frame in the server's words.
 *
 * ⚠ `file.saveAs` asks the person for a folder in filex's own dialog — the
 * one Move to… / Copy to… use (modals/DestinationPickerModal) — and THIS
 * frame draws it, so every placement in every host offers save-as the same
 * way. It used to be a prop no host passed: save-as answered `unavailable`
 * everywhere while the documentation promised the dialog (task #149).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  BRIDGE_VERSION,
  LIMITS,
  type CoEditAppendParams,
  type CoEditBlobGetParams,
  type CoEditBlobPutParams,
  type CoEditCursorParams,
  type CoEditJoinParams,
  type CoEditLeaseParams,
  type CoEditSubscribeParams,
  type ConfirmParams,
  type DownloadParams,
  type EngineCallParams,
  type FileInfo,
  type JobSubmitParams,
  type LicenseInfo,
  type PrintParams,
  type ReadParams,
  type SaveAsParams,
  type SaveParams,
  type Session,
  type Theme,
  type ToastParams,
} from '@brftech/filex-app-ui/protocol';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import type { PluginUIRef } from '../../types/Plugins';
import { useLocale } from '../../composables/useLocale';
import { useSystemDark } from '../../composables/useSystemDark';
import { localeDir } from '../../lib/direction';
import { createAppBridge, BridgeFailure, type AppBridge } from '../../lib/appBridge';
import { appStateGet, appStateSet, AppStateTooLarge, appSeenVersion, markAppSeen } from '../../lib/appState';
import { onAppUpdated } from '../../lib/appUpdates';
import { labelOf } from '../../lib/pluginLabel';
import { parentOfWire } from '../../lib/destinationTree';
import { isInternalPath, listingAddress } from '../../lib/internalPaths';
import { collectPdf, isPdf, lookOf, printPdf, PrintFailure } from '../../lib/printPdf';
import { isEncryptedKind, type EncryptedKind } from '../../lib/encryptedRow';
import { APP_PLAINTEXT_LIMIT, LedgerNeedsAccount, TooLargeToHand, VaultAppRefused, type AppPlaintextDoor } from '../../lib/appPlaintext';
import { sealedStateGet, sealedStateSet, type AppStateLedger } from '../../lib/appStateE2e';
import { E2E_PLAINTEXT_GRANT } from '../../lib/appViewer';
import { CoEditSession, type CoEditTransport } from '../../lib/coeditSession';
import { browserOutbox, type OutboxStore } from '../../lib/coeditOutbox';
import PluginConfirmModal from './PluginConfirmModal.vue';
import Modal from '../../modals/Modal.vue';
import DestinationPickerModal from '../../modals/DestinationPickerModal.vue';

/** A file the interface was opened with. `path` is adapter-qualified. */
export interface AppFrameFile {
  path: string;
  name: string;
  size?: number;
  mime?: string;
  /** Cannot be written (read-only storage, view-only opening). */
  readOnly?: boolean;
  /** End-to-end encrypted, as the server stamped the row (#189): handed to
   *  the interface as `FileInfo.encrypted`. Never saved over - unless it is
   *  handed over in the clear (`e2e`, filex 0.56). */
  encrypted?: EncryptedKind;
  /** Unix ms, as its listing said it: with `size`, the `expect` of a save
   *  over a file of an encrypted folder (filex 0.56). */
  modified?: number;
}

const props = defineProps<{
  api: FileApi;
  /** The app (plugin name) and the view the interface draws. */
  app: string;
  version?: string;
  view: string;
  placement: 'modal' | 'page' | 'inspector' | 'home' | 'viewer';
  ui: PluginUIRef;
  /** The files it was opened with (none for a home screen). */
  files?: AppFrameFile[];
  /**
   * Where `file.save` of the first file writes: the file itself, or — for a
   * draft (issue #71) — the draft the host is editing it as. Absent: the
   * file's own path.
   */
  savePath?: string;
  /** The whole opening is view-only: nothing may be saved over. */
  readOnly?: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** The person, as the interface may know them: a display name only. */
  userName?: string;
  /** The frame's accessible name (the view's label). */
  title?: string;
  /**
   * Storage names the save-as dialog may span (`file.saveAs`) — the host's
   * own list, the one its Move to… / Copy to… offer. Absent: the server's
   * listing names them.
   */
  storages?: string[];
  /**
   * Where the save-as dialog opens when the opened file's folder will not do
   * — no file (a home screen): the folder the person is in; a draft: the
   * folder it will be saved to. With an ordinary file, the dialog opens in
   * that file's folder.
   */
  startAt?: string;
  /**
   * filex 0.56 (task #189): the door that decrypts and encrypts the files of
   * an encrypted folder in this tab (lib/appPlaintext). Given by a host that
   * can hand them to this app in the clear (lib/appViewer `e2ePlaintext`);
   * absent, an encrypted file is refused to the interface as before.
   */
  e2e?: AppPlaintextDoor | null;
}>();

const emit = defineEmits<{
  (e: 'connected'): void;
  (e: 'dirty', dirty: boolean): void;
  (e: 'title', text: string): void;
  (e: 'toast', t: { text: string; tone: NonNullable<ToastParams['tone']> }): void;
  (e: 'close'): void;
  (e: 'saved', r: { path: string; size: number; index: number }): void;
  (e: 'saved-as', r: { path: string; name: string; size: number }): void;
  (e: 'op', op: Record<string, unknown>): void;
}>();

const { t } = useLocale(() => props.locale);
const systemDark = useSystemDark();
const host = ref<HTMLElement | null>(null);
let frame: HTMLIFrameElement | null = null;
let bridge: AppBridge | null = null;
const connected = ref(false);
/** The interface said it holds changes that are not saved (`ui.dirty`). */
const dirty = ref(false);
/** The unsaved-changes question, while it is on screen. */
const closeAsk = ref<((v: 'save' | 'discard' | 'keep') => void) | null>(null);
const closeBusy = ref(false);
/** Bumped to replace the element (a reload): one bridge per element. */
const generation = ref(0);
/** The interface's newer address after "Reload" on an approved version. */
const liveUI = ref<PluginUIRef | null>(null);
/** What is open: the newer interface once reloaded to it, else the one given. */
const current = computed<PluginUIRef>(() => liveUI.value ?? props.ui);
/** A newer version approved while this frame is open ("" = none). */
const newer = ref('');
/** "Updated to" said once per person, on the first opening after an approval. */
const updatedNote = ref('');

const src = computed(() => props.api.appUIUrl(current.value.url));
const grants = computed(() => new Set(current.value.grants ?? []));
const appVersion = computed(() => liveUI.value?.version || props.version || props.ui.version || '');
const mode = computed<'light' | 'dark'>(() =>
  props.theme === 'dark' || (props.theme !== 'light' && systemDark.value) ? 'dark' : 'light',
);

/** The `--fe-*` tokens the interface is handed, as the explorer resolves them here. */
const TOKENS = [
  '--fe-bg', '--fe-bg-elev', '--fe-bg-hover', '--fe-bg-selected',
  '--fe-border', '--fe-border-soft', '--fe-border-strong',
  '--fe-text', '--fe-text-muted', '--fe-text-on-primary',
  '--fe-primary', '--fe-primary-hover', '--fe-primary-soft', '--fe-primary-ink',
  '--fe-danger', '--fe-danger-hover', '--fe-warning', '--fe-ok',
  '--fe-radius', '--fe-radius-sm', '--fe-radius-md', '--fe-radius-lg',
  '--fe-font', '--fe-font-mono', '--fe-text-xs', '--fe-text-sm', '--fe-text-md',
  '--fe-shadow', '--fe-shadow-sm',
];

function themeInfo(): Theme {
  const tokens: Record<string, string> = {};
  if (host.value && typeof getComputedStyle === 'function') {
    const cs = getComputedStyle(host.value);
    for (const k of TOKENS) {
      const v = cs.getPropertyValue(k).trim();
      if (v) tokens[k] = v;
    }
  }
  return { mode: mode.value, tokens };
}

function extOf(name: string): string {
  const i = name.lastIndexOf('.');
  return i > 0 ? name.slice(i + 1).toLowerCase() : '';
}

/**
 * filex 0.56: this file is handed to the interface in the clear - a file of
 * an encrypted folder, or of a vault where the host gave the door's vault
 * half (never a `.fxe`), a host that gave the door, an app granted
 * `files:e2e-plaintext`.
 */
function plaintextOf(f: AppFrameFile): boolean {
  const door = props.e2e;
  if (!door || !grants.value.has(E2E_PLAINTEXT_GRANT)) return false;
  return f.encrypted === 'folder' || (f.encrypted === 'vault' && !!door.vault);
}

/** The frame holds plaintext of an encrypted folder: nothing but the
 *  encrypted save may leave it toward the server. */
const holdsPlaintext = computed(() => (props.files ?? []).some(plaintextOf));

/** Any other end-to-end encrypted file is saved over by no app: the server
 *  holds it only as ciphertext and refuses the app its bytes and its save
 *  (`403 encrypted`), so the interface is told it is read-only. */
function cannotSave(f: AppFrameFile): boolean {
  return !!props.readOnly || !!f.readOnly || (isEncryptedKind(f.encrypted) && !plaintextOf(f));
}

function fileInfos(): FileInfo[] {
  const writable = grants.value.has('files:write');
  return (props.files ?? []).map((f, index) => ({
    index,
    name: f.name,
    ext: extOf(f.name),
    size: f.size ?? 0,
    mime: f.mime ?? '',
    readOnly: !writable || cannotSave(f),
    // Only when the server said so: absent means not encrypted.
    ...(isEncryptedKind(f.encrypted) ? { encrypted: f.encrypted } : {}),
    ...(plaintextOf(f) ? { plaintext: true as const } : {}),
  }));
}

function session(): Session {
  return {
    v: BRIDGE_VERSION,
    app: { name: props.app, version: appVersion.value },
    view: { id: props.view, placement: props.placement },
    locale: props.locale,
    dir: localeDir(props.locale),
    theme: themeInfo(),
    user: { name: props.userName ?? '' },
    files: fileInfos(),
    grants: [...grants.value],
  };
}

function need(grant: string) {
  if (!grants.value.has(grant)) throw new BridgeFailure('not_granted', `this app was not granted ${grant}`);
}

function fileAt(index: unknown): AppFrameFile {
  const i = typeof index === 'number' ? index : 0;
  const f = (props.files ?? [])[i];
  if (!Number.isInteger(i) || !f) throw new BridgeFailure('not_found', `no file at ${String(index)}`);
  return f;
}

/**
 * What a save hands us, as a body the API sends: a string or bytes as a Blob,
 * a stream AS A STREAM — the API sends it in chunks as it is read, so a large
 * document never sits whole in this page.
 */
async function toBody(data: unknown, mime?: string): Promise<Blob | ReadableStream<Uint8Array>> {
  const type = typeof mime === 'string' && mime.length < 200 ? mime : '';
  if (typeof data === 'string') return new Blob([data], { type: type || 'text/plain;charset=utf-8' });
  if (data instanceof ArrayBuffer) return new Blob([data], { type });
  if (typeof ReadableStream !== 'undefined' && data instanceof ReadableStream) return data as ReadableStream<Uint8Array>;
  throw new BridgeFailure('invalid', 'save takes a string, an ArrayBuffer or a ReadableStream');
}

/* ── a file of an encrypted folder, in the clear (filex 0.56) ────────── */

/** Each encrypted file's size and date as the server last wrote it (the
 *  answer of a save): the `expect` of its next save. */
const savedSigs = new Map<number, { size: number; modified: number }>();

/** "Only over the file as it was opened": "<size>:<ms>", or nothing when
 *  the host did not say when it was last changed. */
function expectOf(index: number, f: AppFrameFile): string | undefined {
  const s = savedSigs.get(index);
  if (s) return `${s.size}:${s.modified}`;
  return typeof f.size === 'number' && typeof f.modified === 'number' ? `${f.size}:${f.modified}` : undefined;
}

function tooLargeToHand(): BridgeFailure {
  return new BridgeFailure('too_large', `at most ${APP_PLAINTEXT_LIMIT >> 20} MiB in an encrypted folder`);
}

function folderLocked(): BridgeFailure {
  return new BridgeFailure('unavailable', 'the encrypted folder was locked');
}

/**
 * filex 0.56 - the ledger of an ENCRYPTED SESSION (the frame holds a file of
 * an encrypted folder in the clear): this app's store for that folder,
 * sealed with keys derived from the folder key (lib/appStateE2e, through the
 * door). null: no such session, or the folder is locked now.
 */
async function sealedLedger(): Promise<AppStateLedger | null> {
  const f = (props.files ?? []).find(plaintextOf);
  if (!f || !props.e2e) return null;
  // A vault's file: the store sealed with the vault's own key (the door's
  // vault half, filex 0.56).
  if (f.encrypted === 'vault') return props.e2e.vault ? props.e2e.vault.ledger(f.path, props.app) : null;
  return props.e2e.ledger(f.path, props.app);
}

/** The sealed store is bound to the account this page is signed in as, and
 *  the page could not tell which (LedgerNeedsAccount): said as it is - not
 *  as a locked folder, which it is not. A literal key for the catalogue. */
function noAccountWords(): string {
  return t('appframe.state_no_account');
}

/** What the interface saves, whole, as bytes this page owns (zeroed by the
 *  caller once encrypted). */
async function plainBytes(data: unknown): Promise<Uint8Array<ArrayBuffer>> {
  const isStream = typeof ReadableStream !== 'undefined' && data instanceof ReadableStream;
  if (typeof data !== 'string' && !(data instanceof ArrayBuffer) && !isStream) {
    throw new BridgeFailure('invalid', 'save takes a string, an ArrayBuffer or a ReadableStream');
  }
  const parts: Uint8Array[] = [];
  let size = 0;
  try {
    for await (const chunk of chunksOf(data as string | ArrayBuffer | ReadableStream<Uint8Array>)) {
      size += chunk.byteLength;
      if (size > APP_PLAINTEXT_LIMIT) throw tooLargeToHand();
      parts.push(chunk);
    }
  } catch (e) {
    for (const p of parts) p.fill(0);
    throw e;
  }
  const out = new Uint8Array(size);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.byteLength;
    p.fill(0);
  }
  return out;
}

/** The plaintext of a file of an encrypted folder: the ciphertext through
 *  the server's door, decrypted here. null: the folder is locked now. */
async function folderRead(f: AppFrameFile): Promise<Uint8Array<ArrayBuffer> | null> {
  const door = props.e2e;
  const read = props.api.pluginUIRead;
  if (!door || typeof read !== 'function') throw new BridgeFailure('unavailable', 'this host does not hand over encrypted files');
  const res = await read(props.app, props.view, f.path, { e2e: true });
  const body = res.body ?? new Blob([await res.arrayBuffer()]).stream();
  try {
    return await door.open(f.path, body);
  } catch (e) {
    if (e instanceof TooLargeToHand) throw tooLargeToHand();
    throw new BridgeFailure('failed', 'the file could not be decrypted here');
  }
}

/* A VAULT's file (level 3, filex 0.56): the door's vault half
 * (lib/appPlaintext AppVaultDoor, the explorer's vault engine). The file is
 * read from the vault's packs and a save is the vault's next generation,
 * both in this tab - nothing goes through the server's app doors, which
 * refuse a vault anyway, and its path (the vault folder, then a path that
 * exists only in this tab) is never sent. A save takes the vault's write
 * lock like any change in it, and lands only over the version the
 * interface read. */

/** Each vault file's version as the interface last read or saved it, by
 *  path: the `expect` of its next save. */
const vaultSigs = new Map<string, string>();

/** A vault's refusal, as the interface is told it. The person was told by
 *  the vault (its toast, its strip); a lock closes the frame itself. */
function vaultFailure(e: unknown, failed: string): BridgeFailure {
  if (e instanceof BridgeFailure) return e;
  if (e instanceof TooLargeToHand) return tooLargeToHand();
  if (!(e instanceof VaultAppRefused)) return new BridgeFailure('failed', failed);
  switch (e.why) {
    case 'locked':
      return new BridgeFailure('unavailable', 'the vault was locked');
    case 'too_large':
      return tooLargeToHand();
    case 'gone':
      return new BridgeFailure('not_found', 'the file is not in the vault any more');
    case 'read_only':
      return new BridgeFailure('read_only', 'the vault is read-only here');
    case 'changed':
      return new BridgeFailure('failed', 'changed');
    case 'held':
      return new BridgeFailure('failed', 'vault_locked');
    case 'lock_lost':
      return new BridgeFailure('failed', 'vault_lock_lost');
    default:
      return new BridgeFailure('failed', failed);
  }
}

/** The plaintext of a vault's file, read from its packs here. null: the
 *  vault is not open any more. */
async function vaultRead(f: AppFrameFile): Promise<Uint8Array<ArrayBuffer> | null> {
  const door = props.e2e?.vault;
  if (!door) throw new BridgeFailure('unavailable', 'this host does not hand over files of a vault');
  try {
    const r = await door.read(f.path);
    if (!r) return null;
    vaultSigs.set(f.path, r.sig);
    return r.bytes;
  } catch (e) {
    throw vaultFailure(e, 'the file could not be decrypted here');
  }
}

/** `file.save` of a vault's file: the vault's next generation, written here
 *  under the vault's write lock, only over the version the interface read. */
async function saveToVault(index: number, f: AppFrameFile, data: unknown) {
  const door = props.e2e?.vault;
  if (!door) throw new BridgeFailure('unavailable', 'the vault was locked');
  const plain = await plainBytes(data);
  const size = plain.byteLength;
  let r: { size: number; sig: string };
  try {
    r = await door.save(f.path, plain, vaultSigs.get(f.path) ?? expectOf(index, f));
  } catch (e) {
    throw vaultFailure(e, 'the vault did not save it');
  } finally {
    plain.fill(0);
  }
  vaultSigs.set(f.path, r.sig);
  emit('saved', { path: f.path, size: r.size, index });
  dirty.value = false;
  emit('dirty', false);
  return { saved: true as const, size };
}

/** `file.read` of a file handed over in the clear: decrypted here (a folder's
 *  through the server's door, a vault's from its packs), transferred to the
 *  interface. */
async function readPlain(f: AppFrameFile, as: ReadParams['as']) {
  const plain = f.encrypted === 'vault' ? await vaultRead(f) : await folderRead(f);
  if (!plain) throw f.encrypted === 'vault' ? vaultFailure(new VaultAppRefused('locked'), '') : folderLocked();
  const out = { name: f.name, size: plain.byteLength, mime: f.mime || '' };
  switch (as ?? 'stream') {
    case 'text': {
      if (plain.byteLength > LIMITS.maxTextBytes) {
        plain.fill(0);
        throw new BridgeFailure('too_large', 'read a file this large as bytes');
      }
      const text = new TextDecoder().decode(plain);
      plain.fill(0);
      return { ...out, text };
    }
    case 'bytes':
      // Transferred (lib/appBridge): this page keeps no copy.
      return { ...out, bytes: plain.buffer };
    case 'stream': {
      const whole = plain;
      return {
        ...out,
        stream: new ReadableStream<Uint8Array>({
          start(c) {
            c.enqueue(whole);
            c.close();
          },
        }),
      };
    }
    default:
      plain.fill(0);
      throw new BridgeFailure('invalid', 'as is stream, bytes or text');
  }
}

/** `file.save` of a file handed over in the clear: encrypted here, then only
 *  the ciphertext goes to the server's door, over the file as it was opened. */
async function savePlain(index: number, f: AppFrameFile, data: unknown, through?: number) {
  if (f.encrypted === 'vault') return saveToVault(index, f, data);
  const door = props.e2e;
  if (!door) throw folderLocked();
  const plain = await plainBytes(data);
  const size = plain.byteLength;
  let ct: ArrayBuffer | null;
  try {
    ct = await door.seal(f.path, plain);
  } catch (e) {
    if (e instanceof TooLargeToHand) throw tooLargeToHand();
    throw e;
  } finally {
    plain.fill(0);
  }
  if (!ct) throw folderLocked();
  // Never a draft's path: a file of an encrypted folder is saved where it is.
  // In an editing session the session holds the file to what it holds
  // (another member's save between two of this page's is no change), and
  // records which part of its log this save holds.
  const together = coedit && typeof through === 'number' ? coedit.saveParams(through) : null;
  const r = await props.api.pluginUISave(props.app, props.view, { path: f.path }, new Blob([ct], { type: 'application/octet-stream' }), {
    e2e: true,
    expect: expectOf(index, f),
    ...(together ? { extra: together } : {}),
  });
  if (typeof r.modified === 'number') savedSigs.set(index, { size: r.size, modified: r.modified });
  emit('saved', { path: r.path || f.path, size: r.size, index });
  dirty.value = false;
  emit('dirty', false);
  return { saved: true as const, size };
}

/** While the frame holds plaintext, the module and a copy elsewhere are not
 *  offered: either would carry the file (or what the app took from it) out
 *  of the encrypted folder to the server. */
function refuseWhilePlain(what: string): void {
  if (holdsPlaintext.value) throw new BridgeFailure('unavailable', `${what} is not offered while a file of an encrypted folder is open`);
}

/* ── the bridge's methods: every decision is made here ─────────────────── */

const confirmAsk = ref<(ConfirmParams & { resolve: (v: boolean) => void }) | null>(null);

/**
 * Who is speaking, in the words filex puts around what an app says (a toast,
 * a question, a request for consent): the view's label, with the app's
 * installed name when the label does not already say it — a label cannot
 * pass itself off as filex (security review UI-14).
 */
const who = computed(() => {
  const label = (props.title || '').trim();
  if (!label) return props.app;
  return label.toLowerCase().includes(props.app.toLowerCase()) ? label : `${label} (${props.app})`;
});

/**
 * What only the PERSON may start (security review UI-5, UI-10, and a
 * download): a job, a copy to the clipboard. On a gesture — the person
 * clicked or typed in the frame a moment ago, which the browser tells this
 * page too (`navigator.userActivation`) — it goes ahead; one gesture stands
 * for ONE such call. Otherwise filex asks, above the frame and in its own
 * words, naming the app; "no" is `cancelled`.
 */
const consentAsk = ref<{ text: string; resolve: (v: boolean) => void } | null>(null);
/** A browser's transient activation lasts about this long (Chrome, Firefox). */
const GESTURE_MS = 5000;
let gestureSpentAt = Number.NEGATIVE_INFINITY;
/**
 * When the person last acted on filex's OWN page — a click, a key, a touch.
 * The browser tells this page about those and never about the frame's, and
 * the activation they give is NOT a gesture in the interface: measured
 * 2026-09-27 (Chrome, Firefox), the double-click that opened an app left the
 * page active long enough for the app to fill the clipboard at once, without
 * a question. So an activation within GESTURE_MS of one of filex's own
 * gestures does not count — and at mount, an activation already live is the
 * one that opened the app.
 */
let hostGestureAt = Number.NEGATIVE_INFINITY;

function activationLive(): boolean {
  const ua = typeof navigator !== 'undefined' ? (navigator as Navigator & { userActivation?: { isActive?: boolean } }).userActivation : undefined;
  return !!ua?.isActive;
}

function onHostGesture(): void {
  hostGestureAt = Date.now();
}

function gestureNow(): boolean {
  if (!activationLive()) return false;
  const now = Date.now();
  if (now - gestureSpentAt < GESTURE_MS || now - hostGestureAt < GESTURE_MS) return false;
  gestureSpentAt = now;
  return true;
}

async function consent(text: string): Promise<void> {
  if (gestureNow()) return;
  await askConsent(text);
}

/**
 * "Allow" answers only once the question has been on screen a moment. The
 * app decides WHEN it asks, so it can ask right under a click it invited
 * ("double-click here"): the second click would land on a button that
 * appeared beneath it. And the row pushes the frame down, which Chromium's
 * hit-testing follows a frame late (measured 2026-09-27: a click at once
 * went to the frame, not the row). So Allow is disabled for CONSENT_ARM_MS;
 * "Don't allow" never is.
 */
const CONSENT_ARM_MS = 600;
const consentArmed = ref(false);
let consentArmTimer: ReturnType<typeof setTimeout> | null = null;

async function askConsent(text: string): Promise<void> {
  if (consentAsk.value) throw new BridgeFailure('unavailable', 'a question is already on screen');
  consentArmed.value = false;
  if (consentArmTimer) clearTimeout(consentArmTimer);
  consentArmTimer = setTimeout(() => {
    consentArmed.value = true;
  }, CONSENT_ARM_MS);
  const yes = await new Promise<boolean>((resolve) => {
    consentAsk.value = { text, resolve };
  });
  if (!yes) throw new BridgeFailure('cancelled');
}

/* The consent row's sentences, one function each so the key stays a literal
 * the catalogue checks can find, and none is built once at setup. */
function clipboardQuestion(): string {
  return t('appframe.consent_clipboard', { app: who.value });
}
function jobQuestion(action: string): string {
  return t('appframe.consent_job', { app: who.value, action });
}
function downloadQuestion(name: string): string {
  return t('appframe.consent_download', { app: who.value, name });
}
function printQuestion(name: string): string {
  return t('appframe.consent_print', { app: who.value, name });
}

/** The print page's one button: filex's Allow, in the reader's language now. */
function printAllowLabel(): string {
  return t('appframe.consent_allow');
}

function answerConsent(yes: boolean) {
  if (yes && !consentArmed.value) return;
  const q = consentAsk.value;
  consentAsk.value = null;
  if (consentArmTimer) clearTimeout(consentArmTimer);
  consentArmTimer = null;
  q?.resolve(yes);
}

/* ── ui.download: a file for the person's own disk ──────────────────── */

/** A name for the person's disk: a leaf — nothing that is a path. */
function downloadName(v: unknown): string {
  const name = typeof v === 'string' ? v.trim() : '';
  if (!name || name.length > 255 || name === '.' || name === '..' || /[\\/\u0000-\u001f]/.test(name)) {
    throw new BridgeFailure('invalid', 'name is a file name, without a folder');
  }
  return name;
}

function tooLarge(): BridgeFailure {
  return new BridgeFailure('too_large', `at most ${LIMITS.maxDownloadBytes >> 20} MiB`);
}

/** The data as byte chunks, whatever it came as; a stream is read as it is
 *  used, and let go of (cancelled) when the reading stops early. */
async function* chunksOf(data: string | ArrayBuffer | ReadableStream<Uint8Array>): AsyncGenerator<Uint8Array> {
  if (typeof data === 'string') {
    yield new TextEncoder().encode(data);
    return;
  }
  if (data instanceof ArrayBuffer) {
    yield new Uint8Array(data);
    return;
  }
  const reader = data.getReader();
  let done = false;
  try {
    for (;;) {
      const r = await reader.read();
      if (r.done) {
        done = true;
        return;
      }
      if (r.value) yield r.value;
    }
  } finally {
    if (!done) await reader.cancel().catch(() => undefined);
  }
}

type SaveFilePicker = (o: { suggestedName: string }) => Promise<{
  createWritable(): Promise<{ write(c: Uint8Array): Promise<void>; close(): Promise<void>; abort?(): Promise<void> }>;
}>;

/**
 * Hand the file to the person's disk. Where the browser has File System
 * Access (Chromium) the bytes are streamed into the file the person picks —
 * never whole in this page; elsewhere (or where the picker is refused here: a
 * frame of another site) they become a Blob the browser downloads. Either
 * way nothing past LIMITS.maxDownloadBytes is written.
 */
async function saveToDisk(name: string, data: string | ArrayBuffer | ReadableStream<Uint8Array>, type: string) {
  const picker = (window as unknown as { showSaveFilePicker?: SaveFilePicker }).showSaveFilePicker;
  if (typeof picker === 'function') {
    let handle: Awaited<ReturnType<SaveFilePicker>> | null = null;
    try {
      handle = await picker.call(window, { suggestedName: name });
    } catch (e) {
      if ((e as DOMException)?.name === 'AbortError') throw new BridgeFailure('cancelled');
      handle = null; // not offered here: the Blob below
    }
    if (handle) {
      const w = await handle.createWritable();
      let size = 0;
      try {
        for await (const chunk of chunksOf(data)) {
          size += chunk.byteLength;
          if (size > LIMITS.maxDownloadBytes) throw tooLarge();
          await w.write(chunk);
        }
      } catch (e) {
        await w.abort?.().catch(() => undefined);
        throw e;
      }
      await w.close();
      return { saved: true as const, size };
    }
  }
  const parts: Uint8Array[] = [];
  let size = 0;
  for await (const chunk of chunksOf(data)) {
    size += chunk.byteLength;
    if (size > LIMITS.maxDownloadBytes) throw tooLarge();
    parts.push(chunk);
  }
  const url = URL.createObjectURL(new Blob(parts as BlobPart[], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.rel = 'noopener';
  a.style.display = 'none';
  document.body.appendChild(a);
  try {
    a.click();
  } finally {
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  }
  return { saved: true as const, size };
}

/* ── ui.print: a PDF for the browser's print dialog ─────────────────── */

/**
 * The print question row (security review sec055 S9). filex asks EVERY time:
 * the browser's print dialog opens only on the person's click on the row's
 * Allow, and that Allow is the print page's own button - a frame of
 * `/_print/` in the row (`printSlot`, lib/printPdf). A click on a button of
 * this page would be the person's activation HERE, which a browser shares
 * with a frame of the same origin only (not in every engine) and never where
 * the explorer is embedded in another site or runs in the desktop app.
 *
 * The row is drawn from the start of a print and stays, PARKED (laid out,
 * not seen, inert), while the print page loads and after it printed: the
 * frame in it holds the PDF the browser may still be reading for the dialog,
 * and moving a frame reloads it. It goes with the next print or the frame.
 */
const printAsk = ref<{ text: string; shown: boolean; width: number; height: number } | null>(null);
const printSlot = ref<HTMLElement | null>(null);
/** The print page's frame of the last print. */
let printFrame: HTMLIFrameElement | null = null;
/** The person's "Don't allow" for the print being asked about. */
let printAbort: AbortController | null = null;
/** One print at a time: a second waits for nobody, it is `unavailable`. */
let printing = false;

function dropPrintFrame() {
  printAbort?.abort();
  printAbort = null;
  printFrame?.remove();
  printFrame = null;
  printAsk.value = null;
}

function denyPrint() {
  printAbort?.abort();
}

/** filex's primary button as computed here (theme, density): the print
 *  page draws its Allow with it. */
function primaryLook(): Record<string, string> {
  const at = host.value;
  if (!at) return {};
  const probe = document.createElement('span');
  probe.className = 'fe-btn fe-btn--primary';
  probe.setAttribute('aria-hidden', 'true');
  probe.style.cssText = 'position:absolute;visibility:hidden;pointer-events:none;inset-inline-start:0;top:0';
  probe.textContent = t('appframe.consent_allow');
  at.appendChild(probe);
  try {
    return lookOf(probe);
  } finally {
    probe.remove();
  }
}

function tooLargeToPrint(): BridgeFailure {
  return new BridgeFailure('too_large', `at most ${LIMITS.maxPrintBytes >> 20} MiB`);
}

/* ── file.saveAs: a new file, in a folder the person picks ────────────── */

/**
 * The save-as question while it is on screen: filex's own folder dialog
 * (modals/DestinationPickerModal — the one Move to… / Copy to… use, not a
 * second private browser), titled with the app's name and the file's. One
 * at a time, like every question an interface can raise.
 */
const saveAsAsk = ref<{ name: string; storages: string[]; startAt?: string; resolve: (dir: string | null) => void } | null>(null);
/** From the call to the dialog (the storages may still be on their way). */
let saveAsPending = false;
/** The person's storages as the server's listing names them, once asked. */
let learnedStorages: string[] | null = null;

/**
 * Where the dialog opens: the folder of the file the interface was opened
 * with, else the folder the host names (`startAt`).
 *
 * ⚠ A draft (issue #71) and a desktop open-with copy live in filex's OWN
 * folders (`.filex-drafts/…`, `.filex-open`), which a person is never shown
 * and never writes into: the dialog opens where the host says instead (a
 * draft's own folder-to-be), else at that storage's root.
 */
function saveAsStart(): string | undefined {
  const first = props.files?.[0]?.path ?? '';
  const own = first.includes('://') ? (parentOfWire(first, false) ?? '') : '';
  if (own && !isInternalPath(own)) return own;
  const host = props.startAt ?? '';
  if (host.includes('://') && !isInternalPath(host)) return host;
  return own ? listingAddress(own) : undefined;
}

/**
 * The storages the dialog may span: the host's own list when it gave one
 * (what its Move to… offers), else the ones the server's listing names —
 * a host without the list (the viewer, an app's page) offers the same.
 */
async function saveAsStorages(start: string | undefined): Promise<string[]> {
  if (props.storages?.length) return props.storages;
  if (learnedStorages) return learnedStorages;
  try {
    const r = await props.api.index(start ?? '');
    learnedStorages = Array.isArray(r?.storages) ? r.storages.filter((s) => typeof s === 'string' && !!s) : [];
    return learnedStorages;
  } catch {
    return [];
  }
}

/** Ask the person for a folder; null when they closed the dialog. */
async function askFolder(name: string): Promise<string | null> {
  if (saveAsPending || saveAsAsk.value) throw new BridgeFailure('unavailable', 'a question is already on screen');
  saveAsPending = true;
  try {
    const startAt = saveAsStart();
    const storages = await saveAsStorages(startAt);
    if (!startAt && !storages.length) throw new BridgeFailure('unavailable', 'there is no folder to save into here');
    return await new Promise<string | null>((resolve) => {
      saveAsAsk.value = { name, storages, startAt, resolve };
    });
  } finally {
    saveAsPending = false;
  }
}

function answerSaveAs(dir: string | null) {
  const q = saveAsAsk.value;
  saveAsAsk.value = null;
  q?.resolve(dir);
}

/* The dialog's title: who asks, and for which file (security review UI-14). */
function saveAsTitle(name: string): string {
  return t('appframe.save_as_title', { app: who.value, name });
}

/** A stream the interface handed over and nothing will read: let it go. */
async function release(body: Blob | ReadableStream<Uint8Array>): Promise<void> {
  if (typeof ReadableStream !== 'undefined' && body instanceof ReadableStream) await body.cancel().catch(() => undefined);
}

/** An action's label, for the question; its id when the list cannot say. */
async function actionLabel(id: string): Promise<string> {
  try {
    const list = await props.api.pluginActions();
    const a = list.actions.find((x) => x.plugin === props.app && x.id === id);
    const label = a ? labelOf(a.label, props.locale) : '';
    if (label) return label;
  } catch {
    /* no list: the id */
  }
  return id;
}

const handlers = {
  'session.get': () => session(),

  async 'file.read'(params: unknown) {
    need('files:read');
    const p = (params ?? {}) as ReadParams;
    const f = fileAt(p.index);
    if (plaintextOf(f)) return readPlain(f, p.as);
    // Through the server's own door for an interface (#211, audit B19): it
    // checks the grant and the kind again, as it does for a save. A host api
    // without it (an older embed) reads the preview as before.
    const read = props.api.pluginUIRead;
    const res = typeof read === 'function'
      ? await read(props.app, props.view, f.path)
      : await props.api.fetchResponse(f.path);
    const size = Number(res.headers.get('content-length') ?? f.size ?? 0) || (f.size ?? 0);
    const mime = f.mime || res.headers.get('content-type') || '';
    const out = { name: f.name, size, mime };
    switch (p.as ?? 'stream') {
      case 'text': {
        if (size > LIMITS.maxTextBytes) throw new BridgeFailure('too_large', 'read a file this large as a stream');
        return { ...out, text: await res.text() };
      }
      case 'bytes':
        return { ...out, bytes: await res.arrayBuffer() };
      case 'stream':
        if (!res.body) return { ...out, bytes: await res.arrayBuffer() };
        return { ...out, stream: res.body };
      default:
        throw new BridgeFailure('invalid', 'as is stream, bytes or text');
    }
  },

  async 'file.save'(params: unknown) {
    need('files:write');
    const p = (params ?? {}) as SaveParams;
    const index = typeof p.index === 'number' ? p.index : 0;
    const f = fileAt(index);
    if (cannotSave(f)) throw new BridgeFailure('read_only', 'this file cannot be saved over here');
    if (plaintextOf(f)) return savePlain(index, f, p.data, p.through);
    const body = await toBody(p.data, p.mime);
    const target = index === 0 && props.savePath ? props.savePath : f.path;
    // In a session, the save says which part of its log it holds (0.56).
    const together = coedit && typeof p.through === 'number' ? coedit.saveParams(p.through) : null;
    const r = together
      ? await props.api.pluginUISave(props.app, props.view, { path: target }, body, { extra: together })
      : await props.api.pluginUISave(props.app, props.view, { path: target }, body);
    emit('saved', { path: r.path || target, size: r.size, index });
    // Saved: whatever the interface held is on the storage now. It says
    // `dirty(true)` again with its next change.
    dirty.value = false;
    emit('dirty', false);
    return { saved: true, size: r.size };
  },

  async 'file.saveAs'(params: unknown) {
    need('files:write');
    const p = (params ?? {}) as SaveAsParams;
    const name = typeof p.name === 'string' ? p.name.trim() : '';
    if (!name || name.includes('/') || name.includes('\\') || name === '.' || name === '..' || name.length > 255) {
      throw new BridgeFailure('invalid', 'name is a file name, without a folder');
    }
    if (holdsPlaintext.value) {
      if (typeof ReadableStream !== 'undefined' && p.data instanceof ReadableStream) await p.data.cancel().catch(() => undefined);
      refuseWhilePlain('save as');
    }
    const body = await toBody(p.data, p.mime);
    let dir: string | null;
    try {
      dir = await askFolder(name);
    } catch (e) {
      await release(body);
      throw e;
    }
    if (!dir) {
      await release(body);
      throw new BridgeFailure('cancelled');
    }
    const r = await props.api.pluginUISave(props.app, props.view, { dir, name }, body);
    emit('saved-as', { path: r.path, name: r.name, size: r.size });
    return { saved: true, name: r.name, size: r.size };
  },

  'ui.dirty'(params: unknown) {
    dirty.value = !!(params as { dirty?: boolean } | null)?.dirty;
    emit('dirty', dirty.value);
    return null;
  },

  'ui.title'(params: unknown) {
    const text = String((params as { text?: unknown } | null)?.text ?? '').slice(0, LIMITS.maxLineChars);
    emit('title', text);
    return null;
  },

  'ui.toast'(params: unknown) {
    const p = (params ?? {}) as ToastParams;
    const tone = p.tone === 'success' || p.tone === 'warning' || p.tone === 'error' ? p.tone : 'info';
    emit('toast', { text: `${who.value}: ${String(p.text ?? '').slice(0, LIMITS.maxLineChars)}`, tone });
    return null;
  },

  'ui.confirm'(params: unknown) {
    const p = (typeof params === 'object' && params ? params : {}) as ConfirmParams;
    if (confirmAsk.value) throw new BridgeFailure('unavailable', 'a question is already on screen');
    return new Promise<boolean>((resolve) => {
      confirmAsk.value = {
        text: String(p.text ?? '').slice(0, 1000),
        title: p.title ? String(p.title).slice(0, LIMITS.maxLineChars) : undefined,
        danger: !!p.danger,
        resolve,
      };
    });
  },

  'ui.close'() {
    emit('close');
    return null;
  },

  async 'clipboard.write'(params: unknown) {
    const text = String((params as { text?: unknown } | null)?.text ?? '');
    if (text.length > 1 << 20) throw new BridgeFailure('too_large', 'at most 1 MiB of text');
    if (typeof navigator === 'undefined' || !navigator.clipboard?.writeText) {
      throw new BridgeFailure('unavailable', 'this browser offers no clipboard here');
    }
    // On a gesture in the frame the page writes at once — except where the
    // browser wants the gesture in THIS document (WebKit, measured: the
    // write is refused): then the person is asked, and their click on
    // Allow is the gesture.
    if (gestureNow()) {
      try {
        await navigator.clipboard.writeText(text);
        return null;
      } catch (e) {
        if ((e as DOMException)?.name !== 'NotAllowedError') throw e;
      }
    }
    await askConsent(clipboardQuestion());
    await navigator.clipboard.writeText(text);
    return null;
  },

  async 'ui.download'(params: unknown) {
    need('ui:download');
    const p = (params ?? {}) as DownloadParams;
    const name = downloadName(p.name);
    const data = p.data as unknown;
    const isStream = typeof ReadableStream !== 'undefined' && data instanceof ReadableStream;
    if (typeof data !== 'string' && !(data instanceof ArrayBuffer) && !isStream) {
      throw new BridgeFailure('invalid', 'download takes a string, an ArrayBuffer or a ReadableStream');
    }
    const known = typeof data === 'string' ? new Blob([data]).size : data instanceof ArrayBuffer ? data.byteLength : 0;
    if (known > LIMITS.maxDownloadBytes) throw tooLarge();
    const type = typeof p.mime === 'string' && /^[\w.+-]+\/[\w.+-]+$/.test(p.mime) ? p.mime : 'application/octet-stream';
    try {
      await consent(downloadQuestion(name));
    } catch (e) {
      if (isStream) await (data as ReadableStream).cancel().catch(() => undefined);
      throw e;
    }
    return saveToDisk(name, data as string | ArrayBuffer | ReadableStream<Uint8Array>, type);
  },

  /**
   * 0.55: print a PDF the interface hands over (`ui:print`) - the print
   * dialog can save the PDF, so it is the person's call every time: filex
   * asks, and the dialog opens on their click on the row's Allow, a button of
   * the server's print page (lib/printPdf; security review sec055 S9). The
   * PDF is held once, as one Blob: refused at its first bytes when they are
   * not a PDF's and past LIMITS.maxPrintBytes (S10). `unavailable` where the
   * host names no print page, or while another print is being asked about.
   */
  async 'ui.print'(params: unknown) {
    need('ui:print');
    const p = (params ?? {}) as PrintParams;
    const data = p.data as unknown;
    const isStream = typeof ReadableStream !== 'undefined' && data instanceof ReadableStream;
    const letGo = async () => {
      if (isStream) await (data as ReadableStream).cancel().catch(() => undefined);
    };
    let name = '';
    try {
      name = downloadName(p.name);
      if (!(data instanceof ArrayBuffer) && !isStream) {
        throw new BridgeFailure('invalid', 'print takes a PDF as an ArrayBuffer or a ReadableStream');
      }
      if (p.mime !== undefined && p.mime !== 'application/pdf') throw new BridgeFailure('invalid', 'print takes a PDF (application/pdf)');
      if (data instanceof ArrayBuffer && data.byteLength > LIMITS.maxPrintBytes) throw tooLargeToPrint();
      if (data instanceof ArrayBuffer && !isPdf(data)) throw new BridgeFailure('invalid', 'the data is not a PDF');
    } catch (e) {
      await letGo();
      throw e;
    }
    const url = typeof props.api.printFrameUrl === 'function' ? props.api.printFrameUrl() : '';
    if (!url) {
      await letGo();
      throw new BridgeFailure('unavailable', 'this host offers no printing');
    }
    if (printing) {
      await letGo();
      throw new BridgeFailure('unavailable', 'another print is waiting for the person');
    }
    printing = true;
    try {
      let pdf: Blob;
      try {
        pdf = await collectPdf(data instanceof ArrayBuffer ? data : chunksOf(data as ReadableStream<Uint8Array>), LIMITS.maxPrintBytes);
      } catch (e) {
        if (e instanceof PrintFailure) throw e.code === 'too_large' ? tooLargeToPrint() : new BridgeFailure(e.code, e.message);
        throw e;
      }
      dropPrintFrame();
      const ask = new AbortController();
      printAbort = ask;
      printAsk.value = { text: printQuestion(name), shown: false, width: 0, height: 0 };
      await nextTick();
      try {
        printFrame = await printPdf(url, pdf, {
          mount: printSlot.value ?? host.value,
          label: printAllowLabel(),
          look: primaryLook(),
          armMs: CONSENT_ARM_MS,
          signal: ask.signal,
          onAsk: ({ width, height }) => {
            if (printAbort === ask && printAsk.value) printAsk.value = { ...printAsk.value, shown: true, width, height };
          },
        });
      } catch (e) {
        if (printAbort === ask) printAsk.value = null;
        if (e instanceof PrintFailure) throw new BridgeFailure(e.code, e.message);
        throw e;
      }
      if (printAsk.value) printAsk.value = { ...printAsk.value, shown: false };
      return { printed: true as const, size: pdf.size };
    } finally {
      printing = false;
      printAbort = null;
    }
  },

  async 'engine.call'(params: unknown) {
    if (!current.value.engine) throw new BridgeFailure('unavailable', 'this app has no module');
    refuseWhilePlain("the app's module");
    const p = (params ?? {}) as EngineCallParams;
    if (typeof p.method !== 'string' || !p.method) throw new BridgeFailure('invalid', 'method is required');
    const r = await props.api.pluginUICall(props.app, props.view, {
      method: p.method,
      params: p.params,
      paths: (props.files ?? []).map((f) => f.path),
    });
    return r.result;
  },

  async 'job.submit'(params: unknown) {
    if (!current.value.engine) throw new BridgeFailure('unavailable', 'this app has no module');
    refuseWhilePlain("the app's module");
    const p = (params ?? {}) as JobSubmitParams;
    if (typeof p.action !== 'string' || !p.action) throw new BridgeFailure('invalid', 'action is required');
    if (!gestureNow()) await askConsent(jobQuestion(await actionLabel(p.action)));
    const r = await props.api.pluginActionRun(props.app, p.action, {
      paths: (props.files ?? []).map((f) => f.path),
      params: p.params && typeof p.params === 'object' ? p.params : {},
    });
    const op = (r as { op?: Record<string, unknown> }).op;
    if (!op) throw new BridgeFailure('failed', 'nothing was queued');
    emit('op', op);
    return { op };
  },

  /* An encrypted session (the frame holds plaintext of an encrypted folder)
     reads and writes its own sealed store; a locked folder reads as an empty
     one. Any other session: the app's ordinary store, as always. */
  async 'state.get'(params: unknown) {
    const key = String((params as { key?: unknown } | null)?.key ?? '');
    if (holdsPlaintext.value) {
      const ledger = await sealedLedger().catch(() => null);
      return ledger ? ((await sealedStateGet(props.app, ledger, key)) ?? null) : null;
    }
    return appStateGet(props.app, key) ?? null;
  },

  /**
   * 0.52.0: what the app's license says (`LicenseInfo`) - the status, and for a
   * valid license its dates. filex keeps the key and the licensee's name; the
   * app sees neither (only these three fields cross the bridge, whatever the
   * server answers). A free app reads `{status: "free"}`.
   */
  async 'license.get'() {
    const r = await props.api.pluginLicense(props.app);
    const out: LicenseInfo = { status: String(r.status ?? 'free') };
    if (r.valid_until) out.valid_until = r.valid_until;
    if (r.updates_until) out.updates_until = r.updates_until;
    return out;
  },

  async 'state.set'(params: unknown) {
    const p = (params ?? {}) as { key?: unknown; value?: unknown };
    try {
      if (holdsPlaintext.value) {
        const ledger = await sealedLedger();
        if (!ledger) throw folderLocked();
        await sealedStateSet(props.app, ledger, String(p.key ?? ''), p.value);
      } else {
        appStateSet(props.app, String(p.key ?? ''), p.value);
      }
    } catch (e) {
      if (e instanceof AppStateTooLarge) throw new BridgeFailure('too_large', e.message);
      if (e instanceof LedgerNeedsAccount) throw new BridgeFailure('unavailable', noAccountWords());
      throw e;
    }
    return null;
  },

  /* ── editing together (0.56, files:co-edit) ─────────────────────────── */

  async 'coedit.join'(params: unknown) {
    need('files:co-edit');
    need('files:read');
    const p = (params ?? {}) as CoEditJoinParams;
    const f = fileAt(p.index);
    // A file of an encrypted folder (0.56): edited together only where this
    // page hands it to the interface in the clear (the folder's door,
    // files:e2e-plaintext); the session's key is then sealed with the folder
    // key, here. Any other encrypted file (a vault's, a `.fxe`): alone.
    const encrypted = isEncryptedKind(f.encrypted);
    const door = props.e2e;
    // A vault's file is handed over in the clear too (0.56), but a vault is
    // one person's at a time: never edited together.
    if (encrypted && (f.encrypted !== 'folder' || !plaintextOf(f) || typeof door?.folderKeys !== 'function')) {
      throw new BridgeFailure('unavailable', 'encrypted');
    }
    const folder = encrypted && door?.folderKeys ? () => door.folderKeys!(f.path) : undefined;
    const t = coeditTransport();
    if (!t) throw new BridgeFailure('unavailable', 'this host offers no editing together');
    // One membership per frame: a second join leaves the first.
    const before = coedit;
    coedit = null;
    if (before) await before.leave();
    const s = await CoEditSession.join(t, f.path, {
      entry: (e) => bridge?.emit('coedit.entry', e),
      cursor: (c) => bridge?.emit('coedit.cursor', c),
      dropped: (d) => {
        if (coedit === s) coedit = null;
        bridge?.emit('coedit.dropped', d);
      },
    }, { folder, outbox: coeditOutbox(), askUnsent });
    if (!bridge) {
      // The frame went while the session was joined: leave it again.
      void s.leave({ keepalive: true });
      throw new BridgeFailure('unavailable', 'gone');
    }
    coedit = s;
    return s.hello();
  },

  'coedit.subscribe'(params: unknown) {
    const p = (params ?? {}) as CoEditSubscribeParams;
    inSession().subscribe(Number(p.from ?? 0));
    return null;
  },

  async 'coedit.append'(params: unknown) {
    const p = (params ?? {}) as CoEditAppendParams;
    return inSession().append(p.kind, p.body);
  },

  async 'coedit.lease'(params: unknown) {
    const p = (params ?? {}) as CoEditLeaseParams;
    return { granted: await inSession().lease(p.op, Number(p.changesSeen ?? 0)) };
  },

  'coedit.cursor'(params: unknown) {
    inSession().cursor((params as CoEditCursorParams | null)?.cursor);
    return null;
  },

  async 'coedit.blob.put'(params: unknown) {
    const s = inSession();
    const p = (params ?? {}) as CoEditBlobPutParams;
    if (!(p.data instanceof ArrayBuffer)) throw new BridgeFailure('invalid', 'a blob is an ArrayBuffer');
    await s.putBlob(String(p.name ?? ''), p.data);
    return null;
  },

  async 'coedit.blob.get'(params: unknown) {
    const p = (params ?? {}) as CoEditBlobGetParams;
    const name = String(p.name ?? '');
    return { name, bytes: await inSession().getBlob(name) };
  },

  async 'coedit.leave'() {
    const s = coedit;
    coedit = null;
    await s?.leave();
    return null;
  },
};

/* ── editing together: the session this frame joined ───────────────── */

/** The frame's membership of its file's session (0.56), null when none. */
let coedit: CoEditSession | null = null;

function inSession(): CoEditSession {
  if (!coedit) throw new BridgeFailure('unavailable', 'not in a session (coedit.join first)');
  return coedit;
}

/** The relay's routes through the host's API; null when the host has none
 *  (an older embed's api): then there is no editing together here. */
function coeditTransport(): CoEditTransport | null {
  const api = props.api;
  if (typeof api.coeditJoin !== 'function' || typeof api.coeditCall !== 'function') return null;
  return {
    join: (path, extra) => api.coeditJoin(props.app, props.view, path, extra),
    call: (sid, op, body, opts) => api.coeditCall(props.app, props.view, sid, op, body, opts),
    putBlob: (sid, name, member, bytes) => api.coeditBlobPut(props.app, props.view, sid, name, member, bytes),
    getBlob: (sid, name, member) => api.coeditBlobGet(props.app, props.view, sid, name, member),
  };
}

/** The changes not placed yet, kept in this browser (lib/coeditOutbox): one
 *  store for every frame of the page. */
let outboxStore: OutboxStore | null = null;
function coeditOutbox(): OutboxStore {
  outboxStore ??= browserOutbox();
  return outboxStore;
}

/**
 * The changes of an earlier opening that never reached the others: apply
 * them, or not - asked above the frame in the server's words (the join
 * answered them), filex's own question, not the app's.
 */
const unsentAsk = ref<{ text: string; apply: string; discard: string; resolve: (v: boolean | null) => void } | null>(null);

function askUnsent(q: { text: string; apply: string; discard: string }): Promise<boolean | null> {
  if (unsentAsk.value) return Promise.resolve(null);
  return new Promise<boolean | null>((resolve) => {
    unsentAsk.value = { ...q, resolve };
  });
}

/** true: apply, false: discard, null: the frame went without an answer (kept). */
function answerUnsent(apply: boolean | null) {
  const q = unsentAsk.value;
  unsentAsk.value = null;
  q?.resolve(apply);
}

/** Leave the session when the frame goes (the page may be going too). */
function leaveSession() {
  const s = coedit;
  coedit = null;
  if (s) void s.leave({ keepalive: true });
}

function answerConfirm(yes: boolean) {
  const q = confirmAsk.value;
  confirmAsk.value = null;
  q?.resolve(yes);
}

/* ── the element ────────────────────────────────────────────────────── */

/**
 * Build the frame: the sandbox, the referrer policy and the title first, the
 * address last, and only then into the document. The ORDER is the security
 * (see the file's header) — tests read it back.
 */
function buildFrame(): HTMLIFrameElement {
  const f = document.createElement('iframe');
  f.setAttribute('sandbox', 'allow-scripts');
  f.setAttribute('referrerpolicy', 'no-referrer');
  f.setAttribute('title', props.title || props.app);
  f.className = 'fe-appframe__frame';
  f.dataset.testid = 'app-frame';
  f.setAttribute('src', src.value);
  return f;
}

function mount() {
  unmount();
  if (!host.value) return;
  const f = buildFrame();
  const b = createAppBridge({
    frame: () => f,
    handlers,
    onConnect: () => {
      connected.value = true;
      emit('connected');
    },
    onDisconnect: () => {
      connected.value = false;
    },
  });
  f.addEventListener('load', () => b.frameLoaded());
  frame = f;
  bridge = b;
  host.value.appendChild(f);
}

function unmount() {
  leaveSession();
  if (unsentAsk.value) answerUnsent(null);
  bridge?.destroy();
  bridge = null;
  dropPrintFrame();
  if (frame) {
    frame.remove();
    frame = null;
  }
  connected.value = false;
  dirty.value = false;
  if (confirmAsk.value) answerConfirm(false);
  if (consentAsk.value) answerConsent(false);
  if (closeAsk.value) answerClose('keep');
  if (saveAsAsk.value) answerSaveAs(null);
}

let offUpdated: (() => void) | null = null;

const HOST_GESTURES = ['pointerdown', 'keydown', 'touchstart'] as const;

onMounted(() => {
  if (activationLive()) hostGestureAt = Date.now();
  for (const ev of HOST_GESTURES) window.addEventListener(ev, onHostGesture, true);
  // A tab that closes unmounts nothing: the session is left on the way out.
  window.addEventListener('pagehide', leaveSession);
  mount();
  offUpdated = onAppUpdated((app, version) => {
    if (app !== props.app || !version || version === appVersion.value) return;
    newer.value = version;
    notifyUpdated(version);
  });
  // Said once per person: the first opening after the version they last saw.
  const v = appVersion.value;
  if (v) {
    const seen = appSeenVersion(props.app);
    if (seen && seen !== v) updatedNote.value = v;
    if (seen !== v) markAppSeen(props.app, v);
  }
});
onBeforeUnmount(() => {
  for (const ev of HOST_GESTURES) window.removeEventListener(ev, onHostGesture, true);
  window.removeEventListener('pagehide', leaveSession);
  offUpdated?.();
  offUpdated = null;
  unmount();
});
watch([src, generation], () => mount());
// A parent that hands a different interface wins over a reload's.
watch(
  () => props.ui,
  () => {
    liveUI.value = null;
  },
);

// The look and the language follow the explorer while the frame is open.
watch(mode, () => bridge?.emit('theme', themeInfo()));
watch(
  () => props.locale,
  (l) => bridge?.emit('locale', { locale: l, dir: localeDir(l) }),
);

/**
 * Ask the interface to save (the host's Save, a draft's "Save to disk"):
 * resolves true once it saved, false when it has nothing to save with or
 * refused. Never throws.
 */
async function requestSave(): Promise<boolean> {
  if (!bridge || !connected.value) return false;
  try {
    await bridge.ask('save');
    return true;
  } catch {
    return false;
  }
}

/**
 * May the frame's surroundings close? True at once when nothing is unsaved;
 * otherwise the person is asked — save and close (the interface saves
 * through the bridge first; a failed save keeps it open), close without
 * saving, or keep editing. Every placement's close goes through here, so the
 * question is the same in a dialog, a tab and the viewer.
 */
async function confirmClose(): Promise<boolean> {
  if (!dirty.value || !connected.value) {
    notifyClosing();
    return true;
  }
  const answer = await new Promise<'save' | 'discard' | 'keep'>((resolve) => {
    closeAsk.value = resolve;
  });
  if (answer === 'keep') return false;
  if (answer === 'save') {
    closeBusy.value = true;
    try {
      if (!(await requestSave())) return false;
    } finally {
      closeBusy.value = false;
    }
  }
  notifyClosing();
  return true;
}

function answerClose(v: 'save' | 'discard' | 'keep') {
  const r = closeAsk.value;
  closeAsk.value = null;
  r?.(v);
}

/** A new version of the app was approved: tell the interface. */
function notifyUpdated(version: string) {
  bridge?.emit('app.updated', { version });
}

/** Tell the interface its surroundings are closing. */
function notifyClosing() {
  bridge?.emit('close.request');
}

/** Replace the frame with a fresh one (a reload: one bridge per element). */
function reload() {
  generation.value++;
}

/**
 * "Reload" on a newer approved version: unsaved changes are asked about
 * first (the same question as closing), then the frame opens the newer
 * interface — its address comes from the server's list, which carries the
 * new bundle's hash.
 */
async function reloadNewer() {
  if (!(await confirmClose())) return;
  const version = newer.value;
  let next: PluginUIRef | null = null;
  try {
    const list = await props.api.pluginActions();
    next =
      list.views.find((v) => v.plugin === props.app && v.id === props.view && v.ui)?.ui ??
      list.actions.find((a) => a.plugin === props.app && a.view === props.view && a.ui)?.ui ??
      null;
  } catch {
    /* no list: reload what is open */
  }
  newer.value = '';
  updatedNote.value = '';
  if (next) {
    liveUI.value = next;
    markAppSeen(props.app, next.version || version);
  }
  reload();
}

function dismissNote() {
  newer.value = '';
  updatedNote.value = '';
}

defineExpose({ requestSave, confirmClose, notifyUpdated, notifyClosing, reload, reloadNewer, connected, dirty });
</script>

<template>
  <div
    ref="host"
    class="fe-appframe"
    :class="`fe-appframe--${placement}`"
    :data-app="app"
    :data-connected="connected ? 'true' : 'false'"
  >
    <div v-if="newer || updatedNote" class="fe-appframe__note" role="status" data-testid="appframe-updated">
      <span class="fe-appframe__note-text">
        {{ newer ? t('appframe.updated_reload', { app: title || app }) : t('appframe.updated', { app: title || app, version: updatedNote }) }}
      </span>
      <button v-if="newer" type="button" class="fe-btn fe-btn--primary" data-testid="appframe-reload" @click="reloadNewer">
        {{ t('appframe.reload') }}
      </button>
      <button type="button" class="fe-btn" data-testid="appframe-dismiss" @click="dismissNote">
        {{ t('appframe.dismiss') }}
      </button>
    </div>
    <div
      v-if="unsentAsk"
      class="fe-appframe__note fe-appframe__consent fe-appframe__over"
      role="alertdialog"
      aria-live="assertive"
      data-testid="appframe-unsent"
    >
      <span class="fe-appframe__note-text">{{ unsentAsk.text }}</span>
      <button type="button" class="fe-btn fe-btn--primary" data-testid="appframe-unsent-apply" @click="answerUnsent(true)">
        {{ unsentAsk.apply }}
      </button>
      <button type="button" class="fe-btn" data-testid="appframe-unsent-discard" @click="answerUnsent(false)">
        {{ unsentAsk.discard }}
      </button>
    </div>
    <div
      v-if="consentAsk"
      class="fe-appframe__note fe-appframe__consent"
      role="alertdialog"
      aria-live="assertive"
      data-testid="appframe-consent"
    >
      <span class="fe-appframe__note-text">{{ consentAsk.text }}</span>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        data-testid="appframe-consent-allow"
        :disabled="!consentArmed"
        @click="answerConsent(true)"
      >
        {{ t('appframe.consent_allow') }}
      </button>
      <button type="button" class="fe-btn" data-testid="appframe-consent-deny" @click="answerConsent(false)">
        {{ t('appframe.consent_deny') }}
      </button>
    </div>
    <div
      v-if="printAsk"
      class="fe-appframe__note fe-appframe__consent fe-appframe__print"
      :class="{ 'fe-appframe__print--parked': !printAsk.shown }"
      :role="printAsk.shown ? 'alertdialog' : undefined"
      :aria-live="printAsk.shown ? 'assertive' : undefined"
      :aria-hidden="printAsk.shown ? undefined : 'true'"
      :inert="printAsk.shown ? undefined : true"
      :data-shown="printAsk.shown ? 'true' : 'false'"
      data-testid="appframe-print-ask"
    >
      <span class="fe-appframe__note-text">{{ printAsk.text }}</span>
      <span
        ref="printSlot"
        class="fe-appframe__print-slot"
        :style="printAsk.shown ? { width: `${printAsk.width}px`, height: `${printAsk.height}px` } : undefined"
      />
      <button type="button" class="fe-btn" data-testid="appframe-print-deny" @click="denyPrint">
        {{ t('appframe.consent_deny') }}
      </button>
    </div>
    <PluginConfirmModal
      v-if="confirmAsk"
      :open="true"
      :locale="locale"
      :theme="theme"
      :title="`${who}: ${confirmAsk.title || t('appframe.confirm_title')}`"
      :message="confirmAsk.text"
      :danger="confirmAsk.danger"
      @close="answerConfirm(false)"
      @confirm="answerConfirm(true)"
    />
    <Modal
      v-if="closeAsk"
      :open="true"
      size="sm"
      :locale="locale"
      :theme="theme"
      :busy="closeBusy"
      :title="t('appframe.unsaved_title')"
      @close="answerClose('keep')"
    >
      <p data-testid="appframe-unsaved">{{ t('appframe.unsaved_text', { app: title || app }) }}</p>
      <template #actions>
        <button type="button" class="fe-btn" data-testid="appframe-keep" :disabled="closeBusy" @click="answerClose('keep')">
          {{ t('appframe.keep_open') }}
        </button>
        <button type="button" class="fe-btn fe-btn--danger" data-testid="appframe-discard" :disabled="closeBusy" @click="answerClose('discard')">
          {{ t('appframe.discard') }}
        </button>
        <button type="button" class="fe-btn fe-btn--primary" data-testid="appframe-save-close" :disabled="closeBusy" @click="answerClose('save')">
          {{ t('appframe.save_close') }}
        </button>
      </template>
    </Modal>
    <DestinationPickerModal
      v-if="saveAsAsk"
      :open="true"
      :api="api"
      :locale="locale"
      :theme="theme"
      mode="save"
      :title="saveAsTitle(saveAsAsk.name)"
      :storages="saveAsAsk.storages"
      :start-at="saveAsAsk.startAt"
      @close="answerSaveAs(null)"
      @pick="answerSaveAs"
    />
  </div>
</template>

<style>
/* ⚠ Not scoped: the web-component build does not match scoped hashes (see
   DrawioViewer). Every selector is prefixed. */
.fe-appframe {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
  background: var(--fe-bg, #fff);
}
.fe-appframe--inspector {
  height: 320px;
}
.fe-appframe--viewer,
.fe-appframe--page,
.fe-appframe--home {
  min-height: 70vh;
}
.fe-appframe--modal {
  min-height: 60vh;
}
.fe-appframe__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-block-end: 1px solid var(--fe-border, #e4e4e7);
  background: var(--fe-bg-elev, #f4f4f5);
  color: var(--fe-text, #18181b);
  font-size: 13px;
}
.fe-appframe__note-text {
  flex: 1 1 16rem;
  min-width: 0;
}
/* The print question lies OVER the top of the app's frame instead of above
   it (#217). In the flow it pushed the frame down and shrank it the moment
   it showed, and an editor that was busy making the PDF ("Printing
   Document" in the office editor) could not redraw at its new size: the
   person looked at a blank editor until the print was over. Out of the flow,
   the frame keeps its size from the first print to the last. */
.fe-appframe__print {
  position: absolute;
  inset-inline: 0;
  top: 0;
  z-index: 3;
  box-shadow: var(--fe-shadow-sm, 0 2px 6px rgba(0, 0, 0, 0.12));
}
/* The question about the changes an earlier opening left (editing together,
   0.56) lies over the frame too, for the same reason: it may come while the
   editor is starting. */
.fe-appframe__over {
  position: absolute;
  inset-inline: 0;
  top: 0;
  z-index: 3;
  box-shadow: var(--fe-shadow-sm, 0 2px 6px rgba(0, 0, 0, 0.12));
}
/* The print question while nobody is asked (the print page loading, or the
   PDF already handed to the dialog): laid out, so the frame in it keeps its
   PDF, but not seen and not reachable. */
.fe-appframe__print--parked {
  position: absolute;
  inset-inline-start: -10000px;
  inset-inline-end: auto;
  top: 0;
  width: 1px;
  height: 1px;
  overflow: hidden;
  opacity: 0;
  pointer-events: none;
}
/* Where the print page's own Allow goes: the frame, at the size it asks. */
.fe-appframe__print-slot {
  display: inline-block;
  flex: none;
  max-width: 100%;
  line-height: 0;
}
.fe-appframe__frame {
  flex: 1;
  width: 100%;
  min-height: 0;
  border: 0;
  background: transparent;
  color-scheme: normal;
}
</style>
