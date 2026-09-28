// The name view (packages/core/src/composables/useE2eNames.ts): the one place
// stored (ciphertext) names become the names people see, shared by every pane,
// picker and tray of the explorer. These tests pin its contract: what a
// decorated row keeps, what it never sends, and how it behaves locked,
// unlocked, on a content-only folder and on a name that was never encrypted.

import { beforeAll, describe, expect, it, vi } from 'vitest';

import {
  createE2eNameView,
  isBelowRoot,
  storedName,
} from '../../../packages/core/src/composables/useE2eNames';
import {
  createEncryptedFolder,
  createKeyRing,
  E2E_MARKER_NAME,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import {
  b64urlEncode,
  deriveDirId,
  dirIdOf,
  encryptName,
  type E2eNameKey,
} from '../../../packages/core/src/lib/e2enames';
import type { FileNode } from '../../../packages/core/src/types/FileNode';

const PW = 'correct horse battery';
const ROOT = 'local://Kasa';
const SLOW = 30_000;

/** A fake server: files by wire path, and a log of every path read. */
function fakeApi(files: Record<string, string>) {
  const reads: string[] = [];
  return {
    reads,
    api: {
      async fetchBlob(path: string) {
        reads.push(path);
        if (!(path in files)) throw Object.assign(new Error('404'), { status: 404 });
        return { blob: new Blob([files[path]]), url: 'blob:x' };
      },
      async fetchArrayBuffer(path: string) {
        reads.push(path);
        if (!(path in files)) throw Object.assign(new Error('404'), { status: 404 });
        return new TextEncoder().encode(files[path]).buffer as ArrayBuffer;
      },
    },
  };
}

function row(path: string, type: 'file' | 'dir' = 'file'): FileNode {
  const base = path.slice(path.lastIndexOf('/') + 1);
  return { path, basename: base, type, extension: '' } as FileNode;
}

describe('the name view', () => {
  let marker: E2eMarker;
  let fmk: CryptoKey;
  let nk: E2eNameKey;
  let encReport: string;
  let encSub: string;
  /** "Rapor 2027.pdf" inside Sözleşmeler — sealed for THAT folder. */
  let encReportInSub: string;
  let longStored: string;
  let longSidecar: { name: string; content: string };
  const longName = 'ş'.repeat(90) + '.txt';

  beforeAll(async () => {
    const made = await createEncryptedFolder(PW, { encryptNames: true });
    marker = made.marker;
    fmk = made.fmk;
    nk = made.names!;
    encReport = (await encryptName(nk, 'Rapor 2027.pdf', nk.rootId)).stored;
    const sub = await encryptName(nk, 'Sözleşmeler', nk.rootId, { isDir: true });
    encSub = sub.stored;
    encReportInSub = (await encryptName(nk, 'Rapor 2027.pdf', sub.dirId!)).stored;
    const long = await encryptName(nk, longName, nk.rootId);
    longStored = long.stored;
    longSidecar = long.sidecar!;
  }, SLOW);

  function setup(opts: { unlocked: boolean; markerOverride?: object }) {
    const server = fakeApi({
      [`${ROOT}/${E2E_MARKER_NAME}`]: JSON.stringify(opts.markerOverride ?? marker),
      [`${ROOT}/${longSidecar.name}`]: longSidecar.content,
    });
    const ring = createKeyRing();
    if (opts.unlocked) ring.set(ROOT, fmk, nk);
    const view = createE2eNameView({
      api: server.api,
      ring,
      lockedLabel: () => '🔒 Encrypted item',
      unreadableLabel: () => '🔒 Unreadable name',
    });
    return { view, ring, server };
  }

  it('shows plaintext names in an unlocked folder, and keeps the stored name for the server', async () => {
    const { view } = setup({ unlocked: true });
    const out = await view.decorate(
      [row(`${ROOT}/${encReport}`), row(`${ROOT}/${encSub}`, 'dir')],
      { root: ROOT },
    );
    expect(out.map((r) => r.basename)).toEqual(['Rapor 2027.pdf', 'Sözleşmeler']);
    expect(out[0].extension).toBe('pdf');
    expect(out[1].extension).toBe('');
    // The wire path is untouched: every API call keeps using it.
    expect(out[0].path).toBe(`${ROOT}/${encReport}`);
    expect(storedName(out[0])).toBe(encReport);
    expect(out[0].e2e_name_state).toBe('enc');
    expect(out[0].e2e_display_dir).toBe('Kasa');
  });

  it('says "locked" rather than printing ciphertext when the folder is locked', async () => {
    const { view } = setup({ unlocked: false });
    const [r] = await view.decorate([row(`${ROOT}/${encReport}`)], { root: ROOT });
    expect(r.basename).toBe('🔒 Encrypted item');
    expect(r.extension).toBe('');
    expect(r.e2e_name_state).toBe('locked');
    expect(storedName(r)).toBe(encReport);
  });

  it('resolves a long name through its sidecar, and hides the sidecar', async () => {
    const { view } = setup({ unlocked: true });
    const out = await view.decorate(
      [row(`${ROOT}/${longStored}`), row(`${ROOT}/${longSidecar.name}`)],
      { root: ROOT },
    );
    expect(out).toHaveLength(1);
    expect(out[0].basename).toBe(longName);
  });

  it('shows a name that was never encrypted as it is, and flags it', async () => {
    const { view } = setup({ unlocked: true });
    const [r] = await view.decorate([row(`${ROOT}/written-over-webdav.txt`)], { root: ROOT });
    expect(r.basename).toBe('written-over-webdav.txt');
    expect(r.e2e_name_state).toBe('plain');
  });

  it('leaves a content-only folder (marker v2) exactly as the server sent it', async () => {
    const plain = await createEncryptedFolder(PW);
    const { view } = setup({ unlocked: true, markerOverride: plain.marker });
    const input = [row(`${ROOT}/notes.txt`)];
    const out = await view.decorate(input, { root: ROOT });
    expect(out[0]).toBe(input[0]);
  }, SLOW);

  it('is idempotent — a decorated row decorates to itself', async () => {
    const { view } = setup({ unlocked: true });
    const once = await view.decorate([row(`${ROOT}/${encSub}/${encReportInSub}`)], { root: ROOT });
    const twice = await view.decorate(once, { root: ROOT });
    expect(twice[0].basename).toBe('Rapor 2027.pdf');
    expect(twice[0].e2e_display_dir).toBe('Kasa/Sözleşmeler');
  });

  it('names a row from another view by its own e2e_root (Recent, Starred, search)', async () => {
    const { view } = setup({ unlocked: true });
    const r = { ...row(`${ROOT}/${encSub}/${encReportInSub}`), e2e_root: ROOT } as FileNode;
    const [out] = await view.decorate([r]);
    expect(out.basename).toBe('Rapor 2027.pdf');
    expect(out.e2e_display_dir).toBe('Kasa/Sözleşmeler');
  });

  it('never reads anything but the marker and sidecars — and never by a plaintext path', async () => {
    const { view, server } = setup({ unlocked: true });
    await view.decorate([row(`${ROOT}/${encReport}`), row(`${ROOT}/${longStored}`)], { root: ROOT });
    for (const p of server.reads) {
      expect(p === `${ROOT}/${E2E_MARKER_NAME}` || p.endsWith('.fxl.name')).toBe(true);
      expect(p).not.toContain('Rapor');
    }
  });

  it('encrypts a name written into the folder, and refuses to when it is locked', async () => {
    const unlocked = setup({ unlocked: true });
    await unlocked.view.markerFor(ROOT);
    const enc = await unlocked.view.nameForWrite(`${ROOT}/${encSub}`, 'Yeni belge.txt');
    expect(enc?.stored).toBe((await encryptName(nk, 'Yeni belge.txt', dirIdOf(encSub)!)).stored);

    const locked = setup({ unlocked: false });
    await locked.view.markerFor(ROOT);
    await expect(locked.view.nameForWrite(ROOT, 'x.txt')).rejects.toThrow(/locked/);

    // Outside any encrypted folder there is nothing to encrypt.
    expect(await unlocked.view.nameForWrite('local://elsewhere', 'x.txt')).toBeNull();
  });

  it('labels breadcrumb segments once they are resolved', async () => {
    const { view } = setup({ unlocked: true });
    await view.markerFor(ROOT);
    const wire = `${ROOT}/${encSub}`;
    expect(view.segmentLabel(wire)).toBe('…'); // resolving in the background
    await vi.waitFor(() => expect(view.segmentLabel(wire)).toBe('Sözleşmeler'));
    expect(view.segmentLabel('local://Kasa')).toBeNull(); // the root's own name is plain
    // A whole path: known segments at once, the rest once they resolve.
    expect(view.displayPath(`${wire}/${encReportInSub}`)).toBe('Kasa/Sözleşmeler/…');
    await vi.waitFor(() =>
      expect(view.displayPath(`${wire}/${encReportInSub}`)).toBe('Kasa/Sözleşmeler/Rapor 2027.pdf'),
    );
  });

  it('reads the same name in two folders from two different stored names', async () => {
    const { view } = setup({ unlocked: true });
    expect(encReport).not.toBe(encReportInSub);
    const out = await view.decorate(
      [row(`${ROOT}/${encReport}`), row(`${ROOT}/${encSub}/${encReportInSub}`)],
      { root: ROOT },
    );
    expect(out.map((r) => r.basename)).toEqual(['Rapor 2027.pdf', 'Rapor 2027.pdf']);
    // Moved without being re-sealed (over WebDAV): not the same name any more.
    const [moved] = await view.decorate([row(`${ROOT}/${encSub}/${encReport}`)], { root: ROOT });
    expect(moved.e2e_name_state).toBe('plain');
  });

  it('gives a new folder its own id, and a renamed or moved folder the id it had', async () => {
    const { view } = setup({ unlocked: true });
    await view.markerFor(ROOT);
    const fresh = await view.nameForWrite(ROOT, 'Yeni klasör', { isDir: true });
    expect(dirIdOf(fresh!.stored)).not.toBeNull();
    expect(b64urlEncode(dirIdOf(fresh!.stored)!)).not.toBe(b64urlEncode(dirIdOf(encSub)!));
    // Renamed: a new name, the same id — so everything inside still reads.
    const renamed = await view.nameForWrite(ROOT, 'Anlaşmalar', { keepIdOf: `${ROOT}/${encSub}` });
    expect(b64urlEncode(dirIdOf(renamed!.stored)!)).toBe(b64urlEncode(dirIdOf(encSub)!));
    const [child] = await view.decorate([row(`${ROOT}/${renamed!.stored}/${encReportInSub}`)], { root: ROOT });
    expect(child.basename).toBe('Rapor 2027.pdf');
    expect(child.e2e_display_dir).toBe('Kasa/Anlaşmalar');
  });

  it('reads what is inside a folder whose own name is not encrypted yet', async () => {
    // A level change mid-way: the children of "Arşiv" were sealed under the
    // id "Arşiv" will carry, and "Arşiv" itself is still plaintext.
    const { view } = setup({ unlocked: true });
    const id = await deriveDirId(nk, nk.rootId, 'Arşiv');
    const inner = (await encryptName(nk, 'eski.txt', id)).stored;
    const [r] = await view.decorate([row(`${ROOT}/Arşiv/${inner}`)], { root: ROOT });
    expect(r.basename).toBe('eski.txt');
    expect(r.e2e_name_state).toBe('enc');
    expect(r.e2e_display_dir).toBe('Kasa/Arşiv');
  });

  it('forgets every plaintext name when the folder is locked', async () => {
    const { view, ring } = setup({ unlocked: true });
    await view.decorate([row(`${ROOT}/${encReport}`)], { root: ROOT });
    ring.lock(ROOT);
    view.forget(ROOT);
    const [r] = await view.decorate([row(`${ROOT}/${encReport}`)], { root: ROOT });
    expect(r.basename).toBe('🔒 Encrypted item');
  });

  it('knows what is below a root', () => {
    expect(isBelowRoot('local://Kasa/x', 'local://Kasa')).toBe(true);
    expect(isBelowRoot('local://Kasa', 'local://Kasa')).toBe(false);
    expect(isBelowRoot('local://Kasa2/x', 'local://Kasa')).toBe(false);
    expect(isBelowRoot('local://x', 'local://')).toBe(true);
  });
});
