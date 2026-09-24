// The share dialog — one of everything, and nothing it cannot deliver.
//
// ⚠⚠ What the release-candidate sweep found in it (QA, 2026-09-21):
//   #33  three different "Copy" buttons; the PIN a native checkbox under a
//        switch; "Create link" minting a SECOND live link after the switch had
//        already made one (the old one without the new PIN); the header's link
//        listed again underneath with a second Copy.
//   #21  for an editor the dialog asked for the grant list anyway, swallowed
//        the 403 and quietly drew no people section; for an administrator on a
//        storage with RBAC off the add-people form WORKED while the box above
//        it said grants do not apply ("Bu diskte RBAC kapalı …").
//   and  "Create a link, then it will be sent to x@y ." — a space before the
//        full stop, because the sentence was two halves around a <strong>.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';

type Share = { uuid: string; url: string; kind?: string; expires_at?: string | null; max_downloads?: number | null };

/** A fake FileApi holding real share state: create adds, revoke removes. */
function fakeApi(opts: { perms?: 'owner' | 'forbidden'; rbac?: boolean; shares?: Share[] } = {}) {
  let shares: Share[] = [...(opts.shares ?? [])];
  let seq = 100;
  const calls = { listPermissions: 0, create: 0, revoke: 0, addPermission: 0 };
  const api = {
    listPermissions: vi.fn(async () => {
      calls.listPermissions++;
      if (opts.perms === 'forbidden') throw Object.assign(new Error('forbidden'), { status: 403 });
      return { direct: [], inherited: [], storage_rbac: opts.rbac !== false };
    }),
    listShares: vi.fn(async () => ({ shares: [...shares] })),
    createShare: vi.fn(async (body: { kind?: string; password?: boolean }) => {
      calls.create++;
      const uuid = String(++seq);
      const s: Share = { uuid, url: `https://files.example/${body.kind === 'drop' ? 'd' : 's'}/${uuid}`, kind: body.kind ?? 'download' };
      shares.push(s);
      return { share: { url: s.url, password_pin: body.password ? '4242' : null, expires_at: null } };
    }),
    revokeShare: vi.fn(async (uuid: string) => {
      calls.revoke++;
      shares = shares.filter((s) => s.uuid !== uuid);
    }),
    addPermission: vi.fn(async () => {
      calls.addPermission++;
    }),
    resolveEmail: vi.fn(async (addr: string) =>
      addr === 'known@example.com' ? { found: true, user: { id: 7, role: 'user' } } : { found: false },
    ),
    searchUsers: vi.fn(async () => ({ users: [] })),
  };
  return { api: api as unknown as FileApi, calls, live: () => shares };
}

function dialog(props: Record<string, unknown>) {
  return mount(PermissionsModal, {
    props: { path: 'depo://Projeler', isDir: true, locale: 'en', shareMaxTtlDays: 7, ...props },
    attachTo: document.body,
  });
}

describe('one of everything', () => {
  it('the switch makes THE link; "Create link" with a link on REPLACES it — never a second live link', async () => {
    const { api, calls, live } = fakeApi({ perms: 'owner' });
    const w = dialog({ api });
    await flushPromises();

    await w.get('[data-testid="share-switch"]').trigger('click');
    await flushPromises();
    expect(live().filter((s) => s.kind !== 'drop')).toHaveLength(1);
    const first = live()[0].url;

    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    await w.get('[data-testid="share-pin-switch"]').trigger('click');
    const make = w.get('[data-testid="share-create"]');
    // With a link on, the button says what it does to it, and the note says what it costs.
    expect(make.text()).toBe(en['access.ui.replace_link']);
    expect(w.get('[data-testid="share-replace-hint"]').text()).toBe(en['access.ui.replace_link_hint']);
    await make.trigger('click');
    await flushPromises();

    const downloads = live().filter((s) => s.kind !== 'drop');
    expect(downloads).toHaveLength(1);
    expect(downloads[0].url).not.toBe(first);
    expect(calls.create).toBe(2);
    expect(calls.revoke).toBe(1);
    w.unmount();
  });

  it('the header’s link is not listed a second time underneath', async () => {
    const { api } = fakeApi({
      perms: 'owner',
      shares: [{ uuid: '1', url: 'https://files.example/s/1', kind: 'download' }],
    });
    const w = dialog({ api });
    await flushPromises();
    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    expect(w.text().split('https://files.example/s/1').length - 1).toBe(1);
    expect(w.find('[data-testid="share-other-links"]').exists()).toBe(false);
    w.unmount();
  });

  it('an older extra link is still listed — once — so it can be copied or revoked', async () => {
    const { api } = fakeApi({
      perms: 'owner',
      shares: [
        { uuid: '1', url: 'https://files.example/s/1', kind: 'download' },
        { uuid: '2', url: 'https://files.example/s/2', kind: 'download' },
      ],
    });
    const w = dialog({ api });
    await flushPromises();
    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    const others = w.get('[data-testid="share-other-links"]');
    expect(others.text()).toContain('https://files.example/s/2');
    expect(others.text()).not.toContain('https://files.example/s/1');
    w.unmount();
  });

  it('one Copy button, one on/off control', async () => {
    const { api } = fakeApi({ perms: 'owner' });
    const w = dialog({ api });
    await flushPromises();
    await w.get('[data-testid="share-options-toggle"]').trigger('click');
    await w.get('[data-testid="share-pin-switch"]').trigger('click');
    await w.get('[data-testid="share-create"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="share-drop-toggle"]').trigger('click');
    await w.findAll('button').find((b) => b.text() === en['access.ui.upload_limits'])!.trigger('click');

    const copies = w.findAll('button').filter((b) => b.text() === en['access.copy']);
    // link, PIN, command line — every one the same control
    expect(copies.length).toBeGreaterThanOrEqual(3);
    for (const c of copies) expect(c.classes()).toEqual(['fe-share__copy']);
    // no native checkbox anywhere: PIN (link), PIN (drop), ask-name are switches
    expect(w.findAll('input[type="checkbox"]')).toHaveLength(0);
    expect(w.findAll('[role="switch"]').length).toBe(4);
    w.unmount();
  });

  it('"will be sent to" is one sentence with the address in it — no space before the full stop', async () => {
    const { api } = fakeApi({ perms: 'owner' });
    const w = dialog({ api });
    await flushPromises();
    await w.get('[data-testid="share-people-toggle"]').trigger('click');
    const input = w.get('[data-testid="share-add-person"] input');
    await input.setValue('stranger@example.com');
    await input.trigger('keyup.enter');
    await flushPromises();
    await w.findAll('button').find((b) => b.text() === en['access.ui.just_send_a_share_link'])!.trigger('click');
    expect(w.text()).toContain('Create a link, then it will be sent to stranger@example.com.');
    expect(w.text()).not.toMatch(/stranger@example\.com \./);
    w.unmount();
  });
});

