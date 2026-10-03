/**
 * useE2eFiles — single encrypted files (`.fxe`) and the STREAMED paths of
 * encrypted folders (docs/E2E-ENCRYPTION.md → "Single encrypted files",
 * "Streaming content", "Downloading a decrypted copy").
 *
 * The crypto is lib/e2efile.ts + lib/e2estream.ts; the save sinks are
 * lib/e2esave.ts; the archive is lib/zipstream.ts. What lives here is the
 * ORDER of things — and every step of it happens in this tab:
 *
 *   encrypt   the plaintext is streamed from the server, encrypted here and
 *             uploaded (chunked, as it is made); only after the upload
 *             committed is the original moved to the trash;
 *   open      the header is read, the password (or the recovery key) opens it
 *             HERE, the body is decrypted into a blob for the viewers;
 *   download  decrypted as it arrives, into the save sink the browser has;
 *   remove    decrypted, uploaded under the original name, then the `.fxe`
 *             goes to the trash;
 *   password  a new header, and the body carried over byte for byte.
 *
 * Keys are held in memory only, in a ring keyed by the `.fxe`'s path: the
 * file's own key, its DEK and its original name. Nothing is written to any
 * storage, and a reload forgets everything.
 *
 * One instance per explorer (FileExplorer), so the web app, the desktop app
 * and every embed get the same behaviour.
 */

import { onBeforeUnmount, ref, type Ref } from 'vue';

import type { FileNode } from '../types/FileNode';
import type { FileApi, PendingOpDto } from './useFileApi';
import { isStagedUnsupported, type UploadJob, type UploadOptions, type UploadResult } from './useUploadChunked';
import { requestFailure } from '../lib/errorWords';
import {
  E2E_MARKER_NAME,
  E2eDecryptError,
  b64ToBytes,
  bytesToB64,
  escrowAvailability,
  hasMagic,
  importEscrowPrivateKey,
  rewrapFileKey,
  type E2eCredential,
  type EscrowAvailability,
} from '../lib/e2ecrypto';
import {
  FxeFormatError,
  changeFxePassword,
  createFxe,
  decryptFxeBody,
  fxeHasRecovery,
  fxeSlots,
  fxeStoredName,
  isFxeName,
  isHiddenFxeName,
  readFxe,
  rekeyedFxeKey,
  replaceFxeHeader,
  unlockFxe,
  type FxeCredential,
  type FxeHeader,
  type FxeKey,
} from '../lib/e2efile';
import {
  ByteStreamReader,
  E2E_FOLDER_HEADER_LEN,
  decryptFolderFileStream,
  encryptFolderFileStream,
  prependBytes,
} from '../lib/e2estream';
import { extensionOf, namePlainProblem } from '../lib/e2enames';
import { e2eMimeForExt } from '../lib/e2emime';
import {
  E2E_BLOB_SAVE_LIMIT,
  SaveCancelled,
  SaveNeedsGesture,
  SaveTooLarge,
  pickSaveTarget,
  streamingSaveAvailable,
  type SaveTarget,
} from '../lib/e2esave';
import { createZipStream, type ZipEntry } from '../lib/zipstream';
import { streamToFile, streamUploadSource, type UploadSource } from '../lib/uploadSource';
import { wireBaseName, wireJoinPath, wireParentPath } from './useE2eNames';
import { useLocale } from './useLocale';

/** What the explorer lends this composable. */
export interface E2eFilesDeps {
  api: FileApi;
  chunked: {
    uploadFile(opts: UploadOptions): Promise<UploadResult>;
    threshold(): number;
  };
  locale: () => string | undefined;
  t: (key: string, vars?: Record<string, string | number>) => string;
  /** A short notice; `error` keeps it up longer. */
  toast: (message: string, error?: boolean) => void;
  emitError: (message: string, op: string) => void;
  escrowPublicKey: () => string | null;
  /** The short id of the installation's escrow key (capabilities), or null:
   *  which files the escrow door opens (escrowAvailability). */
  escrowKid?: () => string | null;
  /** A queued job (a delete), so its end refreshes the listing. */
  registerOp: (op: PendingOpDto) => void;
  /** Read the listing on screen again. */
  reload: () => Promise<void>;
  /** Show a decrypted blob in the explorer's viewers, as `node`. */
  openPreview: (node: FileNode, url: string) => void;
  /** Show a recovery key once (RecoveryKeyModal), for the file called `name`. */
  showRecoveryKey: (key: string, name: string) => void;
}

/** Why the unlock dialog is open. */
export type FxeIntent = 'open' | 'download' | 'remove';

/**
 * A decrypted download this browser cannot hold (Firefox, Safari: the
 * in-memory save, over E2E_BLOB_SAVE_LIMIT) — and the way out: the encrypted
 * bytes, and the `filex decrypt` command for them (E2eTooBigModal).
 */
export interface FxeTooBig {
  /** What could not be saved, in the person's words. */
  name: string;
  /** Its size, when known before starting; null when a zip outgrew the limit. */
  size: number | null;
  limit: number;
  /** What to download instead: a `.fxe` as it is, or the encrypted folder. */
  encrypted: { kind: 'file' | 'folder'; path: string };
  /** The command that decrypts that download where it is saved. */
  command: string;
}

/** One row this composable adds to a context menu. Exported because the
 *  composable's return type names it (declaration output). */
export interface FxeMenuRow {
  key: string;
  label: string;
  icon?: string;
  hidden?: boolean;
  danger?: boolean;
  title?: string;
}

