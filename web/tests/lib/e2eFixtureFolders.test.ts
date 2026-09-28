// Encrypted folders the BROWSER code made, frozen on disk for the Go decryptor.
//
// `filex decrypt` (backend/internal/e2edecrypt) is a second implementation of
// everything in packages/core/src/lib/e2ecrypto.ts + e2enames.ts. The only
// claim that matters about it is "what the browser encrypted, the CLI
// decrypts" — so its tests do not encrypt anything themselves. They read the
// folders under backend/internal/e2edecrypt/testdata/folders/, which this
// file wrote with:
//
//   v1-0.30.1   the FROZEN v0.30.1 module (tests/fixtures/…-v0.30.1.ts)
//   v2-0.47.0   the FROZEN v0.47.0 module (tests/fixtures/…-v0.47.0.ts)
//   v3-names    this build, a folder created with encrypted names
//   v3-pending  this build, a v2 folder half-way through the switch to
//               encrypted names (some entries renamed, some not yet)
//   v2-password-changed  this build, a folder whose password was changed
//               (`changePassword`): the new password opens it, the old one
//               must not
//   v3-rekey-pending     a v0.30.1 folder whose password change re-keyed it
//               (`startRekey`) and stopped half-way: one file re-wrapped under
//               the new folder key, one still under the previous key
//   v2-stream   this build, files in the STREAM format (header 0x02, what a
//               file over 200 MB is written as) next to a 0x01 file — at a
//               small chunk size the format records, so a few KB span many
//               chunks. Readable names on purpose: it pins the content
//               format, independent of how names are encrypted
//   fxe/        single encrypted files (`.fxe`), one under its own name and
//               one with the name hidden (`encrypted-<hex>.fxe`); their
//               secrets are under `fxe` in secrets.json
//
// secrets.json next to them holds each case's password, recovery key and the
// expected plaintext tree (path → SHA-256 of the content).
//
// Regenerating is deliberate, never incidental:
//
//   FILEX_WRITE_E2E_FIXTURES=1 npx vitest run tests/lib/e2eFixtureFolders.test.ts
//
// The second test runs every time: it opens the committed folders with the
// CURRENT JS code and checks them against secrets.json. So if this build ever
// stops reading what an earlier one wrote, it fails here — the same frozen
// output then fails in Go if the CLI drifts.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  changePassword,
  createEncryptedFolder,
  decryptFileAny,
  enableNames,
  rewrapFileKey,
  startRekey,
  unlockPrevious,
  encryptFile,
  hasMagic,
  markerHasNames,
  parseMarker,
  unlockNameKey,
  unlockWithPassword,
  unlockWithRecoveryKey,
  E2E_MARKER_NAME,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import {
  decryptStoredName,
  deriveDirId,
  effectiveDirId,
  encryptName,
  type E2eNameKey,
} from '../../../packages/core/src/lib/e2enames';

import * as legacy030 from '../fixtures/e2ecrypto-legacy-v0.30.1';
import * as legacy047 from '../fixtures/e2ecrypto-legacy-v0.47.0';
/* wiring:e2 stream + fxe — the STREAM folder file and the single encrypted file. */
import { bytesStream, collectBytes, encryptFolderFileStream } from '../../../packages/core/src/lib/e2estream';
import {
  createFxe,
  decryptFxeBody,
  fxeStoredName,
  readFxe,
  unlockFxe,
} from '../../../packages/core/src/lib/e2efile';

const ROOT = path.resolve(__dirname, '../../../backend/internal/e2edecrypt/testdata/folders');
const SECRETS = path.join(ROOT, 'secrets.json');
const SLOW = 120_000;
const enc = new TextEncoder();

type Tree = Record<string, string>; // plaintext rel path → sha256 hex, or 'dir' (key ends with '/')

interface FixtureCase {
  password: string;
  /** A password this folder USED to have; it must open nothing now. */
  old_password?: string;
  recovery_key: string | null;
  marker_v: number;
  names: boolean;
  /** How many warnings `filex decrypt` must print (never-encrypted names/content). */
  warnings: number;
  tree: Tree;
}