describe('nothing it cannot deliver (#21)', () => {
  it('an editor is not asked for the grant list at all', async () => {
    const { api, calls } = fakeApi({ perms: 'forbidden' });
    const w = dialog({ api, perm: 'editor' });
    await flushPromises();
    expect(calls.listPermissions).toBe(0);
    expect(w.find('[data-testid="share-people"]').exists()).toBe(false);
    w.unmount();
  });

  it('RBAC off, administrator: the people form is there, greyed, and says where to switch RBAC on', async () => {
    const { api, calls } = fakeApi({ perms: 'owner', rbac: false });
    const w = dialog({ api, perm: 'owner', canConfigure: true });
    await flushPromises();
    await w.get('[data-testid="share-people-toggle"]').trigger('click');
    expect(w.get('[data-testid="share-rbac-off"]').text()).toBe(en['access.ui.rbac_off_here']);
    expect(en['access.ui.rbac_off_here']).not.toMatch(/\bdisk\b/i);
    const add = w.get('[data-testid="share-add-person"]');
    expect((add.get('input').element as HTMLInputElement).disabled).toBe(true);
    expect((add.findAll('button').at(-1)!.element as HTMLButtonElement).disabled).toBe(true);
    // Even a keyboard Enter on a form it greyed does nothing.
    await add.get('input').setValue('known@example.com');
    await add.get('input').trigger('keyup.enter');
    await flushPromises();
    expect(calls.addPermission).toBe(0);
    w.unmount();
  });

  it('RBAC off, not an administrator: no people section at all', async () => {
    const { api } = fakeApi({ perms: 'owner', rbac: false });
    const w = dialog({ api, perm: 'owner', canConfigure: false });
    await flushPromises();
    expect(w.find('[data-testid="share-people"]').exists()).toBe(false);
    w.unmount();
  });

  it('RBAC on, owner: the form works', async () => {
    const { api } = fakeApi({ perms: 'owner', rbac: true });
    const w = dialog({ api, perm: 'owner', canConfigure: false });
    await flushPromises();
    await w.get('[data-testid="share-people-toggle"]').trigger('click');
    expect(w.find('[data-testid="share-rbac-off"]').exists()).toBe(false);
    expect((w.get('[data-testid="share-add-person"] input').element as HTMLInputElement).disabled).toBe(false);
    w.unmount();
  });
});

// The details panel's "Manage permissions" is for an OWNER only (#21): an
// editor cannot read the grant list (403), so the button opened a dialog with
// no permissions in it.
describe('"Manage permissions" in the details panel', () => {
  const api = {
    listShares: async () => ({ shares: [] }),
    listVersions: async () => [],
    listPermissions: async () => {
      throw Object.assign(new Error('forbidden'), { status: 403 });
    },
    listComments: async () => [],
  };
  const node = (perm: string) => ({
    id: 6,
    path: 'depo://notlar.txt',
    basename: 'notlar.txt',
    type: 'file' as const,
    extension: 'txt',
    size: 5,
    last_modified: 1_757_000_000_000,
    perm,
  });

  for (const [perm, shown] of [
    ['owner', true],
    ['editor', false],
    ['viewer', false],
  ] as const) {
    it(`${perm}: ${shown ? 'offered' : 'not offered'}`, async () => {
      const w = mount(InspectorPanel, {
        props: { api: api as never, nodes: [node(perm)], dirLabel: 'depo', dirCount: 1, locale: 'en' },
        attachTo: document.body,
      });
      await new Promise((r) => setTimeout(r, 0));
      const manage = w.findAll('button').filter((b) => b.text().trim() === en['inspector.perm.manage']);
      expect(manage.length > 0).toBe(shown);
      w.unmount();
    });
  }
});
