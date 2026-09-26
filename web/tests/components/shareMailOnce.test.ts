// "Send by email" sends once per press, Enter included.
//
// ⚠ The Send button shut while the mail went out, but the address box sends on
// Enter too, and Enter did not ask: a second Enter while the first mail was on
// its way sent it again, to the same people. Both the share link and the
// upload link had it.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';

/** A FileApi whose mail is held on its way until `release()`. */
function fakeApi() {
  let release: () => void = () => {};
  const sent: Array<Record<string, unknown>> = [];
  let seq = 100;
  const api = {
    listPermissions: vi.fn(async () => ({ direct: [], inherited: [], storage_rbac: true })),
    listShares: vi.fn(async () => ({ shares: [] })),
    createShare: vi.fn(async (body: { kind?: string }) => {
      const uuid = String(++seq);
      return { share: { url: `https://files.example/${body.kind === 'drop' ? 'd' : 's'}/${uuid}`, password_pin: null, expires_at: null } };
    }),
    revokeShare: vi.fn(async () => {}),
    shareMail: vi.fn(
      (body: Record<string, unknown>) =>
        new Promise((resolve) => {
          sent.push(body);
          release = () => resolve({ sent: (body.emails as string[]).length, failed: [] });
        }),
    ),
    resolveEmail: vi.fn(async () => ({ found: false })),
    searchUsers: vi.fn(async () => ({ users: [] })),
  };
  return { api: api as unknown as FileApi, sent, release: () => release() };
}

function dialog(api: FileApi) {
  return mount(PermissionsModal, {
    props: { path: 'depo://Projeler', isDir: true, locale: 'en', shareMaxTtlDays: 7, api, mailReady: true },
    attachTo: document.body,
  });
}

describe('send by email', () => {
  it('a second Enter while the share mail is on its way sends nothing more', async () => {
    const { api, sent, release } = fakeApi();
    const w = dialog(api);
    await flushPromises();
    await w.get('[data-testid="share-switch"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    await flushPromises();

    const box = w.get('[data-testid="share-mail-row"] input');
    await box.setValue('ayse@example.com');
    await box.trigger('keyup', { key: 'Enter' });
    await box.trigger('keyup', { key: 'Enter' });
    await flushPromises();
    expect(sent).toHaveLength(1);

    release();
    await flushPromises();
    w.unmount();
  });

  it('a second Enter while the upload-link mail is on its way sends nothing more', async () => {
    const { api, sent, release } = fakeApi();
    const w = dialog(api);
    await flushPromises();
    await w.get('[data-testid="share-drop-toggle"]').trigger('click');
    await w.get('[data-testid="drop-create"]').trigger('click');
    await flushPromises();

    // The only mail row on screen: no share link was made.
    const box = w.get('.fe-share__mailrow input');
    await box.setValue('ayse@example.com');
    await box.trigger('keyup', { key: 'Enter' });
    await box.trigger('keyup', { key: 'Enter' });
    await flushPromises();
    expect(sent).toHaveLength(1);

    release();
    await flushPromises();
    w.unmount();
  });
});
