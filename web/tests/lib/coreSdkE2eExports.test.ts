// The end-to-end encryption building blocks an integrator gets from the
// PACKAGE (`@brftech/filex-core`), not from a deep import of its source.
//
// ⚠ Why (task #119, API/MCP coverage audit 2026-09-28): the package exported
// createEncryptedFolder, encryptFile, createFxe, readFxe, unlockFxe and
// decryptFxeBody, and nothing else of what the explorer does with an
// encrypted folder. An integrator could not write into a level-2 folder (no
// name encryption), could not encrypt a file over 200 MB (no STREAM writer),
// could not convert a folder or change a .fxe's password. Each test below
// does one of those jobs with nothing but the package's exports.
//
// RED PROOF: on 0.49 every one of these imports is `undefined`, and each test
// fails with "is not a function".
import {
  E2E_FILE_VERSION_STREAM,
  E2E_STREAM_CHUNK_LOG2,
  changeFxePassword,
  classifyStoredName,
  createEncryptedFolder,
  createFxe,
  decryptFolderFileStream,
  decryptFxeBody,
  decryptStoredName,
  dirIdOf,
  encryptFolderFileStream,
  encryptName,
  isDraftLimit,
  draftLimitOf,
  markerHasNames,
  readFxe,
  replaceFxeHeader,
  runConversion,
  runNamePass,
  streamFolderFileSize,
  unlockFxe,
  unlockNameKey,
} from '@brftech/filex-core';
import { describe, expect, it } from 'vitest';

function streamOf(b: Uint8Array, piece = 4096): ReadableStream<Uint8Array> {
  let off = 0;
  return new ReadableStream<Uint8Array>({
    pull(ctl) {
      if (off >= b.length) {
        ctl.close();
        return;
      }
      ctl.enqueue(b.slice(off, off + piece));
      off += piece;
    },
  });
}

async function bytesOf(s: ReadableStream<Uint8Array>): Promise<Uint8Array> {
  const parts: Uint8Array[] = [];
  const r = s.getReader();
  for (;;) {
    const { done, value } = await r.read();
    if (done) break;
    parts.push(value);
  }
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

function pattern(n: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + 7) & 0xff;
  return out;
}

