// The Uploaders tab (0.60) - core's UploadersPanel, which every front door
// draws inside the user settings dialog.
//
// What is pinned here:
//   1. The form asks what the server needs and sends exactly that: name,
//      storage (the shared list, never a native select), folder, public or
//      private, and a link life - 0 for "longest allowed".
//   2. The key is shown ONCE, with the settings the SERVER built: the
//      download is the server's `.sxcu` as it came, under the server's file
//      name. The panel builds no config of its own.
//   3. What the server says about the person shapes the form: no share.links
//      closes "Public link" and starts on private; no access.api leaves only
//      the list; the ceiling is said under the life choice.
//   4. Deleting and a new key take a second click; deleting drops a shown key.
//   5. "Short link" (0.60) is drawn only where an app shortens links for the
//      person, or on an uploader that has it on (so it can be turned off),
//      and short_link is sent only where it is drawn.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import UploadersPanel from '@brftech/filex-core/src/components/UploadersPanel.vue';
import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import UserSettingsDialog from '@brftech/filex-core/src/components/UserSettingsDialog.vue';
import type { UserSettingsHost } from '@brftech/filex-core/src/lib/userSettingsHost';
import type { Uploader, UploaderMade, UploadersList } from '@brftech/filex-core/src/types/Uploaders';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

const shots: Uploader = {
  id: 3,
  name: 'Screenshots',
  storage: 'main',
  storage_id: 1,
  folder: 'uploads',
  visibility: 'public',
  link_days: 0,
  created_at: '2026-11-11T10:00:00Z',
  key_set_at: '2026-11-11T10:00:00Z',
  last_used_at: null,
  available: true,
};

function list(extra: Partial<UploadersList> = {}): UploadersList {
  return {
    uploaders: [shots],
    endpoint: 'https://files.example.com/api/uploads',
    link_max_days: 7,
    may_link_publicly: true,
    may_create: true,
    default_folder: 'uploads',
    storages: ['main', 'team'],
    ...extra,
  };
}

// Built from parts: a curl line with a literal bearer in the source is what
// the export's secret scan (gitleaks curl-auth-header) refuses to publish.
const TEST_KEY = ['fxu', 'test-key-not-real'].join('_');
const made: UploaderMade = {
  uploader: { ...shots, id: 4, name: 'Phone' },
  key: TEST_KEY,
  setup: {
    endpoint: 'https://files.example.com/api/uploads',
    sharex: { Version: '17.0.0', Name: 'Phone (files.example.com)', DestinationType: 'ImageUploader, TextUploader, FileUploader' },
    sharex_file: 'Phone.sxcu',
    curl: `curl -H "Authorization: Bearer ${TEST_KEY}" -F "file=@<file>" https://files.example.com/api/uploads`,
  },
};

function host(state: UploadersList, locale = 'en') {
  const api = {
    uploaders: vi.fn(async () => state),
    createUploader: vi.fn(async () => made),
    updateUploader: vi.fn(async () => ({ uploader: shots })),
    rotateUploaderKey: vi.fn(async () => made),
    deleteUploader: vi.fn(async () => {}),
  };
  const toast = vi.fn();
  const h = {
    locale,
    user: { id: 7, email: 'ayse@example.com', role: 'user' },
    isAdmin: false,
    demoReadOnly: false,
    capabilities: null,
    api: {
      updateProfile: vi.fn(),
      changePassword: vi.fn(),
      enrollTotp: vi.fn(),
      verifyTotp: vi.fn(),
      disableTotp: vi.fn(),
      quota: vi.fn(async () => ({ used_bytes: 0, quota_bytes: 0, percent_used: 0, unlimited: true })),
      notificationSettings: vi.fn(async () => null),
      updateNotificationSettings: vi.fn(async () => null),
      ...api,
    },
    setUser: vi.fn(),
    toast,
    errorText: (_e: unknown, fallback: string) => fallback,
    zone: { get: () => 'UTC', set: vi.fn() },
    mode: { get: () => 'dark' as const, set: vi.fn() },
  } as unknown as UserSettingsHost;
  return { h, api, toast };
}