/** The context-menu keys this composable answers. */
export const FXE_ACTION_KEYS = [
  'fxe-encrypt',
  'fxe-download-raw',
  'fxe-remove',
  'fxe-password',
  'e2e-download-encrypted',
] as const;

export function isFxeActionKey(key: string): boolean {
  return (FXE_ACTION_KEYS as readonly string[]).includes(key);
}

/** A row that is a single encrypted file. */
export function isFxeRow(n: { type?: string; basename?: string; e2e_stored?: unknown } | null | undefined): boolean {
  return !!n && n.type === 'file' && isFxeName(typeof n.e2e_stored === 'string' ? n.e2e_stored : n.basename);
}

/** `name (2).ext` — a free name next to a taken one. */
export function numberedName(name: string, n: number): string {
  const dot = name.lastIndexOf('.');
  if (dot <= 0) return `${name} (${n})`;
  return `${name.slice(0, dot)} (${n})${name.slice(dot)}`;
}

export function useE2eFiles(deps: E2eFilesDeps) {
  const { api, t } = deps;

  /** path of a `.fxe` → its keys and original name (memory only). */
  const ring = new Map<string, FxeKey>();
  const ringVer = ref(0);
  /** path → decrypted blob URL handed to a viewer. */
  const urls = new Map<string, string>();

  function revokeAll() {
    for (const u of urls.values()) URL.revokeObjectURL(u);
    urls.clear();
  }
  onBeforeUnmount(() => {
    revokeAll();
    ring.clear();
  });

  // ── state the dialogs render ──────────────────────────────────────────
  const encryptTarget: Ref<FileNode | null> = ref(null);
  const encryptBusy = ref(false);
  const encryptError = ref<string | null>(null);
  const encryptProgress = ref<number | null>(null);

  const unlockTarget: Ref<FileNode | null> = ref(null);
  const unlockIntent = ref<FxeIntent>('open');
  const unlockBusy = ref(false);
  const unlockError = ref<string | null>(null);
  const unlockProgress = ref<number | null>(null);
  /** The browser wants one more click before its save dialog (after an unlock). */
  const unlockNeedsSave = ref(false);
  /** The file has a recovery key slot (the dialog's "use the recovery key" link). */
  const unlockHasRecovery = ref(true);
  /** Whether the installation's escrow key opens this file — read from its
   *  header when the dialog opens, as for a folder's lock screen. */
  const unlockEscrow = ref<EscrowAvailability>('off');
  /** The escrow key this file was sealed to, when it was. */
  const unlockEscrowKid = ref<string | null>(null);

  const pwTarget: Ref<FileNode | null> = ref(null);
  const pwBusy = ref(false);
  const pwError = ref<string | null>(null);

  /** Set when a decrypted download is too big for this browser's memory. */
  const tooBig: Ref<FxeTooBig | null> = ref(null);

  // ── helpers ───────────────────────────────────────────────────────────

  /** The body of a file on the server, as a stream. */
  async function fetchBody(path: string): Promise<ReadableStream<Uint8Array>> {
    const headers = await api.authHeaders();
    const res = await fetch(api.previewUrl(path), {
      headers,
      credentials: api.credentialsMode(),
      cache: 'no-store',
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw requestFailure(res.status, text, deps.locale());
    }
    if (!res.body) throw new Error('e2e: this browser cannot read a download as a stream');
    return res.body;
  }

  /** The names taken in a folder, as the server stores them. */
  async function takenIn(dir: string): Promise<Set<string>> {
    const resp = await api.index(dir);
    return new Set((resp.files ?? []).map((f) => f.basename));
  }

  /** The first of `name`, `name (2)`, … not in `taken`. */
  function freeName(name: string, taken: Set<string>): string {
    if (!taken.has(name)) return name;
    for (let i = 2; i < 1000; i++) {
      const n = numberedName(name, i);
      if (!taken.has(n)) return n;
    }
    throw new Error('e2e: no free name');
  }

  /**
   * Upload a stream of exactly `size` bytes as `dir/name`: the staged,
   * chunked path reading the stream as it goes (nothing held but the chunk in
   * flight); the single POST for what is below the chunk size, or when the
   * server has no staged path — then gathered in memory, and refused past
   * the in-memory limit rather than tried.
   */
  async function uploadStream(
    dir: string,
    name: string,
    stream: ReadableStream<Uint8Array>,
    size: number,
    onProgress?: (bytes: number) => void,
    /* wiring:e2 convert - `fields` go with the write wherever it lands: the
       staged commit's query, or the single POST's form (`expect`,
       `e2e_convert`). `signal` stops the upload in flight. */
    extra: { fields?: Record<string, string>; signal?: AbortSignal } = {},
  ): Promise<void> {
    const file = new File([], name, { type: 'application/octet-stream' });
    const { fields, signal } = extra;
    if (signal?.aborted) {
      await stream.cancel().catch(() => undefined);
      throw new DOMException('Aborted by user', 'AbortError');
    }
    if (size > deps.chunked.threshold()) {
      const source: UploadSource = streamUploadSource(stream, size);
      let job: UploadJob | null = null;
      const onAbort = () => job?.cancel();
      signal?.addEventListener('abort', onAbort);
      try {
        await deps.chunked.uploadFile({
          path: dir,
          file,
          source,
          commitQuery: fields,
          onProgress: (j) => {
            job = j;
            if (signal?.aborted) j.cancel();
            onProgress?.(j.uploadedBytes);
          },
        });
        return;
      } catch (err) {
        if (!isStagedUnsupported(err)) throw err;
        if (size > E2E_BLOB_SAVE_LIMIT) {
          // Nothing read yet (the source is lazy): let the download go.
          await stream.cancel().catch(() => undefined);
          throw new SaveTooLarge(size, E2E_BLOB_SAVE_LIMIT);
        }
      } finally {
        signal?.removeEventListener('abort', onAbort);
      }
    }
    const whole = await streamToFile(stream, name);
    if (whole.size !== size) throw new Error('e2e: the upload came out a different size than it should');
    if (signal?.aborted) throw new DOMException('Aborted by user', 'AbortError');
    await api.uploadMultipart(dir, [whole], (p) => onProgress?.(Math.round((p / 100) * size)), fields);
  }

  /** Move one item to the trash, queued where the server queues. */
  async function trash(path: string): Promise<void> {
    const dir = wireParentPath(path);
    if (api.endpoints.deleteAsync) {
      const { op } = await api.deleteAsync([path], dir);
      deps.registerOp(op);
      return;
    }
    await api.deleteItems(dir, [path]);
  }

  function messageOf(err: unknown): string {
    return err instanceof Error && err.message ? err.message : String(err);
  }

  /** A decrypt failure in words: a wrong key, or a damaged/tampered file. */
  function decryptWords(err: unknown): string {
    if (err instanceof FxeFormatError) return t('e2e.fxe.not_readable');
    if (err instanceof E2eDecryptError) return t('e2e.fxe.damaged');
    return messageOf(err);
  }

  /** The header and body of a `.fxe`, read from the server. */
  async function readHeader(path: string): Promise<{ header: FxeHeader; unsupported: string[]; body: ReadableStream<Uint8Array> }> {
    const { parsed, body } = await readFxe(await fetchBody(path));
    return { header: parsed.header, unsupported: parsed.unsupported, body };
  }

  /** The ring's key for this file, if it still fits the header on the server. */
  function keyFits(key: FxeKey, h: FxeHeader): boolean {
    return key.header.dek === h.dek && key.header.nonce === h.nonce && key.header.size === h.size;
  }

  /** The label a `.fxe` row is shown with: its real name once opened here. */
  function realName(path: string): string | undefined {
    void ringVer.value;
    return ring.get(path)?.name;
  }

  // ── context menu ──────────────────────────────────────────────────────

  /**
   * The rows this composable adds to a selection's menu.
   *   canWrite     may write to the selection
   *   inEncrypted  the selection is inside an encrypted folder (the folder
   *                encrypts it already; a `.fxe` in there is not offered)
   *   unlockedEncryptedCopy  the selection is (inside) an UNLOCKED encrypted
   *                folder: "Download" decrypts, so the ciphertext copy is a
   *                second verb
   */
  function menuRows(
    sel: FileNode[],
    ctx: { canWrite: boolean; inEncrypted: boolean; unlockedEncryptedCopy: boolean },
  ): FxeMenuRow[] {
    const single = sel.length === 1 ? sel[0] : null;
    const fxe = !!single && isFxeRow(single);
    const plainFile = !!single && single.type === 'file' && !fxe && !ctx.inEncrypted && !single.e2e_root;
    return [
      {
        key: 'fxe-encrypt',
        label: t('e2e.fxe.ctx_encrypt'),
        icon: 'lock',
        hidden: !plainFile || !ctx.canWrite,
      },
      { key: 'fxe-download-raw', label: t('e2e.fxe.ctx_download_raw'), icon: 'download', hidden: !fxe },
      {
        key: 'e2e-download-encrypted',
        label: t('e2e.dl.ctx_encrypted_copy'),
        icon: 'download',
        hidden: fxe || sel.length === 0 || !ctx.unlockedEncryptedCopy,
      },
      { key: 'fxe-password', label: t('e2e.fxe.ctx_password'), icon: 'lock', hidden: !fxe || !ctx.canWrite },
      { key: 'fxe-remove', label: t('e2e.fxe.ctx_remove'), icon: 'restore', hidden: !fxe || !ctx.canWrite },
    ];
  }

  // ── encrypt a plain file ──────────────────────────────────────────────

  function startEncrypt(n: FileNode) {
    encryptTarget.value = n;
    encryptError.value = null;
    encryptProgress.value = null;
  }

  function closeEncrypt() {
    if (encryptBusy.value) return;
    encryptTarget.value = null;
  }

  /**
   * An administrator's "delete the original for good", once the encrypted copy
   * is committed: every version the original kept (row and bytes), then the
   * original itself — into the trash synchronously, so its entry exists — and
   * that trash entry. The versions go first, while the file is still live and
   * its history can be listed; a trash purge would drop their rows but not
   * necessarily what they stored.
   */
  async function purgeOriginal(n: FileNode, nodeId: number): Promise<void> {
    for (const v of await api.listVersions(nodeId)) await api.purgeVersion(v.id);
    await api.deleteItems(wireParentPath(n.path), [n.path]);
    try {
      await api.purgeTrash(nodeId);
    } catch (err) {
      // No trash on this server: the delete above was already for good.
      if ((err as { status?: number }).status !== 404) throw err;
    }
  }

  async function submitEncrypt(payload: { password: string; hideName: boolean; purge?: boolean }) {
    const n = encryptTarget.value;
    if (!n || encryptBusy.value) return;
    encryptBusy.value = true;
    encryptError.value = null;
    encryptProgress.value = 0;
    try {
      const dir = wireParentPath(n.path);
      const size = typeof n.size === 'number' ? n.size : NaN;
      if (!Number.isSafeInteger(size) || size < 0) throw new Error(t('e2e.fxe.size_unknown'));
      const purge = payload.purge === true;
      if (purge && typeof n.id !== 'number') throw new Error(t('e2e.fxe.purge_no_id'));
      if (namePlainProblem(n.basename)) throw new Error(t('e2e.fxe.bad_name'));
      const taken = await takenIn(dir);
      let stored = fxeStoredName(n.basename, payload.hideName);
      if (payload.hideName) {
        for (let i = 0; taken.has(stored) && i < 20; i++) stored = fxeStoredName(n.basename, true);
      } else {
        stored = `${freeName(n.basename, new Set([...taken].filter(isFxeName).map((x) => x.slice(0, -4))))}.fxe`;
      }
      if (namePlainProblem(stored)) throw new Error(t('e2e.fxe.name_too_long'));
      const created = await createFxe(n.basename, size, await fetchBody(n.path), payload.password, {
        escrowPublicKey: deps.escrowPublicKey(),
      });
      await uploadStream(dir, stored, created.stream, created.size, (b) => {
        encryptProgress.value = created.size > 0 ? Math.min(100, Math.round((b / created.size) * 100)) : 100;
      });
      const fxePath = wireJoinPath(dir, stored);
      ring.set(fxePath, created.key);
      ringVer.value++;
      // Only now — the encrypted copy is committed — does the original go.
      if (purge) {
        try {
          await purgeOriginal(n, n.id as number);
        } catch (err) {
          encryptTarget.value = null;
          deps.emitError(messageOf(err), 'fxe-purge-original');
          deps.toast(t('e2e.fxe.purge_failed', { name: n.basename, reason: messageOf(err) }), true);
          deps.showRecoveryKey(created.recoveryKey, n.basename);
          await deps.reload();
          return;
        }
        encryptTarget.value = null;
        deps.showRecoveryKey(created.recoveryKey, n.basename);
        deps.toast(t('e2e.fxe.purged', { name: n.basename }));
        await deps.reload();
        return;
      }
      try {
        await trash(n.path);
      } catch (err) {
        encryptTarget.value = null;
        deps.emitError(messageOf(err), 'fxe-trash-original');
        deps.toast(t('e2e.fxe.original_kept', { name: n.basename }), true);
        deps.showRecoveryKey(created.recoveryKey, n.basename);
        await deps.reload();
        return;
      }
      encryptTarget.value = null;
      deps.showRecoveryKey(created.recoveryKey, n.basename);
      deps.toast(t('e2e.fxe.encrypted', { name: n.basename }));
      await deps.reload();
    } catch (err) {
      encryptError.value = err instanceof SaveTooLarge ? t('e2e.fxe.upload_too_big') : messageOf(err);
      deps.emitError(messageOf(err), 'fxe-encrypt');
    } finally {
      encryptBusy.value = false;
      encryptProgress.value = null;
    }
  }

  // ── open / download / remove: the unlock dialog ───────────────────────

  function askUnlock(n: FileNode, intent: FxeIntent) {
    unlockTarget.value = n;
    unlockIntent.value = intent;
    unlockError.value = null;
    unlockNeedsSave.value = false;
    unlockProgress.value = null;
    unlockHasRecovery.value = true;
    unlockEscrow.value = 'off';
    unlockEscrowKid.value = null;
    if (!ring.has(n.path)) void peekSlots(n);
  }

  /** Which doors this file has (recovery key, escrow), read from its header
   *  so the dialog offers the right ones before anything is typed. */
  async function peekSlots(n: FileNode) {
    try {
      const { header, body } = await readHeader(n.path);
      await body.cancel().catch(() => undefined);
      if (unlockTarget.value?.path !== n.path) return;
      unlockHasRecovery.value = fxeHasRecovery(header);
      unlockEscrow.value = escrowAvailability(fxeSlots(header), deps.escrowKid?.() ?? null);
      unlockEscrowKid.value = header.esc?.kid ?? null;
    } catch {
      /* the submit reads the header again and says what is wrong */
    }
  }

  /**
   * The escrow key was used: prove it to the server (open its challenge with
   * the private key) so the file's OWNER is told — BEFORE anything is
   * decrypted, and a failure here opens nothing. The same rule, and the same
   * two requests, as an encrypted folder's escrow unlock.
   *
   * ⚠ An announcement, not enforcement: whoever holds the private key can
   * decrypt the file offline and never come here (docs/E2E-ENCRYPTION.md).
   */
  async function announceEscrow(path: string, priv: CryptoKey): Promise<void> {
    const ch = await api.e2eEscrowChallenge(path);
    const nonce = new Uint8Array(
      await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, priv, b64ToBytes(ch.challenge).buffer as ArrayBuffer),
    );
    await api.e2eEscrowUsed({ path, id: ch.id, nonce: bytesToB64(nonce) });
  }

  function closeUnlock() {
    if (unlockBusy.value) return;
    unlockTarget.value = null;
    unlockNeedsSave.value = false;
  }

  /** Open a `.fxe` (double click, Enter, Preview). */
  async function open(n: FileNode) {
    const key = ring.get(n.path);
    if (!key) {
      askUnlock(n, 'open');
      return;
    }
    try {
      await previewWith(n, key);
    } catch (err) {
      failAndMaybeForget(n, err, 'fxe-open');
    }
  }

  /** Download a `.fxe` decrypted, under its original name. */
  async function download(n: FileNode) {
    if (await refusedForSize(n)) return;
    const key = ring.get(n.path);
    if (!key) {
      askUnlock(n, 'download');
      return;
    }
    try {
      await saveDecrypted(n, key);
    } catch (err) {
      if (err instanceof SaveNeedsGesture) {
        askUnlock(n, 'download');
        unlockNeedsSave.value = true;
        return;
      }
      failAndMaybeForget(n, err, 'fxe-download');
    }
  }

  /** "Remove encryption…": always through the dialog, which says what it means. */
  function startRemove(n: FileNode) {
    askUnlock(n, 'remove');
  }

  /** A command-line argument, quoted for a shell: the name as it is saved. */
  function shellArg(name: string): string {
    return `"${name.replace(/(["\\$`])/g, '\\$1')}"`;
  }

  /** The dialog for a `.fxe` too big to decrypt in memory here. */
  function tooBigFile(n: FileNode, size: number, limit: number) {
    const stored = wireBaseName(n.path);
    tooBig.value = {
      name: ring.get(n.path)?.name ?? n.basename,
      size,
      limit,
      encrypted: { kind: 'file', path: n.path },
      command: `filex decrypt ${shellArg(stored)}`,
    };
  }

  /** The dialog for a file (or a zip) of an encrypted folder: the way out is
   *  the encrypted folder as a zip, which carries its key file. */
  function tooBigFolder(name: string, root: string, size: number | null, limit: number) {
    const folder = wireBaseName(root) || 'filex';
    tooBig.value = {
      name,
      size,
      limit,
      encrypted: { kind: 'folder', path: root },
      command: `filex decrypt ${shellArg(`${folder}.zip`)}`,
    };
  }

  function closeTooBig() {
    tooBig.value = null;
  }

  /**
   * In a browser that saves from memory, a `.fxe` over the limit is refused
   * BEFORE its password is asked for: the header (read without any key) says
   * how big the plaintext is. True when the dialog was shown instead.
   */
  async function refusedForSize(n: FileNode): Promise<boolean> {
    if (streamingSaveAvailable()) return false;
    const known = ring.get(n.path)?.header.size;
    let size = typeof known === 'number' ? known : null;
    if (size === null) {
      if (typeof n.size !== 'number' || n.size <= E2E_BLOB_SAVE_LIMIT) return false;
      try {
        const { header, body } = await readHeader(n.path);
        await body.cancel().catch(() => undefined);
        size = header.size;
      } catch {
        return false;
      }
    }
    if (size <= E2E_BLOB_SAVE_LIMIT) return false;
    tooBigFile(n, size, E2E_BLOB_SAVE_LIMIT);
    return true;
  }

  /** A known key that no longer opens the file: forget it and ask again. */
  function failAndMaybeForget(n: FileNode, err: unknown, op: string) {
    if (err instanceof SaveCancelled) return;
    if (err instanceof SaveTooLarge) {
      tooBigFile(n, err.size, err.limit);
      return;
    }
    if (err instanceof E2eDecryptError) {
      ring.delete(n.path);
      ringVer.value++;
    }
    deps.emitError(messageOf(err), op);
    deps.toast(decryptWords(err), true);
  }

  async function previewWith(n: FileNode, key: FxeKey) {
    const { header, body } = await readHeader(n.path);
    if (!keyFits(key, header)) {
      await body.cancel().catch(() => undefined);
      throw new E2eDecryptError('e2e: the file changed since it was opened');
    }
    if (header.size > E2E_BLOB_SAVE_LIMIT) {
      await body.cancel().catch(() => undefined);
      throw new SaveTooLarge(header.size, E2E_BLOB_SAVE_LIMIT);
    }
    const ext = extensionOf(key.name);
    const plain = await streamToFile(decryptFxeBody(key, body), key.name, e2eMimeForExt(ext));
    const old = urls.get(n.path);
    if (old) URL.revokeObjectURL(old);
    const url = URL.createObjectURL(plain);
    urls.set(n.path, url);
    // The viewer describes the FILE it shows: its real name, type and size
    // (the row's size is the .fxe's, header and tags included).
    deps.openPreview(
      { ...n, basename: key.name, extension: ext, mime_type: e2eMimeForExt(ext), size: header.size, fxe_name: key.name },
      url,
    );
  }

  async function saveDecrypted(n: FileNode, key: FxeKey, target?: SaveTarget) {
    const into = target ?? (await pickSaveTarget(key.name, key.header.size));
    let started = false;
    try {
      const { header, body } = await readHeader(n.path);
      if (!keyFits(key, header)) {
        await body.cancel().catch(() => undefined);
        throw new E2eDecryptError('e2e: the file changed since it was opened');
      }
      started = true;
      await into.write(decryptFxeBody(key, body), { mime: e2eMimeForExt(extensionOf(key.name)) });
      deps.toast(t('e2e.fxe.downloaded', { name: key.name }));
    } catch (err) {
      if (!started) await into.discard();
      throw err;
    }
  }

  async function removeWith(n: FileNode, key: FxeKey) {
    const dir = wireParentPath(n.path);
    const { header, body } = await readHeader(n.path);
    if (!keyFits(key, header)) {
      await body.cancel().catch(() => undefined);
      throw new E2eDecryptError('e2e: the file changed since it was opened');
    }
    const name = freeName(key.name, await takenIn(dir));
    unlockProgress.value = 0;
    await uploadStream(dir, name, decryptFxeBody(key, body), header.size, (b) => {
      unlockProgress.value = header.size > 0 ? Math.min(100, Math.round((b / header.size) * 100)) : 100;
    });
    await trash(n.path);
    ring.delete(n.path);
    ringVer.value++;
    deps.toast(t('e2e.fxe.removed', { name }));
    await deps.reload();
  }

  /**
   * The dialog's answer: a password or the recovery key, or — for a file
   * already open in this tab — just "go ahead".
   */
  async function submitUnlock(payload: { password?: string; recoveryKey?: string; escrowKey?: string } = {}) {
    const n = unlockTarget.value;
    if (!n || unlockBusy.value) return;
    unlockBusy.value = true;
    unlockError.value = null;
    try {
      let key = ring.get(n.path);
      if (!key || payload.password || payload.recoveryKey || payload.escrowKey) {
        let cred: FxeCredential;
        if (payload.escrowKey) {
          try {
            cred = { escrowKey: await importEscrowPrivateKey(payload.escrowKey) };
          } catch {
            unlockError.value = t('e2e.recover.bad_escrow_key');
            return;
          }
        } else if (payload.recoveryKey) {
          cred = { recoveryKey: payload.recoveryKey };
        } else {
          cred = { password: payload.password ?? '' };
        }
        const { header, unsupported, body } = await readHeader(n.path);
        await body.cancel().catch(() => undefined);
        unlockHasRecovery.value = fxeHasRecovery(header);
        if (unsupported.length > 0) {
          unlockError.value = t('e2e.fxe.unsupported', { features: unsupported.join(', ') });
          return;
        }
        const u = await unlockFxe(header, cred);
        if ('error' in u) {
          unlockError.value =
            u.error === 'damaged'
              ? t('e2e.fxe.damaged')
              : 'escrowKey' in cred
                ? t('e2e.fxe.wrong_escrow')
                : 'recoveryKey' in cred
                  ? t('e2e.fxe.wrong_recovery')
                  : t('e2e.fxe.wrong_password');
          return;
        }
        if ('escrowKey' in cred) {
          try {
            await announceEscrow(n.path, cred.escrowKey);
          } catch (err) {
            unlockError.value = t('e2e.fxe.escrow_notify_failed');
            deps.emitError(messageOf(err), 'fxe-escrow-notify');
            return;
          }
          deps.toast(t('e2e.fxe.escrow_done'));
        }
        key = u.key;
        ring.set(n.path, key);
        ringVer.value++;
      }
      if (unlockIntent.value === 'open') {
        await previewWith(n, key);
        unlockTarget.value = null;
      } else if (unlockIntent.value === 'download') {
        try {
          await saveDecrypted(n, key);
        } catch (err) {
          if (err instanceof SaveNeedsGesture) {
            unlockNeedsSave.value = true;
            return;
          }
          throw err;
        }
        unlockTarget.value = null;
        unlockNeedsSave.value = false;
      } else {
        await removeWith(n, key);
        unlockTarget.value = null;
      }
    } catch (err) {
      if (err instanceof SaveCancelled) return;
      if (err instanceof SaveTooLarge && unlockIntent.value === 'download') {
        unlockTarget.value = null;
        tooBigFile(n, err.size, err.limit);
        return;
      }
      unlockError.value =
        err instanceof SaveTooLarge
          ? t('e2e.dl.too_big', { size: formatBytes(err.size), limit: formatBytes(err.limit) })
          : decryptWords(err);
      if (err instanceof E2eDecryptError) {
        ring.delete(n.path);
        ringVer.value++;
      }
      deps.emitError(messageOf(err), `fxe-${unlockIntent.value}`);
    } finally {
      unlockBusy.value = false;
      unlockProgress.value = null;
    }
  }

  // ── change the password ───────────────────────────────────────────────

  function startPassword(n: FileNode) {
    pwTarget.value = n;
    pwError.value = null;
  }

  function closePassword() {
    if (pwBusy.value) return;
    pwTarget.value = null;
  }

  /** A new header; the body is re-sent unread (streamed). */
  /**
   * Tell the server the file's password was changed — once the new header is
   * written — so the audit log records it and the file's OWNER is notified
   * (POST /api/files/e2e/password-changed, the same announcement as a
   * folder's). A failed announcement is said, not thrown: the password HAS
   * changed. The server also sees the rewrite itself (e2e.fxe_header_rewritten).
   */
  async function announcePassword(path: string, via: 'password' | 'recovery_key') {
    if (!api.endpoints.e2ePasswordChanged) return;
    try {
      await api.e2ePasswordChanged({ path, via, rekey: false });
    } catch (err) {
      deps.emitError(messageOf(err), 'fxe-password-announce');
      deps.toast(t('e2e.password.announce_failed'), true);
    }
  }

  async function submitPassword(payload: { proof: E2eCredential | null; newPassword: string }) {
    const n = pwTarget.value;
    if (!n || pwBusy.value) return;
    if (!payload.proof) {
      pwError.value = t('e2e.password.current_required');
      return;
    }
    pwBusy.value = true;
    pwError.value = null;
    const via = 'recoveryKey' in payload.proof ? 'recovery_key' : 'password';
    try {
      const { header, unsupported, body } = await readHeader(n.path);
      if (unsupported.length > 0) {
        await body.cancel().catch(() => undefined);
        pwError.value = t('e2e.fxe.unsupported', { features: unsupported.join(', ') });
        return;
      }
      let next: FxeHeader;
      try {
        next = await changeFxePassword(header, payload.proof, payload.newPassword);
      } catch (err) {
        await body.cancel().catch(() => undefined);
        if (err instanceof E2eDecryptError) {
          pwError.value = via === 'recovery_key' ? t('e2e.password.wrong_recovery') : t('e2e.password.wrong_current');
          return;
        }
        throw err;
      }
      const out = replaceFxeHeader(next, body);
      await uploadStream(wireParentPath(n.path), wireBaseName(n.path), out.stream, out.size);
      const known = ring.get(n.path);
      if (known) ring.set(n.path, rekeyedFxeKey(known, next));
      ringVer.value++;
      pwTarget.value = null;
      deps.toast(t('e2e.fxe.password_done'));
      await announcePassword(n.path, via);
      await deps.reload();
    } catch (err) {
      pwError.value = t('e2e.password.failed');
      deps.emitError(messageOf(err), 'fxe-password');
    } finally {
      pwBusy.value = false;
    }
  }

  // ── encrypted folders: streamed download, zip, large uploads ──────────

  /**
   * One file of an unlocked encrypted folder, decrypted as it arrives into
   * the save sink (either header version; a file never encrypted comes down
   * as it is).
   */
  async function downloadFolderFile(
    n: FileNode,
    fmk: CryptoKey,
    previous: CryptoKey | null | undefined,
    /** The encrypted folder it is in: what to download instead when it is too big here. */
    root?: string | null,
  ) {
    const tooBigHere = (err: SaveTooLarge) => {
      if (root) tooBigFolder(n.basename, root, err.size, err.limit);
      else deps.toast(t('e2e.dl.too_big', { size: formatBytes(err.size), limit: formatBytes(err.limit) }), true);
    };
    let target: SaveTarget;
    try {
      target = await pickSaveTarget(n.basename, n.size);
    } catch (err) {
      if (err instanceof SaveTooLarge) tooBigHere(err);
      else failAndMaybeForget(n, err, 'e2e-download');
      return;
    }
    try {
      const plain = decryptFolderFileStream(fmk, previous, await fetchBody(n.path), { passPlain: true });
      await target.write(plain, { mime: e2eMimeForExt(n.extension) });
    } catch (err) {
      await target.discard();
      if (err instanceof SaveCancelled) return;
      if (err instanceof SaveTooLarge) {
        tooBigHere(err);
        return;
      }
      deps.emitError(messageOf(err), 'e2e-download');
      deps.toast(t('e2e.download.failed'), true);
    }
  }

  /**
   * A zip of decrypted content with PLAINTEXT names, made in this tab: the
   * selection inside an unlocked encrypted folder, or the encrypted folder
   * itself. Names come from the name view (`decorate`), never re-derived
   * here; the key file and long-name sidecars are left out.
   */
  async function downloadDecryptedZip(
    targets: FileNode[],
    ctx: {
      root: string;
      fmk: CryptoKey;
      previous: CryptoKey | null | undefined;
      decorate: (rows: FileNode[], opts: { root: string }) => Promise<FileNode[]>;
      zipName: string;
    },
  ) {
    let target: SaveTarget;
    try {
      target = await pickSaveTarget(ctx.zipName);
    } catch (err) {
      if (err instanceof SaveCancelled) return;
      if (err instanceof SaveNeedsGesture) {
        deps.toast(t('e2e.dl.click_again'), true);
        return;
      }
      throw err;
    }
    let plainFiles = 0;
    const fileEntry = (r: FileNode, name: string): ZipEntry => ({
      name,
      sizeHint: typeof r.size === 'number' ? r.size : undefined,
      mtime: typeof r.last_modified === 'number' ? new Date(r.last_modified) : undefined,
      data: async () =>
        decryptFolderFileStream(ctx.fmk, ctx.previous, await fetchBody(r.path), {
          passPlain: true,
          onPlain: () => plainFiles++,
        }),
    });
    const labelOf = (r: FileNode) =>
      r.e2e_name_state === 'locked' || r.e2e_name_state === 'unreadable'
        ? (typeof r.e2e_stored === 'string' ? r.e2e_stored : r.basename)
        : r.basename;
    async function* walk(dirNode: FileNode, prefix: string): AsyncGenerator<ZipEntry> {
      yield { name: `${prefix}/` };
      const resp = await api.index(dirNode.path);
      const rows = await ctx.decorate(resp.files ?? [], { root: ctx.root });
      const taken = new Set<string>();
      for (const r of rows) {
        if (r.basename === E2E_MARKER_NAME || (typeof r.e2e_stored !== 'string' && r.basename === E2E_MARKER_NAME)) continue;
        const name = freeName(labelOf(r), taken);
        taken.add(name);
        if (r.type === 'dir') yield* walk(r, `${prefix}/${name}`);
        else yield fileEntry(r, `${prefix}/${name}`);
      }
    }
    async function* all(): AsyncGenerator<ZipEntry> {
      const taken = new Set<string>();
      for (const n of targets) {
        const name = freeName(labelOf(n), taken);
        taken.add(name);
        if (n.type === 'dir') yield* walk(n, name);
        else yield fileEntry(n, name);
      }
    }
    deps.toast(t('e2e.dl.zip_started', { name: ctx.zipName }));
    try {
      await target.write(createZipStream(all()), { mime: 'application/zip' });
      deps.toast(
        plainFiles > 0 ? t('e2e.dl.zip_done_plain', { name: ctx.zipName, n: plainFiles }) : t('e2e.dl.zip_done', { name: ctx.zipName }),
      );
    } catch (err) {
      await target.discard();
      if (err instanceof SaveCancelled) return;
      if (err instanceof SaveTooLarge) {
        // A zip's size is not known up front: it outgrew the memory here.
        tooBigFolder(ctx.zipName, ctx.root, null, err.limit);
        return;
      }
      deps.emitError(messageOf(err), 'e2e-download-zip');
      deps.toast(err instanceof E2eDecryptError ? t('e2e.dl.zip_damaged') : t('e2e.download.failed'), true);
    }
  }

  /**
   * A file over the one-shot limit, going into an encrypted folder: a 0x02
   * STREAM file, encrypted as it is uploaded. Returns the upload source and
   * its size; the caller names the upload.
   */
  async function folderUploadSource(file: File, fmk: CryptoKey): Promise<UploadSource> {
    const enc = await encryptFolderFileStream(fmk, file.size, file.stream() as ReadableStream<Uint8Array>);
    return streamUploadSource(enc.stream, enc.size);
  }

  /**
   * wiring:e2 convert - a file over the one-shot limit, encrypted IN PLACE
   * (docs/E2E-ENCRYPTION.md → "Encrypting a folder you already have"): read
   * from the server as a stream, encrypted as a STREAM (0x02) file as it is
   * read, and written over itself as a conversion write - on condition that
   * it is still the file listed (`expect`). Nothing is held but the chunks in
   * flight, whatever the size.
   *
   * Staged where the server has it. Where it has not, a single POST, which
   * has to be gathered in memory first: up to E2E_BLOB_SAVE_LIMIT, and above
   * that this resolves FALSE - this browser cannot convert it on this server,
   * and the file is left as it is (the conversion says so and stays open).
   */
  async function convertLarge(
    row: { path: string; basename: string; size?: number },
    fmk: CryptoKey,
    expect: string | null,
    opts: { signal?: AbortSignal; onProgress?: (fraction: number) => void } = {},
  ): Promise<boolean> {
    const size = row.size;
    if (typeof size !== 'number' || !Number.isSafeInteger(size) || size < 0) {
      throw new Error('e2e: unknown file size');
    }
    const enc = await encryptFolderFileStream(fmk, size, await fetchBody(row.path));
    const progress = opts.onProgress
      ? (sent: number) => opts.onProgress!(enc.size > 0 ? Math.min(1, sent / enc.size) : 1)
      : undefined;
    try {
      await uploadStream(wireParentPath(row.path), row.basename, enc.stream, enc.size, progress, {
        fields: { e2e_convert: '1', ...(expect ? { expect } : {}) },
        signal: opts.signal,
      });
    } catch (err) {
      if (err instanceof SaveTooLarge) return false;
      throw err;
    }
    return true;
  }

  /**
   * Re-wrap a large folder file's key during a re-key without reading it into
   * memory: the 97-byte header is re-wrapped, the body is re-sent unread.
   * True for an encrypted file (re-wrapped now, or already under the new
   * key); false for one that was never encrypted (written over WebDAV).
   */
  async function rewrapLarge(row: FileNode, previous: CryptoKey, fmk: CryptoKey): Promise<boolean> {
    const reader = new ByteStreamReader(await fetchBody(row.path));
    const head = await reader.read(E2E_FOLDER_HEADER_LEN);
    if (!hasMagic(head)) {
      await reader.cancel().catch(() => undefined);
      return false;
    }
    const out = await rewrapFileKey(head.buffer, previous, fmk);
    if (!out) {
      await reader.cancel().catch(() => undefined);
      return true;
    }
    const size = typeof row.size === 'number' ? row.size : NaN;
    if (!Number.isSafeInteger(size)) {
      await reader.cancel().catch(() => undefined);
      throw new Error('e2e: unknown file size');
    }
    const stored = typeof row.e2e_stored === 'string' ? row.e2e_stored : row.basename;
    await uploadStream(wireParentPath(row.path), stored, prependBytes(new Uint8Array(out), reader.rest()), size);
    return true;
  }

  /** Sizes in words, the one way the explorer writes them (useLocale). */
  const { formatSize: formatBytes } = useLocale(() => deps.locale() ?? 'en');

  /** Answer a menu key. False when it is not ours. */
  async function dispatch(key: string, targets: FileNode[], downloadEncryptedCopy: (t: FileNode[]) => Promise<void> | void): Promise<boolean> {
    const n = targets[0];
    switch (key) {
      case 'fxe-encrypt':
        if (n) startEncrypt(n);
        return true;
      case 'fxe-download-raw':
        if (n) window.open(api.downloadUrl(n.path), '_blank');
        return true;
      case 'fxe-password':
        if (n) startPassword(n);
        return true;
      case 'fxe-remove':
        if (n) startRemove(n);
        return true;
      case 'e2e-download-encrypted':
        await downloadEncryptedCopy(targets);
        return true;
    }
    return false;
  }

  return {
    ringVer,
    realName,
    isHiddenFxeName,
    menuRows,
    dispatch,
    // encrypt
    encryptTarget,
    encryptBusy,
    encryptError,
    encryptProgress,
    startEncrypt,
    closeEncrypt,
    submitEncrypt,
    // open / download / remove
    unlockTarget,
    unlockIntent,
    unlockBusy,
    unlockError,
    unlockProgress,
    unlockNeedsSave,
    unlockHasRecovery,
    unlockEscrow,
    unlockEscrowKid,
    open,
    download,
    startRemove,
    closeUnlock,
    submitUnlock,
    // password
    pwTarget,
    pwBusy,
    pwError,
    // too big for this browser
    tooBig,
    closeTooBig,
    startPassword,
    closePassword,
    submitPassword,
    // encrypted folders
    downloadFolderFile,
    downloadDecryptedZip,
    folderUploadSource,
    convertLarge,
    rewrapLarge,
    revokeAll,
  };
}

export type E2eFiles = ReturnType<typeof useE2eFiles>;