/** One `.fxe` under fxe/, by its stored name. */
interface FxeCase {
  password: string;
  recovery_key: string;
  /** The original name the header seals. */
  name: string;
  /** SHA-256 of the plaintext. */
  sha256: string;
  size: number;
}

interface Secrets {
  comment: string;
  cases: Record<string, FixtureCase>;
  fxe?: Record<string, FxeCase>;
}

const FXE_DIR = path.join(ROOT, 'fxe');

function sha(b: Uint8Array): string {
  return createHash('sha256').update(b).digest('hex');
}

function bytes(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

function ab(b: Uint8Array): ArrayBuffer {
  return new Uint8Array(b).buffer as ArrayBuffer;
}

/** Writes one case's folder. `names` null = plaintext names (v1/v2). */
class FolderWriter {
  readonly tree: Tree = {};
  /** Each folder's stored name and id, by plaintext path: made once, reused. */
  private readonly folders = new Map<string, { stored: string; id: Uint8Array }>();
  constructor(
    readonly dir: string,
    readonly key: CryptoKey,
    readonly names: E2eNameKey | null,
  ) {
    fs.mkdirSync(dir, { recursive: true });
  }

  writeMarker(m: E2eMarker) {
    fs.writeFileSync(path.join(this.dir, E2E_MARKER_NAME), JSON.stringify(m));
  }

  /**
   * Stored path for a plaintext path, the way the browser spells it: every
   * name sealed for the folder it is in (lib/e2enames → folder ids). A new
   * folder gets a random id; a folder left plaintext (`plainAt`, segment
   * indexes) has the derived id its contents are sealed under meanwhile.
   */
  private async storedPath(parts: string[], plainAt: Set<number>, lastIsDir: boolean): Promise<string> {
    const out: string[] = [];
    const names = this.names;
    let parentId = names?.rootId;
    for (let i = 0; i < parts.length; i++) {
      if (!names || !parentId) {
        out.push(parts[i]);
        continue;
      }
      const isDir = i < parts.length - 1 || lastIsDir;
      if (isDir) {
        const key = parts.slice(0, i + 1).join('/');
        let known = this.folders.get(key);
        if (!known) {
          if (plainAt.has(i)) {
            known = { stored: parts[i], id: await deriveDirId(names, parentId, parts[i]) };
          } else {
            const e = await encryptName(names, parts[i], parentId, { isDir: true });
            if (e.sidecar) fs.writeFileSync(path.join(this.dir, ...out, e.sidecar.name), e.sidecar.content);
            known = { stored: e.stored, id: e.dirId! };
          }
          this.folders.set(key, known);
        }
        out.push(known.stored);
        parentId = known.id;
        continue;
      }
      if (plainAt.has(i)) {
        out.push(parts[i]);
        continue;
      }
      const e = await encryptName(names, parts[i], parentId);
      if (e.sidecar) fs.writeFileSync(path.join(this.dir, ...out, e.sidecar.name), e.sidecar.content);
      out.push(e.stored);
    }
    return path.join(this.dir, ...out);
  }

  async mkdir(rel: string, plainAt: number[] = []) {
    const p = await this.storedPath(rel.split('/'), new Set(plainAt), true);
    fs.mkdirSync(p, { recursive: true });
    this.tree[rel + '/'] = 'dir';
  }

  /** content encrypted with the folder's key unless `plainContent`. */
  async file(rel: string, content: Uint8Array, opts: { plainAt?: number[]; plainContent?: boolean } = {}) {
    const p = await this.storedPath(rel.split('/'), new Set(opts.plainAt ?? []), false);
    const body = opts.plainContent ? content : new Uint8Array(await encryptFile(this.key, ab(content)));
    fs.writeFileSync(p, body);
    this.tree[rel] = sha(content);
  }

  /* wiring:e2 stream — a STREAM (0x02) file, as a file over 200 MB is
     written, at a chunk size of 2^10 the header records. */
  async streamFile(rel: string, content: Uint8Array) {
    const p = await this.storedPath(rel.split('/'), new Set());
    const out = await encryptFolderFileStream(this.key, content.length, bytesStream(content, 1000), { chunkLog2: 10 });
    fs.writeFileSync(p, await collectBytes(out.stream));
    this.tree[rel] = sha(content);
  }
}

/** The cases on disk now, so a regeneration can add a case without
 *  re-randomising the ones already committed (FILEX_WRITE_E2E_FIXTURES=all
 *  rebuilds everything). */
function existingCases(): Record<string, FixtureCase> {
  if (process.env.FILEX_WRITE_E2E_FIXTURES === 'all' || !fs.existsSync(SECRETS)) return {};
  return (JSON.parse(fs.readFileSync(SECRETS, 'utf8')) as Secrets).cases;
}

/* wiring:e2 fxe — the committed single files, kept like the cases are. */
function existingFxe(): Record<string, FxeCase> {
  if (process.env.FILEX_WRITE_E2E_FIXTURES === 'all' || !fs.existsSync(SECRETS)) return {};
  return (JSON.parse(fs.readFileSync(SECRETS, 'utf8')) as Secrets).fxe ?? {};
}

async function generate(): Promise<Secrets> {
  const cases: Record<string, FixtureCase> = existingCases();
  if (Object.keys(cases).length === 0) {
    fs.rmSync(ROOT, { recursive: true, force: true });
  }
  fs.mkdirSync(ROOT, { recursive: true });
  const want = (name: string) => !cases[name] || !fs.existsSync(path.join(ROOT, name));

  // v1 — the frozen v0.30.1 module: no slots, the FMK is the password KEK.
  if (want('v1-0.30.1')) {
    const pw = 'fixture-v1-password';
    const made = await legacy030.createMarker(pw);
    const w = new FolderWriter(path.join(ROOT, 'v1-0.30.1'), made.kek, null);
    w.writeMarker(made.marker as E2eMarker);
    // The frozen module's own encryptFile, so the bytes are 0.30.1's.
    const put = async (rel: string, content: Uint8Array) => {
      const p = path.join(w.dir, ...rel.split('/'));
      fs.mkdirSync(path.dirname(p), { recursive: true });
      fs.writeFileSync(p, new Uint8Array(await legacy030.encryptFile(made.kek, ab(content))));
      w.tree[rel] = sha(content);
    };
    await put('notes.txt', enc.encode('v1 notes, written by filex 0.30.1\n'));
    w.tree['sub/'] = 'dir';
    await put('sub/deep.bin', bytes(4096, 1));
    await put('sub/empty.txt', new Uint8Array(0));
    cases['v1-0.30.1'] = { password: pw, recovery_key: null, marker_v: 1, names: false, warnings: 0, tree: w.tree };
  }

  // v2 — the frozen v0.47.0 module: wrapped FMK, recovery slot, plain names.
  if (want('v2-0.47.0')) {
    const pw = 'fixture-v2-password';
    const made = await legacy047.createEncryptedFolder(pw);
    const w = new FolderWriter(path.join(ROOT, 'v2-0.47.0'), made.fmk, null);
    w.writeMarker(made.marker as E2eMarker);
    const put = async (rel: string, content: Uint8Array) => {
      const p = path.join(w.dir, ...rel.split('/'));
      fs.mkdirSync(path.dirname(p), { recursive: true });
      fs.writeFileSync(p, new Uint8Array(await legacy047.encryptFile(made.fmk, ab(content))));
      w.tree[rel] = sha(content);
    };
    await put('report.txt', enc.encode('quarterly report, filex 0.47.0\n'));
    w.tree['photos/'] = 'dir';
    await put('photos/pixel.bin', bytes(70_000, 2));
    await put('empty.txt', new Uint8Array(0));
    cases['v2-0.47.0'] = {
      password: pw,
      recovery_key: made.recoveryKey,
      marker_v: 2,
      names: false,
      warnings: 0,
      tree: w.tree,
    };
  }

  // v3 — this build, created with encrypted names.
  if (want('v3-names')) {
    const pw = 'fixture-v3-password';
    const made = await createEncryptedFolder(pw, { encryptNames: true });
    const w = new FolderWriter(path.join(ROOT, 'v3-names'), made.fmk, made.names!);
    w.writeMarker(made.marker);
    await w.file('Bütçe 2027 — İzmir.xlsx', bytes(5000, 3));
    await w.mkdir('Sözleşmeler');
    await w.file('Sözleşmeler/Kira sözleşmesi.pdf', bytes(12_345, 4));
    await w.mkdir('Sözleşmeler/Eski');
    await w.file('Sözleşmeler/Eski/2019.txt', enc.encode('eski sözleşme\n'));
    // The same name in two folders: two different stored names.
    await w.mkdir('Sözleşmeler/2024');
    await w.file('Sözleşmeler/2024/fatura.pdf', bytes(900, 6));
    await w.mkdir('Sözleşmeler/2025');
    await w.file('Sözleşmeler/2025/fatura.pdf', bytes(901, 7));
    await w.file('boş.txt', new Uint8Array(0));
    await w.file('.gizli', enc.encode('dotfile\n'));
    // Long names: over 149 UTF-8 bytes, so they are shortened + sidecar.
    await w.file('ş'.repeat(100) + '.txt', enc.encode('uzun adlı dosya\n'));
    const longDir = 'Klasör-'.repeat(25);
    await w.mkdir(longDir);
    await w.file(longDir + '/iç.txt', enc.encode('uzun adlı klasörün içi\n'));
    // Never encrypted at all: plain name, plain content (a WebDAV write).
    await w.file('stray.txt', enc.encode('written over WebDAV\n'), { plainAt: [0], plainContent: true });
    cases['v3-names'] = {
      password: pw,
      recovery_key: made.recoveryKey,
      marker_v: 3,
      names: true,
      warnings: 2, // stray.txt: name never encrypted + content never encrypted
      tree: w.tree,
    };
  }

  // v3 pending — a v2 folder this build made, then switched to encrypted
  // names; the rename pass stopped half-way.
  if (want('v3-pending')) {
    const pw = 'fixture-v3p-password';
    const made = await createEncryptedFolder(pw);
    const started = await enableNames(made.marker, made.fmk);
    const w = new FolderWriter(path.join(ROOT, 'v3-pending'), made.fmk, started.names);
    w.writeMarker(started.marker);
    await w.file('done.txt', enc.encode('renamed already\n'));
    await w.file('not-yet.txt', enc.encode('name still plaintext\n'), { plainAt: [0] });
    await w.mkdir('dir-done');
    await w.file('dir-done/inner.txt', enc.encode('inner, not reached yet\n'), { plainAt: [1] });
    await w.mkdir('dir-plain', [0]);
    await w.file('dir-plain/renamed.txt', enc.encode('renamed, parent not yet\n'), { plainAt: [0] });
    cases['v3-pending'] = {
      password: pw,
      recovery_key: made.recoveryKey,
      marker_v: 3,
      names: true,
      warnings: 3, // not-yet.txt, dir-done/inner.txt, dir-plain
      tree: w.tree,
    };
  }

  // A password change: the folder was made with one password and changed to
  // another. Only the key file changed; the files are what they were.
  if (want('v2-password-changed')) {
    const before = 'fixture-pw-before-change';
    const after = 'fixture-pw-after-change';
    const made = await createEncryptedFolder(before);
    const w = new FolderWriter(path.join(ROOT, 'v2-password-changed'), made.fmk, null);
    await w.file('before-the-change.txt', enc.encode('written under the first password\n'));
    await w.mkdir('arşiv');
    await w.file('arşiv/eski.bin', bytes(3000, 5));
    w.writeMarker(await changePassword(made.marker, { password: before }, after));
    cases['v2-password-changed'] = {
      password: after,
      old_password: before,
      recovery_key: made.recoveryKey,
      marker_v: 2,
      names: false,
      warnings: 0,
      tree: w.tree,
    };
  }

  // A re-key stopped half-way: a v0.30.1 folder whose password was changed
  // (which, for a pre-0.31 folder, replaces the folder key). One file is
  // re-wrapped under the new key, the other is still under the previous one;
  // the marker carries the previous key sealed under the new (`rekey`).
  if (want('v3-rekey-pending')) {
    const before = 'fixture-rekey-before';
    const after = 'fixture-rekey-after';
    const made = await legacy030.createMarker(before);
    const dir = path.join(ROOT, 'v3-rekey-pending');
    fs.mkdirSync(dir, { recursive: true });
    const tree: Tree = {};
    const one = enc.encode('re-wrapped already\n');
    const two = enc.encode('still under the previous key\n');
    const f1 = await legacy030.encryptFile(made.kek, ab(one));
    const f2 = await legacy030.encryptFile(made.kek, ab(two));
    const start = await startRekey(made.marker as E2eMarker, { password: before }, after);
    fs.writeFileSync(path.join(dir, 'done.txt'), new Uint8Array((await rewrapFileKey(f1, start.previous, start.fmk))!));
    fs.writeFileSync(path.join(dir, 'pending.txt'), new Uint8Array(f2));
    fs.writeFileSync(path.join(dir, E2E_MARKER_NAME), JSON.stringify(start.marker));
    tree['done.txt'] = sha(one);
    tree['pending.txt'] = sha(two);
    cases['v3-rekey-pending'] = {
      password: after,
      old_password: before,
      recovery_key: start.recoveryKey,
      marker_v: 3,
      names: false,
      warnings: 0,
      tree,
    };
  }

  /* wiring:e2 stream — two STREAM files (one of them empty, one exactly two
     chunks) and a 0x01 file side by side, in a content-only folder. */
  if (want('v2-stream')) {
    const pw = 'fixture-stream-password';
    const made = await createEncryptedFolder(pw, { encryptNames: false });
    const w = new FolderWriter(path.join(ROOT, 'v2-stream'), made.fmk, null);
    w.writeMarker(made.marker);
    await w.streamFile('Büyük video.mp4', bytes(9000, 21));
    await w.file('küçük.txt', enc.encode('tek parça, 0x01\n'));
    await w.mkdir('alt');
    await w.streamFile('alt/boş akış.bin', new Uint8Array(0));
    await w.streamFile('alt/tam iki parça.bin', bytes(2048, 22));
    cases['v2-stream'] = {
      password: pw,
      recovery_key: made.recoveryKey,
      marker_v: 2,
      names: false,
      warnings: 0,
      tree: w.tree,
    };
  }

  /* wiring:e2 fxe — single encrypted files, each with its own password. */
  const fxe: Record<string, FxeCase> = existingFxe();
  if (Object.keys(fxe).length === 0 || !fs.existsSync(FXE_DIR)) {
    fs.rmSync(FXE_DIR, { recursive: true, force: true });
    fs.mkdirSync(FXE_DIR, { recursive: true });
    const made: Array<[string, Uint8Array, boolean, string]> = [
      ['Rapor 2027.pdf', bytes(3000, 23), false, 'fixture-fxe-password'],
      ['Gizli sözleşme.docx', bytes(1500, 24), true, 'fixture-fxe-hidden-name'],
    ];
    for (const [name, content, hide, pw] of made) {
      const c = await createFxe(name, content.length, bytesStream(content, 700), pw, { chunkLog2: 10 });
      const stored = fxeStoredName(name, hide);
      fs.writeFileSync(path.join(FXE_DIR, stored), await collectBytes(c.stream));
      fxe[stored] = { password: pw, recovery_key: c.recoveryKey, name, sha256: sha(content), size: content.length };
    }
  }

  const secrets: Secrets = {
    comment:
      'Test fixtures only. Written by web/tests/lib/e2eFixtureFolders.test.ts (FILEX_WRITE_E2E_FIXTURES=1). ' +
      'Passwords and recovery keys here open nothing but these folders.',
    cases,
    fxe,
  };
  fs.writeFileSync(SECRETS, JSON.stringify(secrets, null, 2) + '\n');
  return secrets;
}

/** Read one committed case back with the CURRENT browser code. `previous` is
 *  the previous folder key of a folder mid re-key. */
async function readBack(
  dir: string,
  fmk: CryptoKey,
  names: E2eNameKey | null,
  previous: CryptoKey | null = null,
): Promise<Tree> {
  const tree: Tree = {};
  const walk = async (abs: string, rel: string, isRoot: boolean, dirId: Uint8Array | null) => {
    for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
      if (isRoot && ent.name === E2E_MARKER_NAME) continue;
      let plain = ent.name;
      if (names && dirId) {
        const got = await decryptStoredName(names, ent.name, dirId, async (side) => {
          const p = path.join(abs, side);
          return fs.existsSync(p) ? fs.readFileSync(p, 'utf8') : null;
        });
        if (got.state === 'sidecar') continue;
        if (got.state === 'unreadable' || got.name === null) throw new Error(`unreadable: ${rel}${ent.name}`);
        plain = got.name;
      }
      const r = rel + plain;
      if (ent.isDirectory()) {
        tree[r + '/'] = 'dir';
        const inner = names && dirId ? await effectiveDirId(names, dirId, ent.name) : null;
        await walk(path.join(abs, ent.name), r + '/', false, inner);
        continue;
      }
      const data = new Uint8Array(fs.readFileSync(path.join(abs, ent.name)));
      const content = hasMagic(data) ? new Uint8Array(await decryptFileAny(fmk, previous, ab(data))) : data;
      tree[r] = sha(content);
    }
  };
  await walk(dir, '', true, names?.rootId ?? null);
  return tree;
}