describe('@brftech/filex-core: E2E building blocks (#119)', () => {
  it('writes and reads an encrypted NAME in a level-2 folder, at its root and one folder down', async () => {
    const created = await createEncryptedFolder('a folder password', { encryptNames: true });
    expect(markerHasNames(created.marker)).toBe(true);
    const names = await unlockNameKey(created.marker, created.fmk);
    expect(names).not.toBeNull();

    const file = await encryptName(names!, 'Q3 rapor şubat.pdf', names!.rootId);
    expect(file.stored).not.toContain('rapor');
    expect(classifyStoredName(file.stored).kind).toBe('file');
    expect(await decryptStoredName(names!, file.stored, names!.rootId)).toMatchObject({ name: 'Q3 rapor şubat.pdf' });

    // A folder carries its own id in its stored name; its children are sealed under it.
    const dir = await encryptName(names!, 'Arşiv', names!.rootId, { isDir: true });
    const dirId = dirIdOf(dir.stored);
    expect(dirId).not.toBeNull();
    const child = await encryptName(names!, 'not.txt', dirId!);
    expect(await decryptStoredName(names!, child.stored, dirId!)).toMatchObject({ name: 'not.txt' });
    // Sealed for the folder it is in: under another folder's id it does not open.
    expect((await decryptStoredName(names!, child.stored, names!.rootId)).name).not.toBe('not.txt');
  });

  it('writes a STREAM (0x02) file - the format of every file over the one-shot limit - and reads it back', async () => {
    const created = await createEncryptedFolder('a folder password');
    const plain = pattern(3 * (1 << 10) + 5);
    const { stream, size } = await encryptFolderFileStream(created.fmk, plain.length, streamOf(plain), { chunkLog2: 10 });
    const ct = await bytesOf(stream);
    expect(ct.length).toBe(size);
    expect(new TextDecoder().decode(ct.slice(0, 8))).toBe('filexe2e');
    expect(ct[8]).toBe(E2E_FILE_VERSION_STREAM);
    expect(streamFolderFileSize(plain.length, E2E_STREAM_CHUNK_LOG2)).toBeGreaterThan(plain.length);

    const back = await bytesOf(decryptFolderFileStream(created.fmk, null, streamOf(ct, 1000)));
    expect(Buffer.compare(Buffer.from(back), Buffer.from(plain))).toBe(0);
  });

  it("changes a .fxe's password: the old one stops opening it, the new one and the recovery key do", async () => {
    const plain = pattern(2000);
    const made = await createFxe('maaş.xlsx', plain.length, streamOf(plain), 'the first password');
    const file = await bytesOf(made.stream);

    const { parsed } = await readFxe(streamOf(file));
    const next = await changeFxePassword(parsed.header, { password: 'the first password' }, 'the second password');
    const rewritten = await bytesOf(replaceFxeHeader(next, streamOf(file.slice(parsed.prefixLength))).stream);

    const open = async (cred: Parameters<typeof unlockFxe>[1]) => {
      const { parsed: p, body } = await readFxe(streamOf(rewritten));
      const u = await unlockFxe(p.header, cred);
      if ('error' in u) {
        await body.cancel();
        return u.error;
      }
      return Buffer.from(await bytesOf(decryptFxeBody(u.key, body))).toString('hex');
    };
    expect(await open({ password: 'the first password' })).toBe('wrong');
    expect(await open({ password: 'the second password' })).toBe(Buffer.from(plain).toString('hex'));
    expect(await open({ recoveryKey: made.recoveryKey })).toBe(Buffer.from(plain).toString('hex'));
  });

  it('converts a folder that holds plain files, through the caller\'s own I/O', async () => {
    const files = new Map<string, Uint8Array>([
      ['docs://kasa/a.txt', new TextEncoder().encode('alpha')],
      ['docs://kasa/alt/b.txt', new TextEncoder().encode('beta')],
    ]);
    const written: string[] = [];
    const rows = (dir: string) => {
      if (dir === 'docs://kasa') {
        return [
          { path: 'docs://kasa/a.txt', basename: 'a.txt', type: 'file' as const, size: 5, last_modified: 1 },
          { path: 'docs://kasa/alt', basename: 'alt', type: 'dir' as const },
        ];
      }
      return [{ path: 'docs://kasa/alt/b.txt', basename: 'b.txt', type: 'file' as const, size: 4, last_modified: 2 }];
    };
    const prog = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(
      'docs://kasa',
      {
        list: async (dir) => rows(dir),
        head: async (p) => files.get(p)!.slice(0, 9),
        read: async (p) => new Uint8Array(files.get(p)!).buffer,
        write: async (_dir, row) => {
          written.push(row.path);
        },
        encrypt: async (data) => data,
        stopped: () => false,
      },
      prog,
      { maxBytes: 1 << 20 },
    );
    expect(prog).toMatchObject({ total: 2, done: 2, failed: 0 });
    expect(written.sort()).toEqual(['docs://kasa/a.txt', 'docs://kasa/alt/b.txt']);
  });

  it('runs the names pass: every plain name in the folder is renamed to its sealed form', async () => {
    const created = await createEncryptedFolder('a folder password', { encryptNames: true });
    const names = (await unlockNameKey(created.marker, created.fmk))!;
    const renamed: Array<{ from: string; to: string }> = [];
    const prog = { renamed: 0, failed: 0, seen: 0 };
    await runNamePass(
      names,
      'docs://kasa',
      {
        list: async (dir) =>
          dir === 'docs://kasa' ? [{ path: 'docs://kasa/plan.txt', basename: 'plan.txt', type: 'file' as const }] : [],
        readSidecar: async () => null,
        writeSidecar: async () => {},
        rename: async (_dir, row, to) => {
          renamed.push({ from: row.basename, to });
        },
        stopped: () => false,
      },
      prog,
    );
    expect(prog.renamed).toBe(1);
    expect(renamed[0].from).toBe('plan.txt');
    expect(await decryptStoredName(names, renamed[0].to, names.rootId)).toMatchObject({ name: 'plan.txt' });
  });

  it('reads a draft-limit refusal for a caller of api.drafts', () => {
    // The RequestFailure `api.drafts` throws: the refusal's JSON body in `detail`.
    const atLimit = Object.assign(new Error('HTTP 409'), {
      status: 409,
      detail: JSON.stringify({ error: 'you already keep 50 drafts', code: 'DRAFT_LIMIT', limit: 50 }),
    });
    const other = Object.assign(new Error('HTTP 409'), { status: 409, detail: JSON.stringify({ code: 'TARGET_TAKEN' }) });
    expect(isDraftLimit(atLimit)).toBe(true);
    expect(draftLimitOf(atLimit)).toBe(50);
    expect(isDraftLimit(other)).toBe(false);
    expect(draftLimitOf(other)).toBeNull();
  });
});
