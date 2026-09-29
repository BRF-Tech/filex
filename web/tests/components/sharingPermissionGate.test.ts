// Every door that makes a link asks the account's sharing permissions — the
// same ones the server asks (0.49.0 doc audit). "+ New → Request files" was
// still offered to an account without `share.upload_links`, and the server
// refused the request; the share dialog's link switch and file-request
// section, and the details panel's "Create link", were offered the same way.
//
// One answer (lib/sharingHeld), read by every door in the core, so the web
// app, the desktop app and the embeds hide the same things.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';
import ContextMenu from '@brftech/filex-core/src/components/ContextMenu.vue';
import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import { ALL_SHARING, canShareAny, sharingHeld } from '@brftech/filex-core/src/lib/sharingHeld';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';

afterEach(() => {
  document.body.innerHTML = '';
});

describe('lib/sharingHeld — one answer for the three kinds', () => {
  it('asks each kind by its permission key', () => {
    const asked: string[] = [];
    const got = sharingHeld((p) => {
      asked.push(p);
      return p !== 'share.upload_links';
    });
    expect(asked).toEqual(['share.links', 'share.upload_links', 'share.users']);
    expect(got).toEqual({ links: true, uploadLinks: false, users: true });
  });

  it('Share is worth offering when one kind applies — a file takes no file request', () => {
    const onlyDrop = { links: false, uploadLinks: true, users: false };
    expect(canShareAny(onlyDrop, true)).toBe(true);
    expect(canShareAny(onlyDrop, false)).toBe(false);
    expect(canShareAny({ links: false, uploadLinks: false, users: false }, true)).toBe(false);
    expect(canShareAny({ ...onlyDrop, users: true }, false)).toBe(true);
    expect(canShareAny(ALL_SHARING, false)).toBe(true);
  });
});

describe('"+ New → Request files"', () => {
  function nav(extra: Record<string, unknown> = {}) {
    return mount(SideNav, {
      attachTo: document.body,
      props: {
        expanded: true,
        activeView: '',
        storages: [{ name: 'docs' }],
        locale: 'en',
        showIdentitySurfaces: true,
        trashVisible: true,
        canWrite: true,
        canRequestFiles: true,
        ...extra,
      },
    });
  }
  const newMenuKeys = (w: ReturnType<typeof nav>) =>
    (w.findAllComponents(ContextMenu)[0].props('actions') as Array<{ key: string }>).map((a) => a.key);

  it('is offered when the host says nothing — the server decides', () => {
    expect(newMenuKeys(nav())).toContain('request-files');
  });

  it('is not offered at all without share.upload_links', () => {
    const keys = newMenuKeys(nav({ requestFilesAllowed: false }));
    expect(keys).not.toContain('request-files');
    // …and the divider that only separated it goes with it.
    expect(keys).not.toContain('new-sep');
    expect(keys).toEqual(expect.arrayContaining(['upload', 'new-folder']));
  });

  it('a folder that cannot take one still greys it, as before', () => {
    const menu = nav({ requestFilesAllowed: true, canRequestFiles: false }).findAllComponents(ContextMenu)[0];
    const row = (menu.props('actions') as Array<{ key: string; disabled?: boolean }>).find((a) => a.key === 'request-files');
    expect(row?.disabled).toBe(true);
  });
});

type Share = { uuid: string; url: string; kind?: string };

function fakeApi() {
  const shares: Share[] = [];
  const api = {
    listPermissions: vi.fn(async () => ({ direct: [], inherited: [], storage_rbac: true })),
    listShares: vi.fn(async () => ({ shares: [...shares] })),
    createShare: vi.fn(async (body: { kind?: string }) => {
      const s = { uuid: String(shares.length + 1), url: 'https://files.example/s/1', kind: body.kind ?? 'download' };
      shares.push(s);
      return { share: { url: s.url, password_pin: null, expires_at: null } };
    }),
    revokeShare: vi.fn(async () => {}),
    searchUsers: vi.fn(async () => ({ users: [] })),
  };
  return api;
}

function dialog(props: Record<string, unknown>) {
  return mount(PermissionsModal, {
    props: { path: 'depo://Projeler', isDir: true, locale: 'en', shareMaxTtlDays: 7, perm: 'owner', ...props },
    attachTo: document.body,
  });
}
const has = (w: ReturnType<typeof dialog>, id: string) => w.find(`[data-testid="${id}"]`).exists();

