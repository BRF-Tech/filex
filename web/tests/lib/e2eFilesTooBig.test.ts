// A decrypted download too big for the browser's memory (Firefox, Safari: no
// File System Access, so the Blob sink, limited to E2E_BLOB_SAVE_LIMIT). It is
// refused BEFORE the password is asked — the `.fxe` header says the plaintext
// size without any key — and a dialog gives the way out: the encrypted bytes
// and the `filex decrypt` command. Where the browser saves as a stream
// (Chrome, Edge, the desktop app) nothing changes. The browser run with a
// real 1.1 GB file is e2e/tests/173.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick } from 'vue';

import { useE2eFiles } from '../../../packages/core/src/composables/useE2eFiles';
import { createFxe, encodeFxePrefix, readFxe } from '../../../packages/core/src/lib/e2efile';
import { E2E_BLOB_SAVE_LIMIT } from '../../../packages/core/src/lib/e2esave';
import { bytesStream } from '../../../packages/core/src/lib/e2estream';
import type { FileNode } from '../../../packages/core/src/types/FileNode';
import E2eTooBigModal from '../../../packages/core/src/components/E2eTooBigModal.vue';

const PW = 'a file password, long enough';
const HUGE = E2E_BLOB_SAVE_LIMIT + 60 * 1024 * 1024;

/** The first bytes of a real `.fxe` whose header says `size` — all a size
 *  check reads; the body is never asked for. */
async function headerSaying(size: number): Promise<Uint8Array> {
  const plain = new Uint8Array([1, 2, 3]);
  const created = await createFxe('Yedek arşivi.bin', plain.length, bytesStream(plain), PW);
  const { parsed, body } = await readFxe(created.stream);
  await body.cancel();
  return encodeFxePrefix({ ...parsed.header, size });
}

function mount() {
  const api = {
    authHeaders: async () => ({}),
    previewUrl: (p: string) => `http://filex.test/preview?path=${encodeURIComponent(p)}`,
    credentialsMode: () => 'same-origin',
  };
  let files!: ReturnType<typeof useE2eFiles>;
  const app = createApp(
    defineComponent({
      setup() {
        files = useE2eFiles({
          api: api as never,
          chunked: { uploadFile: async () => ({ id: 'x' }) as never, threshold: () => 8 << 20 },
          locale: () => 'en',
          t: (k: string) => k,
          toast: () => undefined,
          emitError: () => undefined,
          escrowPublicKey: () => null,
          registerOp: () => undefined,
          reload: async () => undefined,
          openPreview: () => undefined,
          showRecoveryKey: () => undefined,
        } as never);
        return () => h('div');
      },
    }),
  );
  app.mount(document.createElement('div'));
  return { files, unmount: () => app.unmount() };
}

const fxe = (size: number): FileNode => ({
  path: 's://Arşiv/encrypted-9f8e7d6c.fxe',
  basename: 'encrypted-9f8e7d6c.fxe',
  type: 'file',
  extension: 'fxe',
  size,
});

describe('over the in-memory limit', () => {
  let head: Uint8Array;
  beforeEach(async () => {
    head = await headerSaying(HUGE);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(head.slice())),
    );
  }, 30_000);
  afterEach(() => {
    vi.unstubAllGlobals();
    delete (window as unknown as { showSaveFilePicker?: unknown }).showSaveFilePicker;
  });

  it('a .fxe: said before the password is asked, with the encrypted file and the command', async () => {
    const { files, unmount } = mount();
    await files.download(fxe(HUGE + 4096));
    expect(files.unlockTarget.value, 'no password is asked for a download that cannot happen here').toBeNull();
    expect(files.tooBig.value).toEqual({
      name: 'encrypted-9f8e7d6c.fxe',
      size: HUGE,
      limit: E2E_BLOB_SAVE_LIMIT,
      encrypted: { kind: 'file', path: 's://Arşiv/encrypted-9f8e7d6c.fxe' },
      command: 'filex decrypt "encrypted-9f8e7d6c.fxe"',
    });
    files.closeTooBig();
    expect(files.tooBig.value).toBeNull();
    unmount();
  });

  it('a .fxe whose plaintext is under the limit goes on to the password, as before', async () => {
    head = await headerSaying(E2E_BLOB_SAVE_LIMIT - 1);
    const { files, unmount } = mount();
    await files.download(fxe(E2E_BLOB_SAVE_LIMIT + 10));
    expect(files.tooBig.value).toBeNull();
    expect(files.unlockTarget.value?.path).toBe('s://Arşiv/encrypted-9f8e7d6c.fxe');
    unmount();
  }, 30_000);

  it('a .fxe whose stored size is under the limit is not size-checked at all', async () => {
    // The header would say HUGE: a refusal here would mean it was read for size.
    const { files, unmount } = mount();
    await files.download(fxe(1234));
    expect(files.tooBig.value).toBeNull();
    expect(files.unlockTarget.value?.path).toBe('s://Arşiv/encrypted-9f8e7d6c.fxe');
    unmount();
  });

  it('where the browser saves as a stream (File System Access), nothing changes: any size goes on', async () => {
    (window as unknown as { showSaveFilePicker?: unknown }).showSaveFilePicker = async () => ({});
    const { files, unmount } = mount();
    await files.download(fxe(HUGE + 4096));
    expect(files.tooBig.value, 'no size limit where the save is a stream').toBeNull();
    expect(files.unlockTarget.value?.path, 'on to the password, as before').toBe('s://Arşiv/encrypted-9f8e7d6c.fxe');
    unmount();
  });

  it('a file of an encrypted folder: the way out is the folder as a zip', async () => {
    const { files, unmount } = mount();
    const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
    const row: FileNode = { path: 's://Kasa/film.mkv', basename: 'film.mkv', type: 'file', extension: 'mkv', size: HUGE };
    await files.downloadFolderFile(row, key, null, 's://Kasa');
    expect(files.tooBig.value).toEqual({
      name: 'film.mkv',
      size: HUGE,
      limit: E2E_BLOB_SAVE_LIMIT,
      encrypted: { kind: 'folder', path: 's://Kasa' },
      command: 'filex decrypt "Kasa.zip"',
    });
    unmount();
  });
});

describe('E2eTooBigModal', () => {
  it('says the size and the limit, shows the command, and hands over the encrypted file', async () => {
    const got: string[] = [];
    const el = document.createElement('div');
    document.body.appendChild(el);
    const app = createApp({
      render: () =>
        h(E2eTooBigModal, {
          open: true,
          locale: 'en',
          info: {
            name: 'Yedek arşivi.bin',
            size: 1_136_660_705,
            limit: E2E_BLOB_SAVE_LIMIT,
            encrypted: { kind: 'file', path: 's://x.fxe' },
            command: 'filex decrypt "Yedek arşivi.bin.fxe"',
          },
          onDownload: () => got.push('download'),
          onClose: () => got.push('close'),
        }),
    });
    app.mount(el);
    await nextTick();
    const box = document.body.querySelector('[data-testid="e2e-too-big"]')!;
    expect(box.textContent).toContain('“Yedek arşivi.bin” is 1.14 GB once decrypted');
    expect(box.textContent).toContain('at most 1.07 GB');
    expect(document.body.querySelector('[data-testid="e2e-too-big-command"]')?.textContent).toBe('filex decrypt "Yedek arşivi.bin.fxe"');
    const dl = document.body.querySelector('[data-testid="e2e-too-big-download"]') as HTMLButtonElement;
    expect(dl.textContent?.trim()).toBe('Download encrypted file');
    dl.click();
    expect(got).toEqual(['download']);
    app.unmount();
    el.remove();
  });
});
