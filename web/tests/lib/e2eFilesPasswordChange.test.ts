// A `.fxe` password change is the new header, written over the file - and
// nothing more from the browser. Up to 0.53 the explorer then ANNOUNCED it
// (POST /api/files/e2e/password-changed) and the server's audit row and the
// owner's notification came from that announcement, on the client's word.
// Since 0.54 the server records the change itself from the header it sees
// rewritten (backend e2e/fxewatch, e2e/slotchange; measured in
// backend/internal/api/handlers/e2e_fxe_password_test.go and
// internal/e2e/keyfilewatch/watch_test.go); the browser run is e2e/tests/173.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h } from 'vue';

import { useE2eFiles } from '../../../packages/core/src/composables/useE2eFiles';
import { createFxe } from '../../../packages/core/src/lib/e2efile';
import { bytesStream, collectBytes } from '../../../packages/core/src/lib/e2estream';
import type { FileNode } from '../../../packages/core/src/types/FileNode';

const PW = 'a file password, long enough';
const PW2 = 'the second file password';
const PATH = 's://Sözleşme.txt.fxe';

let fxeBytes: Uint8Array;

function mount() {
  const order: string[] = [];
  const announced: unknown[] = [];
  const api = {
    authHeaders: async () => ({}),
    previewUrl: (p: string) => `http://filex.test/preview?path=${encodeURIComponent(p)}`,
    credentialsMode: () => 'same-origin',
    // An embed may still name the old door: it is not called.
    endpoints: { e2ePasswordChanged: '/api/files/e2e/password-changed' },
    uploadMultipart: vi.fn(async (_dir: string, files: File[]) => {
      order.push(`upload ${files[0].name}`);
      return {};
    }),
    e2ePasswordChanged: vi.fn(async (p: unknown) => {
      order.push('announce');
      announced.push(p);
      return { ok: true };
    }),
  };
  const toasts: Array<[string, boolean | undefined]> = [];
  let files!: ReturnType<typeof useE2eFiles>;
  const app = createApp(
    defineComponent({
      setup() {
        files = useE2eFiles({
          api: api as never,
          chunked: { uploadFile: async () => ({ id: 'x' }) as never, threshold: () => 8 << 20 },
          locale: () => 'en',
          t: (k: string) => k,
          toast: (m: string, e?: boolean) => toasts.push([m, e]),
          emitError: () => undefined,
          escrowPublicKey: () => null,
          registerOp: () => undefined,
          reload: async () => {
            order.push('reload');
          },
          openPreview: () => undefined,
          showRecoveryKey: () => undefined,
        } as never);
        return () => h('div');
      },
    }),
  );
  app.mount(document.createElement('div'));
  return { files, order, announced, toasts, unmount: () => app.unmount() };
}

const node: FileNode = { path: PATH, basename: 'Sözleşme.txt.fxe', type: 'file', extension: 'fxe', size: 100 };

describe('a .fxe password change announces nothing: the server sees it', () => {
  beforeEach(async () => {
    const plain = new TextEncoder().encode('sözleşme maddeleri\n');
    const created = await createFxe('Sözleşme.txt', plain.length, bytesStream(plain), PW);
    fxeBytes = await collectBytes(created.stream);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(fxeBytes.slice())),
    );
  }, 60_000);
  afterEach(() => vi.unstubAllGlobals());

  it('writes the new header, and tells the server nothing else', async () => {
    const { files, order, announced, toasts, unmount } = mount();
    files.startPassword(node);
    await files.submitPassword({ proof: { password: PW }, newPassword: PW2 });
    expect(files.pwError.value).toBeNull();
    expect(order).toEqual(['upload Sözleşme.txt.fxe', 'reload']);
    expect(announced, 'no announcement: the server records the change itself').toEqual([]);
    expect(toasts).toContainEqual(['e2e.fxe.password_done', undefined]);
    unmount();
  }, 60_000);

  it('a wrong current password changes nothing', async () => {
    const { files, order, unmount } = mount();
    files.startPassword(node);
    await files.submitPassword({ proof: { password: 'not the password' }, newPassword: PW2 });
    expect(files.pwError.value).toBe('e2e.password.wrong_current');
    expect(order).toEqual([]);
    unmount();
  }, 60_000);
});