describe('encrypted-folder fixtures for the Go decryptor', () => {
  it.skipIf(!process.env.FILEX_WRITE_E2E_FIXTURES)(
    'regenerates the folders (FILEX_WRITE_E2E_FIXTURES=1)',
    async () => {
      const s = await generate();
      expect(Object.keys(s.cases)).toHaveLength(7);
      expect(Object.keys(s.fxe ?? {})).toHaveLength(2);
    },
    SLOW,
  );

  it(
    'the committed folders still open with this build, by password and by recovery key',
    async () => {
      const secrets = JSON.parse(fs.readFileSync(SECRETS, 'utf8')) as Secrets;
      expect(Object.keys(secrets.cases).sort()).toEqual([
        'v1-0.30.1',
        'v2-0.47.0',
        'v2-password-changed',
        'v2-stream',
        'v3-names',
        'v3-pending',
        'v3-rekey-pending',
      ]);
      for (const [name, c] of Object.entries(secrets.cases)) {
        const dir = path.join(ROOT, name);
        const marker = parseMarker(fs.readFileSync(path.join(dir, E2E_MARKER_NAME), 'utf8'));
        expect(marker, name).not.toBeNull();
        expect(marker!.v, name).toBe(c.marker_v);
        expect(markerHasNames(marker), name).toBe(c.names);

        const fmk = await unlockWithPassword(marker!, c.password);
        expect(fmk, name).not.toBeNull();
        const nk = c.names ? await unlockNameKey(marker!, fmk!) : null;
        const prev = await unlockPrevious(marker!, fmk!);
        expect(await readBack(dir, fmk!, nk, prev), name).toEqual(c.tree);

        if (c.recovery_key) {
          const byRk = await unlockWithRecoveryKey(marker!, c.recovery_key);
          expect(byRk, name).not.toBeNull();
          const nk2 = c.names ? await unlockNameKey(marker!, byRk!) : null;
          expect(await readBack(dir, byRk!, nk2, await unlockPrevious(marker!, byRk!)), name).toEqual(c.tree);
        }
        expect(await unlockWithPassword(marker!, c.password + 'x'), name).toBeNull();
        if (c.old_password) {
          expect(await unlockWithPassword(marker!, c.old_password), `${name}: the old password`).toBeNull();
        }
      }
    },
    SLOW,
  );

  /* wiring:e2 stream + fxe */
  it('v2-stream really holds STREAM files, and 0x01 beside them', () => {
    const versions: number[] = [];
    const walk = (abs: string) => {
      for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
        const p = path.join(abs, ent.name);
        if (ent.isDirectory()) walk(p);
        else if (ent.name !== E2E_MARKER_NAME) versions.push(fs.readFileSync(p)[8]);
      }
    };
    walk(path.join(ROOT, 'v2-stream'));
    expect(versions.sort()).toEqual([1, 2, 2, 2]);
  });

  it(
    'the committed .fxe files open with this build, by password and by recovery key',
    async () => {
      const secrets = JSON.parse(fs.readFileSync(SECRETS, 'utf8')) as Secrets;
      const cases = Object.entries(secrets.fxe ?? {});
      expect(cases).toHaveLength(2);
      expect(cases.some(([stored]) => /^encrypted-[0-9a-f]{8}\.fxe$/.test(stored))).toBe(true);
      for (const [stored, c] of cases) {
        const bytesOnDisk = new Uint8Array(fs.readFileSync(path.join(FXE_DIR, stored)));
        expect(Buffer.from(bytesOnDisk.slice(0, 8)).toString(), stored).toBe('filexfxe');
        for (const cred of [{ password: c.password }, { recoveryKey: c.recovery_key }]) {
          const { parsed, body } = await readFxe(bytesStream(bytesOnDisk));
          const u = await unlockFxe(parsed.header, cred);
          expect('error' in u, stored).toBe(false);
          if ('error' in u) continue;
          expect(u.key.name).toBe(c.name);
          const plain = await collectBytes(decryptFxeBody(u.key, body));
          expect(plain.length).toBe(c.size);
          expect(sha(plain)).toBe(c.sha256);
        }
        const { parsed } = await readFxe(bytesStream(bytesOnDisk));
        expect(await unlockFxe(parsed.header, { password: c.password + 'x' })).toEqual({ error: 'wrong' });
        // The stored bytes carry no readable name: not the original, not a piece of it.
        expect(Buffer.from(bytesOnDisk).includes(Buffer.from(c.name.split(' ')[0]))).toBe(false);
      }
    },
    SLOW,
  );

  it('the v3 folders store no plaintext name the browser encrypted', () => {
    // Every stored name in v3-names is base64url, a long name, a sidecar, the
    // marker — or the one deliberate stray.
    const seen: string[] = [];
    const walk = (abs: string) => {
      for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
        seen.push(ent.name);
        if (ent.isDirectory()) walk(path.join(abs, ent.name));
      }
    };
    walk(path.join(ROOT, 'v3-names'));
    const unexpected = seen.filter(
      (n) =>
        n !== E2E_MARKER_NAME &&
        n !== 'stray.txt' &&
        !/^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]{22})?$/.test(n) &&
        !/^[A-Za-z0-9_-]{43}\.fxl(\.name|\.[A-Za-z0-9_-]{22})?$/.test(n),
    );
    expect(unexpected).toEqual([]);
    expect(seen.some((n) => n.endsWith('.fxl.name'))).toBe(true);
    // A long FOLDER name keeps its id after the hash.
    expect(seen.some((n) => /\.fxl\.[A-Za-z0-9_-]{22}$/.test(n))).toBe(true);
  });

  it('the v3 folders spell the same name differently in different folders', () => {
    const years = path.join(ROOT, 'v3-names');
    const files: string[] = [];
    const walk = (abs: string, depth: number) => {
      for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
        if (ent.isDirectory()) walk(path.join(abs, ent.name), depth + 1);
        else if (depth === 2) files.push(ent.name);
      }
    };
    walk(years, 0);
    // Sözleşmeler/{2024,2025}/fatura.pdf and Sözleşmeler/Eski/2019.txt.
    expect(files).toHaveLength(3);
    expect(new Set(files).size).toBe(3);
  });
});