describe('the share dialog draws only what the account may make', () => {
  it('everything, when nothing narrows it', async () => {
    const api = fakeApi();
    const w = dialog({ api: api as unknown as FileApi });
    await flushPromises();
    expect(has(w, 'share-lead')).toBe(true);
    expect(has(w, 'share-link-section')).toBe(true);
    expect(has(w, 'share-drop')).toBe(true);
    expect(has(w, 'share-people')).toBe(true);
    w.unmount();
  });

  it('no share.links: no link switch, no link options — and no link is made', async () => {
    const api = fakeApi();
    const w = dialog({ api: api as unknown as FileApi, sharing: { links: false, uploadLinks: true, users: true } });
    await flushPromises();
    expect(has(w, 'share-switch')).toBe(false);
    expect(has(w, 'share-lead')).toBe(false);
    expect(has(w, 'share-link-section')).toBe(false);
    expect(has(w, 'share-drop')).toBe(true);
    expect(has(w, 'share-people')).toBe(true);
    expect(api.createShare).not.toHaveBeenCalled();
    w.unmount();
  });

  it('no share.upload_links: "Request files" opens a dialog without the file request', async () => {
    const api = fakeApi();
    const w = dialog({
      api: api as unknown as FileApi,
      initialTab: 'drop',
      sharing: { links: true, uploadLinks: false, users: true },
    });
    await flushPromises();
    expect(has(w, 'share-drop')).toBe(false);
    expect(has(w, 'share-drop-toggle')).toBe(false);
    expect(has(w, 'share-lead')).toBe(true);
    w.unmount();
  });

  it('no share.users: no people section, and the grant list is not even asked for', async () => {
    const api = fakeApi();
    const w = dialog({ api: api as unknown as FileApi, sharing: { links: true, uploadLinks: true, users: false } });
    await flushPromises();
    expect(has(w, 'share-people')).toBe(false);
    expect(api.listPermissions).not.toHaveBeenCalled();
    w.unmount();
  });

  it('"just send a share link" is not offered without share.links', async () => {
    const api = {
      ...fakeApi(),
      resolveEmail: vi.fn(async () => ({ found: false })),
    };
    const w = dialog({ api: api as unknown as FileApi, sharing: { links: false, uploadLinks: true, users: true } });
    await flushPromises();
    await w.get('[data-testid="share-people-toggle"]').trigger('click');
    await w.get('[data-testid="share-add-person"] input').setValue('nobody@example.com');
    await w.get('[data-testid="share-add-person"] .fe-share__btn--primary').trigger('click');
    await flushPromises();
    expect(w.text()).toContain(en['access.ui.no_account_for_this_email_what_next']);
    expect(has(w, 'share-send-link-instead')).toBe(false);
    w.unmount();
  });
});

describe('the details panel', () => {
  const node = {
    id: 6,
    path: 'depo://notlar.txt',
    basename: 'notlar.txt',
    type: 'file' as const,
    extension: 'txt',
    size: 5,
    last_modified: 1_757_000_000_000,
    perm: 'owner',
  };
  const api = {
    listShares: async () => ({ shares: [] }),
    listVersions: async () => [],
    listPermissions: async () => ({ direct: [], inherited: [], storage_rbac: true }),
    listComments: async () => [],
    createShare: vi.fn(async () => ({ share: { url: 'x' } })),
  };
  function panel(extra: Record<string, unknown> = {}) {
    return mount(InspectorPanel, {
      props: { api: api as never, nodes: [node] as never, dirLabel: 'depo', dirCount: 1, locale: 'en', ...extra },
      attachTo: document.body,
    });
  }
  // "Manage access" under People with access, "Manage permissions" when the
  // panel shows only the level — the same door, whichever section draws it.
  const manage = (w: ReturnType<typeof panel>) =>
    w
      .findAll('button')
      .filter((b) => [en['inspector.people.manage'], en['inspector.perm.manage']].includes(b.text().trim()));

  it('offers "Create link" and "Manage" when nothing narrows it', async () => {
    const w = panel();
    await flushPromises();
    expect(w.find('[data-testid="inspector-create-link"]').exists()).toBe(true);
    expect(manage(w).length).toBeGreaterThan(0);
    w.unmount();
  });

  it('no "Create link" without share.links, no "Manage" without share.users', async () => {
    const w = panel({ sharing: { links: false, uploadLinks: true, users: false } });
    await flushPromises();
    expect(w.find('[data-testid="inspector-create-link"]').exists()).toBe(false);
    expect(manage(w)).toHaveLength(0);
    w.unmount();
  });
});

describe('the explorer wires every door to the one answer', () => {
  const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

  it('Share is gated on the kinds that apply to the selection, not on any sharing permission', () => {
    expect(src).toMatch(/if \(a\.key === 'access'\)[\s\S]{0,300}canShareAny\(sharingAt\(paths\), dirs\)/);
    expect(src).not.toMatch(/access: \['share\.links', 'share\.upload_links', 'share\.users'\]/);
  });

  it('the New menu, the dialog and the details panel each get the answer', () => {
    expect(src).toMatch(/:request-files-allowed="requestFilesHeld/);
    expect(src).toMatch(/sharingAt\(currentPath\.value \? \[qualify\(currentPath\.value\)\] : \[\]\)\.uploadLinks/);
    expect(src).toMatch(/:sharing="permTargetSharing/);
    expect(src).toMatch(/:sharing="inspectorSharing/);
    expect(src).toMatch(/if \(!path \|\| !requestFilesHeld\.value\) return;/);
  });

  it('the Share key opens the dialog only where the menu offers Share', () => {
    expect(src).toMatch(/onShare: \(\) => \{[\s\S]{0,120}offeredOn\('access', targets\)/);
  });
});
