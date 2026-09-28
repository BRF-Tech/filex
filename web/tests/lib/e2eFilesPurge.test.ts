// "Delete the original for good" while encrypting a file — an administrator's
// choice in the encrypt dialog (docs/E2E-ENCRYPTION.md → "Single encrypted
// files"). The dialog offers it only to an administrator, asks a second,
// separate confirmation when the file is someone else's, and the composable
// does it in an order that leaves nothing behind: the encrypted copy first,
// then every version the original kept, then the original into the trash
// synchronously, then that trash entry. The browser run is e2e/tests/173.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick } from 'vue';

import { useE2eFiles } from '../../../packages/core/src/composables/useE2eFiles';
import type { FileNode } from '../../../packages/core/src/types/FileNode';
import E2eFileEncryptModal from '../../../packages/core/src/components/E2eFileEncryptModal.vue';

const PW = 'a file password, long enough';
const PLAIN = new TextEncoder().encode('ad;maaş\nAyşe;1\n');

function mountModal(props: Record<string, unknown>) {
  const submitted: unknown[] = [];
  const el = document.createElement('div');
  document.body.appendChild(el);
  const app = createApp({
    render: () =>
      h(E2eFileEncryptModal, {
        open: true,
        locale: 'en',
        fileName: 'Maaş listesi.csv',
        ...props,
        onSubmit: (p: unknown) => submitted.push(p),
      }),
  });
  app.mount(el);
  const q = (id: string) => document.body.querySelector(`[data-testid="${id}"]`) as HTMLInputElement | null;
  const type = async (id: string, v: string) => {
    const i = q(id)!;
    i.value = v;
    i.dispatchEvent(new Event('input'));
    await nextTick();
  };
  const tick = async (id: string) => {
    const i = q(id)!;
    i.checked = true;
    i.dispatchEvent(new Event('change'));
    await nextTick();
  };
  return {
    submitted,
    q,
    type,
    tick,
    unmount: () => {
      app.unmount();
      el.remove();
    },
  };
}

describe('the encrypt dialog — "delete the original for good"', () => {
  it('is not offered to someone who does not administer the installation', async () => {
    const m = mountModal({ canPurge: false });
    await nextTick();
    expect(m.q('fxe-encrypt-purge')).toBeNull();
    m.unmount();
  });

  it('an administrator may tick it; unticked, the original goes to the trash as before', async () => {
    const m = mountModal({ canPurge: true });
    await nextTick();
    expect(m.q('fxe-encrypt-purge')).not.toBeNull();
    expect(document.body.textContent).toContain('unless you delete it for good below');
    await m.type('fxe-encrypt-pw', PW);
    await m.type('fxe-encrypt-pw2', PW);
    await m.tick('fxe-encrypt-ack');
    m.q('fxe-encrypt-submit')!.click();
    await nextTick();
    expect(m.submitted).toEqual([{ password: PW, hideName: false, purge: false }]);
    m.unmount();
  });

  it('my own file: ticked, no second confirmation', async () => {
    const m = mountModal({ canPurge: true, notOwner: false });
    await nextTick();
    await m.tick('fxe-encrypt-purge');
    expect(m.q('fxe-encrypt-purge-others')).toBeNull();
    await m.type('fxe-encrypt-pw', PW);
    await m.type('fxe-encrypt-pw2', PW);
    await m.tick('fxe-encrypt-ack');
    m.q('fxe-encrypt-submit')!.click();
    await nextTick();
    expect(m.submitted).toEqual([{ password: PW, hideName: false, purge: true }]);
    m.unmount();
  });

  it('someone else’s file: names the owner and refuses without the second confirmation', async () => {
    const m = mountModal({ canPurge: true, notOwner: true, ownerName: 'Çağla Yılmaz' });
    await nextTick();
    expect(m.q('fxe-encrypt-purge-others'), 'shown only once "delete for good" is ticked').toBeNull();
    await m.tick('fxe-encrypt-purge');
    expect(m.q('fxe-encrypt-purge-others')?.textContent).toContain('This file belongs to Çağla Yılmaz, not to you');
    await m.type('fxe-encrypt-pw', PW);
    await m.type('fxe-encrypt-pw2', PW);
    await m.tick('fxe-encrypt-ack');
    m.q('fxe-encrypt-submit')!.click();
    await nextTick();
    expect(m.submitted).toEqual([]);
    expect(m.q('fxe-encrypt-error')?.textContent).toBe('Confirm that you are deleting someone else’s file for good.');
    await m.tick('fxe-encrypt-purge-others-ack');
    m.q('fxe-encrypt-submit')!.click();
    await nextTick();
    expect(m.submitted).toEqual([{ password: PW, hideName: false, purge: true }]);
    m.unmount();
  });

  it('a file with no recorded owner is said to be nobody’s, and asks the same', async () => {
    const m = mountModal({ canPurge: true, notOwner: true, ownerName: '' });
    await nextTick();
    await m.tick('fxe-encrypt-purge');
    expect(m.q('fxe-encrypt-purge-others')?.textContent).toContain('no recorded owner');
    m.unmount();
  });
});

