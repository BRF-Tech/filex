// A `.fxe` password change is ANNOUNCED once the new header is written, like
// an encrypted folder's (POST /api/files/e2e/password-changed): the server
// records it in the audit log and tells the file's owner. The announcement
// comes after the upload — announcing a change that failed would be a lie —
// and a failed announcement is said, not thrown: the password has changed.
// The server's own record of the rewrite (e2e.fxe_header_rewritten) is
// measured in backend/internal/api/handlers/e2e_fxe_password_test.go; the
// browser run is e2e/tests/173.

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

function mount(opts: { announceFails?: boolean } = {}) {
  const order: string[] = [];
  const announced: unknown[] = [];
  const api = {
    authHeaders: async () => ({}),
    previewUrl: (p: string) => `http://filex.test/preview?path=${encodeURIComponent(p)}`,
    credentialsMode: () => 'same-origin',
    endpoints: { e2ePasswordChanged: '/api/files/e2e/password-changed' },
    uploadMultipart: vi.fn(async (_dir: string, files: File[]) => {
      order.push(`upload ${files[0].name}`);
      return {};
    }),
    e2ePasswordChanged: vi.fn(async (p: unknown) => {
      order.push('announce');
      announced.push(p);
      if (opts.announceFails) throw new Error('502 bad gateway');
      return { ok: true, notified: true };
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

describe('a .fxe password change is announced', () => {
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

  it('after the new header is written, with how it was proved', async () => {
    const { files, order, announced, unmount } = mount();
    files.startPassword(node);
    await files.submitPassword({ proof: { password: PW }, newPassword: PW2 });
    expect(files.pwError.value).toBeNull();
    expect(order).toEqual(['upload Sözleşme.txt.fxe', 'announce', 'reload']);
    expect(announced).toEqual([{ path: PATH, via: 'password', rekey: false }]);
    unmount();
  }, 60_000);

  it('a wrong current password changes nothing and announces nothing', async () => {
    const { files, order, unmount } = mount();
    files.startPassword(node);
    await files.submitPassword({ proof: { password: 'not the password' }, newPassword: PW2 });
    expect(files.pwError.value).toBe('e2e.password.wrong_current');
    expect(order).toEqual([]);
    unmount();
  }, 60_000);

  it('a failed announcement is said — the password has changed all the same', async () => {
    const { files, toasts, unmount } = mount({ announceFails: true });
    files.startPassword(node);
    await files.submitPassword({ proof: { password: PW }, newPassword: PW2 });
    expect(files.pwError.value).toBeNull();
    expect(files.pwTarget.value, 'the dialog closed: the change is done').toBeNull();
    expect(toasts).toContainEqual(['e2e.password.announce_failed', true]);
    unmount();
  }, 60_000);
});