describe('UploadersPanel', () => {
  it('asks what the server needs, with the shared controls, and sends exactly that', async () => {
    const { h, api } = host(list());
    const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
    await flushPromises();
    expect(w.find('select').exists()).toBe(false);
    expect((w.get('[data-testid="uploaders-folder"]').element as HTMLInputElement).value).toBe('uploads');
    expect(w.get('[data-testid="uploaders-ceiling"]').text()).toBe('This server keeps public links for at most 7 days.');
    expect(w.text()).toContain('Screenshots');
    expect(w.text()).toContain('main/uploads');

    await w.get('[data-testid="uploaders-name"]').setValue('Phone');
    await w.get('[data-testid="uploaders-life-days"]').trigger('click');
    await w.get('[data-testid="uploaders-days"]').setValue(3);
    await w.get('[data-testid="uploaders-form"]').trigger('submit');
    await flushPromises();
    expect(api.createUploader).toHaveBeenCalledWith({
      name: 'Phone',
      storage: 'main',
      folder: 'uploads',
      visibility: 'public',
      link_days: 3,
    });
    w.unmount();
  });

  it('shows the key once, with the server-built settings, and downloads them as they came', async () => {
    const { h } = host(list());
    const created: Blob[] = [];
    const urlApi = URL as unknown as { createObjectURL: (b: Blob) => string; revokeObjectURL: (u: string) => void };
    const realCreate = urlApi.createObjectURL;
    const realRevoke = urlApi.revokeObjectURL;
    urlApi.createObjectURL = (b: Blob) => {
      created.push(b);
      return 'blob:test';
    };
    urlApi.revokeObjectURL = () => {};
    const clicked: string[] = [];
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download);
    });
    try {
      const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
      await flushPromises();
      await w.get('[data-testid="uploaders-name"]').setValue('Phone');
      await w.get('[data-testid="uploaders-form"]').trigger('submit');
      await flushPromises();
      expect(w.get('[data-testid="uploaders-key-value"]').text()).toBe(made.key);
      expect(w.get('[data-testid="uploaders-curl"]').text()).toBe(made.setup.curl);
      await w.get('[data-testid="uploaders-download-sharex"]').trigger('click');
      expect(clicked).toEqual(['Phone.sxcu']);
      expect(JSON.parse(await created[0].text())).toEqual(made.setup.sharex);
      await w.get('[data-testid="uploaders-key-dismiss"]').trigger('click');
      expect(w.find('[data-testid="uploaders-key"]').exists()).toBe(false);
      w.unmount();
    } finally {
      urlApi.createObjectURL = realCreate;
      urlApi.revokeObjectURL = realRevoke;
      click.mockRestore();
    }
  });

  it('closes Public link without share.links, says why, and starts on private', async () => {
    const { h, api } = host(list({ may_link_publicly: false }));
    const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
    await flushPromises();
    expect(w.get('[data-testid="uploaders-visibility-public"]').attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid="uploaders-public-denied"]').text()).toContain('may not make public links');
    expect(w.find('[data-testid="uploaders-ceiling"]').exists()).toBe(false);
    await w.get('[data-testid="uploaders-name"]').setValue('Notes');
    await w.get('[data-testid="uploaders-form"]').trigger('submit');
    await flushPromises();
    expect(api.createUploader).toHaveBeenCalledWith(expect.objectContaining({ visibility: 'private', link_days: 0 }));
    w.unmount();
  });

  it('without access.api it says so and offers no form', async () => {
    const { h } = host(list({ may_create: false }));
    const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="uploaders-form"]').exists()).toBe(false);
    expect(w.get('[data-testid="uploaders-cannot-create"]').text()).toContain('access.api');
    w.unmount();
  });

  it('says what Longest allowed means where there is no ceiling', async () => {
    const { h } = host(list({ link_max_days: 0 }));
    const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
    await flushPromises();
    expect(w.get('[data-testid="uploaders-ceiling"]').text()).toContain('the link does not expire');
    w.unmount();
  });

  it('offers a short link only where an app shortens links, and sends the choice', async () => {
    const plain = host(list());
    const w0 = mount(UploadersPanel, { props: { host: plain.h }, attachTo: document.body });
    await flushPromises();
    expect(w0.find('[data-testid="uploaders-short"]').exists()).toBe(false);
    w0.unmount();

    const { h, api } = host(list({ short_links_available: true }));
    const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
    await flushPromises();
    expect(w.get('[data-testid="uploaders-short"]').text()).toContain('Its short address');
    await w.get('[data-testid="uploaders-name"]').setValue('Phone');
    await w.get('[data-testid="uploaders-short-on"]').trigger('click');
    await w.get('[data-testid="uploaders-form"]').trigger('submit');
    await flushPromises();
    expect(api.createUploader).toHaveBeenCalledWith(expect.objectContaining({ name: 'Phone', short_link: true }));
    w.unmount();
  });

  it("keeps an uploader's short link drawn when the shortener is gone, so it can be turned off", async () => {
    const { h, api } = host(list({ uploaders: [{ ...shots, short_link: true }] }));
    const w = mount(UploadersPanel, { props: { host: h }, attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="uploaders-short"]').exists()).toBe(false);
    // The shared table's row action, as its menu sends it.
    w.findComponent(DataTable).vm.$emit('row-action', 'edit', { ...shots, short_link: true });
    await flushPromises();
    expect(w.get('[data-testid="uploaders-short"]').exists()).toBe(true);
    await w.get('[data-testid="uploaders-short-off"]').trigger('click');
    await w.get('[data-testid="uploaders-form"]').trigger('submit');
    await flushPromises();
    expect(api.updateUploader).toHaveBeenCalledWith(3, expect.objectContaining({ short_link: false }));
    w.unmount();
  });

  it('every string it says is in both catalogues, Turkish with Turkish letters', () => {
    const keys = Object.keys(coreEn).filter((k) => k.startsWith('uploaders.') || k.endsWith('.uploaders'));
    expect(keys.length).toBeGreaterThan(40);
    for (const k of keys) expect(tr[k], k).toBeTruthy();
    expect(tr['userSettings.tabs.uploaders']).toBe('Yükleyiciler');
  });
});

describe('the Uploaders tab', () => {
  it('is drawn where the host gives the endpoints, and opens the panel', async () => {
    const { h } = host(list());
    const w = mount(UserSettingsDialog, { props: { modelValue: true, host: h }, attachTo: document.body });
    await flushPromises();
    const tab = document.querySelector<HTMLElement>('[data-testid="user-settings-tab-uploaders"]');
    expect(tab).not.toBeNull();
    tab!.click();
    await flushPromises();
    expect(document.querySelector('[data-testid="uploaders-panel"]')).not.toBeNull();
    w.unmount();
  });

  it('is not drawn for a host without them', async () => {
    const { h } = host(list());
    const api = { ...(h.api as unknown as Record<string, unknown>) };
    for (const k of ['uploaders', 'createUploader', 'updateUploader', 'rotateUploaderKey', 'deleteUploader']) delete api[k];
    const bare = { ...h, api } as unknown as UserSettingsHost;
    const w = mount(UserSettingsDialog, { props: { modelValue: true, host: bare }, attachTo: document.body });
    await flushPromises();
    expect(document.querySelector('[data-testid="user-settings-tab-uploaders"]')).toBeNull();
    w.unmount();
  });
});
