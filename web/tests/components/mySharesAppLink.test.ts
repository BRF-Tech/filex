// A link an app opened, in "My shares" — the owner's decision of 2026-09-21.
//
// Signing links sat in this list as plain `/teklif.pdf` shares offering
// "Copy PIN" and "Revoke", with nothing saying they belonged to a signing
// request, and revoking one cancelled the request. They stay listed, but:
//
//   · named for what they are (the app's page declares it: "İmza isteği"),
//   · opening the request's page in the app (its home page, in this tab),
//   · and "Revoke" saying, before it fires, what it does.
//
// The row's data is the backend's db.AppLink (handlers/shares_mine.go →
// wasmplugin.Registry.LinkOf); an ordinary share has none and is unchanged.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const signingLink = {
  share: {
    id: 21,
    node_id: 7,
    token: '0123456789abcdef0123456789abcdef',
    has_pin: true,
    pin_recoverable: true,
    download_count: 0,
    visit_count: 2,
    created_by: 2,
    created_at: '2026-09-21T10:00:00Z',
    expires_at: '2036-01-01T00:00:00Z',
    plugin_id: 1,
    page_id: 'signer',
  },
  node_path: '/teklif.pdf',
  storage_name: 'depo',
  plugin_name: 'sign',
  url: 'https://files.example.com/s/0123456789abcdef0123456789abcdef',
  app: {
    plugin: 'sign',
    page: 'signer',
    label: { en: 'Signing request', tr: 'İmza isteği' },
    revoke: {
      en: 'Revoking this link cancels the signing request.',
      tr: 'Bu bağlantıyı iptal etmek imza isteğini iptal eder.',
    },
    view: 'envelopes',
    section: 'requested',
  },
};

/** The same kind of link after its owner revoked it (00053, feat/043-tables). */
const revokedSigningLink = {
  ...signingLink,
  share: {
    ...signingLink.share,
    id: 22,
    token: '1123456789abcdef0123456789abcdef',
    expires_at: '2026-09-21T10:05:00Z',
    revoked_at: '2026-09-21T10:05:00Z',
  },
  url: 'https://files.example.com/s/1123456789abcdef0123456789abcdef',
};

const plainShare = {
  share: {
    id: 3,
    node_id: 8,
    token: 'ffffffffffffffffffffffffffffffff',
    has_pin: false,
    pin_recoverable: false,
    download_count: 1,
    created_by: 2,
    created_at: '2026-09-20T10:00:00Z',
  },
  node_path: '/rapor.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/ffffffffffffffffffffffffffffffff',
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, fallback: string) => fallback,
  api: {
    get: vi.fn(async (url: string) =>
      url === '/shares'
        ? { data: { items: [signingLink, revokedSigningLink, plainShare], total: 3, page: 1, page_size: 25 } }
        : { data: {} },
    ),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));

import MyShares from '@/views/MyShares.vue';

/** The warning of the dialog that is open now (a teleported modal of an
 *  earlier mount may still be in the document — the last one is this one). */
function warning(): string {
  const all = document.querySelectorAll('[data-testid="my-share-revoke-warning"]');
  return (all[all.length - 1]?.textContent ?? '').trim();
}

function mountPage(locale = 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(MyShares, {
    global: { plugins: [i18n], stubs: { RouterLink: RouterLinkStub } },
    attachTo: document.body,
  });
}

describe('My shares — a link an app opened', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    closeRowMenus();
  });

  it('is named for what it is and opens the app’s page for it', async () => {
    const w = mountPage('tr');
    await flushPromises();
    const row = w.get('[data-testid="my-share-row-21"]');
    expect(row.text()).toContain('/teklif.pdf');
    expect(row.get('[data-testid="my-share-app-21"]').text()).toBe('İmza isteği');
    const link = row.findComponent(RouterLinkStub);
    expect(link.exists()).toBe(true);
    expect(link.props('to')).toEqual({
      name: 'app-home',
      params: { plugin: 'sign', view: 'envelopes' },
      query: { section: 'requested' },
    });
    // An ordinary share carries no such mark.
    expect(w.get('[data-testid="my-share-row-3"]').find('[data-testid="my-share-app-3"]').exists()).toBe(false);
    w.unmount();
  });

  it('says before a revoke that the revoke cancels the request', async () => {
    const w = mountPage('tr');
    await flushPromises();
    await openRowMenu(w, 'my-share-actions-21');
    await pickMenuItem('my-share-actions-21-revoke');
    await flushPromises();
    expect(warning()).toBe('Bu bağlantıyı iptal etmek imza isteğini iptal eder.');
    w.unmount();
  });

  // ⚠ Merge seam (feat/043-tables × feat/043-signing): tables rewrote the
  // dialog — the title is the question, the buttons the two answers
  // ("Vazgeç" / "Bağlantıyı iptal et") — and signing puts the APP's sentence
  // in its body. An app's link gets both: the tables frame, the app's words.
  it('an app link: the tables frame around the app’s own sentence', async () => {
    const w = mountPage('tr');
    await flushPromises();
    await openRowMenu(w, 'my-share-actions-21');
    await pickMenuItem('my-share-actions-21-revoke');
    await flushPromises();
    const lastOf = (id: string) => {
      const all = document.querySelectorAll(`[data-testid="${id}"]`);
      return (all[all.length - 1]?.textContent ?? '').trim();
    };
    expect(warning()).toBe('Bu bağlantıyı iptal etmek imza isteğini iptal eder.');
    expect(document.body.textContent).toContain(tr.myShares.revokeTitle);
    expect(lastOf('my-share-revoke-dismiss')).toBe('Vazgeç');
    expect(lastOf('my-share-revoke-confirm')).toBe('Bağlantıyı iptal et');
    w.unmount();
  });

  it('a revoked app link reads as revoked and is still named for what it is', async () => {
    const w = mountPage('tr');
    await flushPromises();
    const row = w.get('[data-testid="my-share-row-22"]');
    expect(row.get('[data-testid="my-share-revoked"]').text()).toBe('İptal edildi');
    expect(row.text()).not.toContain(tr.myShares.expired);
    expect(row.get('[data-testid="my-share-app-22"]').text()).toBe('İmza isteği');
    w.unmount();
  });

  it('asks the ordinary question for an ordinary share', async () => {
    const w = mountPage('en');
    await flushPromises();
    await openRowMenu(w, 'my-share-actions-3');
    await pickMenuItem('my-share-actions-3-revoke');
    await flushPromises();
    expect(warning()).toBe(en.myShares.revokeConfirm);
    w.unmount();
  });
});