describe('the purge, in order', () => {
  function mount(opts: { trash404?: boolean } = {}) {
    const calls: string[] = [];
    const api = {
      authHeaders: async () => ({}),
      previewUrl: (p: string) => `http://filex.test/preview?path=${encodeURIComponent(p)}`,
      credentialsMode: () => 'same-origin',
      endpoints: { deleteAsync: '/api/files/delete' },
      index: async () => ({ files: [] }),
      uploadMultipart: vi.fn(async (_dir: string, files: File[]) => {
        calls.push(`upload ${files[0].name}`);
        return {};
      }),
      listVersions: vi.fn(async (id: number) => {
        calls.push(`list ${id}`);
        return [
          { id: 11, node_id: id, version_n: 1, size: 1 },
          { id: 12, node_id: id, version_n: 2, size: 1 },
        ];
      }),
      purgeVersion: vi.fn(async (id: number) => {
        calls.push(`purge-version ${id}`);
      }),
      deleteItems: vi.fn(async (_dir: string, items: string[]) => {
        calls.push(`delete ${items.join(',')}`);
        return {};
      }),
      deleteAsync: vi.fn(async () => {
        calls.push('delete-async');
        return { op: { id: 1 } };
      }),
      purgeTrash: vi.fn(async (id: number) => {
        calls.push(`purge-trash ${id}`);
        if (opts.trash404) throw Object.assign(new Error('not found'), { status: 404 });
        return {};
      }),
    };
    const toasts: string[] = [];
    const shown: string[] = [];
    let files!: ReturnType<typeof useE2eFiles>;
    const app = createApp(
      defineComponent({
        setup() {
          files = useE2eFiles({
            api: api as never,
            chunked: { uploadFile: async () => ({ id: 'x' }) as never, threshold: () => 8 << 20 },
            locale: () => 'en',
            t: (k: string) => k,
            toast: (m: string) => toasts.push(m),
            emitError: () => undefined,
            escrowPublicKey: () => null,
            registerOp: () => undefined,
            reload: async () => undefined,
            openPreview: () => undefined,
            showRecoveryKey: (_k: string, name: string) => shown.push(name),
          } as never);
          return () => h('div');
        },
      }),
    );
    app.mount(document.createElement('div'));
    return { files, api, calls, toasts, shown, unmount: () => app.unmount() };
  }

  const node = (extra: Partial<FileNode> = {}): FileNode => ({
    path: 's://Maaş listesi.csv',
    basename: 'Maaş listesi.csv',
    type: 'file',
    extension: 'csv',
    size: PLAIN.length,
    id: 42,
    ...extra,
  });

  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(PLAIN.slice())),
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('the encrypted copy, then every version, then the original — synchronously — then its trash entry', async () => {
    const { files, calls, toasts, shown, unmount } = mount();
    files.startEncrypt(node());
    await files.submitEncrypt({ password: PW, hideName: false, purge: true });
    expect(files.encryptError.value).toBeNull();
    expect(calls).toEqual([
      'upload Maaş listesi.csv.fxe',
      'list 42',
      'purge-version 11',
      'purge-version 12',
      'delete s://Maaş listesi.csv',
      'purge-trash 42',
    ]);
    expect(toasts).toContain('e2e.fxe.purged');
    expect(shown, 'the recovery key is still shown').toEqual(['Maaş listesi.csv']);
    unmount();
  }, 60_000);

  it('a server without a trash already deleted it for good: that is done, not failed', async () => {
    const { files, toasts, unmount } = mount({ trash404: true });
    files.startEncrypt(node());
    await files.submitEncrypt({ password: PW, hideName: false, purge: true });
    expect(toasts).toContain('e2e.fxe.purged');
    unmount();
  }, 60_000);

  it('without "delete for good" nothing is purged; the original goes to the trash as before', async () => {
    const { files, calls, unmount } = mount();
    files.startEncrypt(node());
    await files.submitEncrypt({ password: PW, hideName: false });
    expect(calls).toEqual(['upload Maaş listesi.csv.fxe', 'delete-async']);
    unmount();
  }, 60_000);

  it('a row the catalogue does not know yet is refused before anything is uploaded', async () => {
    const { files, calls, unmount } = mount();
    files.startEncrypt(node({ id: undefined }));
    await files.submitEncrypt({ password: PW, hideName: false, purge: true });
    expect(files.encryptError.value).toBe('e2e.fxe.purge_no_id');
    expect(calls).toEqual([]);
    unmount();
  }, 60_000);
});
