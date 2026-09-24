// What one row of "Paylaştıklarım" / "My shares" offers.
//
// The product owner, 2026-09-20: *"paylaşımın sahibi ve admin alabilir
// şifreyi. Paylaşım sahibi paylaştıklarını da bir menüde görebilir olsun,
// 'Paylaştıklarım' diye. Admin değilse göremez, kopyalayamaz."*
//
// This screen is the second half of that sentence, and the PIN is the first.
// What has to stay true here, in the order the assertions run:
//
//   · a person's own links are listed at all, with what is shared, the link,
//     whether a PIN is set, when it lapses and a way to revoke;
//   · "Copy PIN" is offered ONLY where there is a PIN that can still be shown
//     — a link with none, and a link minted before the PIN could be kept, are
//     different rows and say different things;
//   · the PIN never touches the DOM. It goes from the server to the
//     clipboard, because a PIN painted into a row is a PIN in a screenshot,
//     over a shoulder and in a shared-screen recording;
//   · the LISTING never carries it either — the row asks for it, one row at a
//     time, from an endpoint that audits every read;
//   · and when it cannot be shown, the person is told WHICH of the three
//     cases it is, because only one of them is something anybody can fix.
//
// ⚠ The row's verbs are in the menu its ONE pinned control opens, not in the
// row: every table row ends in a single "Actions" / "Aksiyon" control
// (DataTable's `row-actions` → core RowActions → the explorer's own
// ContextMenu). That is why the
// affordances are counted as MENU ENTRIES — counting buttons would pass while
// measuring nothing.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { useToastStore } from '@/stores/toast';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

// Three links, all the caller's own — which is the only kind this endpoint
// returns. Shape is the backend's ShareWithMeta envelope verbatim, minus the
// creator's address (handlers/shares_mine.go clears it: on this page the
// creator is always the person reading it).
const withPin = {
  share: {
    id: 13,
    node_id: 57,
    token: 'fc8275aa3a8c6c4aabfdc5459815fcfa',
    has_pin: true,
    pin_recoverable: true,
    download_count: 0,
    max_downloads: 5,
    created_by: 2,
    created_at: '2026-09-20T12:56:06Z',
    expires_at: '2036-01-01T00:00:00Z',
  },
  node_path: '/teklif.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/fc8275aa3a8c6c4aabfdc5459815fcfa',
};

const withoutPin = {
  share: {
    id: 1,
    node_id: 12,
    token: 'bce88390ab12cd34ef56ab78cd90ef12',
    has_pin: false,
    pin_recoverable: false,
    download_count: 3,
    created_by: 2,
    created_at: '2026-09-19T09:10:11Z',
  },
  node_path: '/rapor.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/bce88390ab12cd34ef56ab78cd90ef12',
};

/** A link from before the PIN could be kept: PIN required, PIN unknowable. */
const pinUnrecoverable = {
  share: {
    id: 7,
    node_id: 99,
    token: 'aa11bb22cc33dd44ee55ff6677889900',
    has_pin: true,
    pin_recoverable: false,
    download_count: 1,
    created_by: 2,
    created_at: '2026-06-18T08:00:00Z',
  },
  node_path: '/eski.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/aa11bb22cc33dd44ee55ff6677889900',
};

const PLAIN_PIN = '834595';

/** A link its owner REVOKED (00053: `revoked_at` beside the expiry, which
 *  revoking sets to the same moment) and one that simply ran out. Before the
 *  column both read "Expired" (QA, 2026-09-21). */
const revokedLink = {
  share: {
    id: 31,
    node_id: 70,
    token: 'ab12ab12ab12ab12ab12ab12ab12ab12',
    has_pin: false,
    pin_recoverable: false,
    download_count: 0,
    created_by: 2,
    created_at: '2026-09-21T10:00:00Z',
    expires_at: '2026-09-21T10:05:00Z',
    revoked_at: '2026-09-21T10:05:00Z',
  },
  node_path: '/iptal.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/ab12ab12ab12ab12ab12ab12ab12ab12',
};
const ranOut = {
  share: {
    id: 32,
    node_id: 71,
    token: 'cd34cd34cd34cd34cd34cd34cd34cd34',
    has_pin: false,
    pin_recoverable: false,
    download_count: 0,
    created_by: 2,
    created_at: '2026-09-01T10:00:00Z',
    expires_at: '2026-09-08T10:00:00Z',
  },
  node_path: '/eski.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/cd34cd34cd34cd34cd34cd34cd34cd34',
};

/** What the PIN endpoint answers, per share id. Tests rewrite it. */
let pinAnswers: Record<number, unknown> = {};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, fallback: string) => fallback,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/shares') {
        return {
          data: {
            items: [withPin, withoutPin, pinUnrecoverable, revokedLink, ranOut],
            total: 5,
            page: 1,
            page_size: 25,
          },
        };
      }
      const pin = /^\/shares\/(\d+)\/pin$/.exec(url);
      if (pin) {
        const answer = pinAnswers[Number(pin[1])];
        if (answer instanceof Error) throw answer;
        return { data: answer ?? { pin: null, reason: 'not_recoverable' } };
      }
      return { data: {} };
    }),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const routerPush = vi.fn();
vi.mock('vue-router', () => ({ useRouter: () => ({ push: routerPush }) }));

import MyShares from '@/views/MyShares.vue';
import { api } from '@/api/client';

const written: string[] = [];

function mountPage(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(MyShares, { global: { plugins: [i18n] }, attachTo: document.body });
}

/** The newest element with this test id. ⚠ A dialog teleports to <body>, and
 *  an earlier test's dialog can still be there — the first match would then be
 *  that test's, in that test's language. */
const lastOf = (id: string): HTMLElement | null => {
  const all = document.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`);
  return all.length ? all[all.length - 1] : null;
};

/** A row's ONE control, and the ids its entries take (`<control>-<verb>`). */
const actionsOf = (id: number) => `my-share-actions-${id}`;

describe('My shares — the row', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    closeRowMenus();
    written.length = 0;
    routerPush.mockClear();
    pinAnswers = { 13: { pin: PLAIN_PIN } };
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: vi.fn(async (v: string) => {
          written.push(v);
        }),
      },
    });
  });

  it('lists the caller’s own links, and asks the user-scoped endpoint for them', async () => {
    const w = mountPage();
    await flushPromises();

    // ⚠ `/shares`, never `/admin/shares`: the admin listing is every user's
    // links and is refused outright to the account this page exists for.
    expect(vi.mocked(api.get).mock.calls.map((c) => c[0])).toContain('/shares');
    expect(vi.mocked(api.get).mock.calls.map((c) => c[0])).not.toContain('/admin/shares');

    expect(w.find('[data-testid="my-share-row-13"]').exists()).toBe(true);
    expect(w.find('[data-testid="my-share-row-1"]').exists()).toBe(true);
    // What is shared, in the row — a link with no name is a link nobody can
    // tell from the next one.
    expect(w.get('[data-testid="my-share-row-13"]').text()).toContain('/teklif.pdf');
    expect(w.get('[data-testid="my-share-row-13"]').text()).toContain('depo');
  });

  it('says whether a PIN is set — and separately whether it can be shown', async () => {
    const w = mountPage();
    await flushPromises();

    // A PIN that can still be shown.
    expect(w.get('[data-testid="my-share-row-13"]').find('[data-testid="my-share-pin"]').exists()).toBe(
      true,
    );
    // No PIN at all.
    expect(
      w.get('[data-testid="my-share-row-1"]').find('[data-testid="my-share-no-pin"]').exists(),
    ).toBe(true);
    // ⚠ The third state, and the one a row is most likely to collapse into one
    // of the other two: PIN required, PIN unknowable.
    const old = w.get('[data-testid="my-share-row-7"]');
    expect(old.find('[data-testid="my-share-pin-hidden"]').exists()).toBe(true);
    expect(old.find('[data-testid="my-share-pin"]').exists()).toBe(false);
    expect(old.text()).toContain(en.myShares.pinHidden);
  });

  it('offers "Copy PIN" only where there is a PIN that can be shown', async () => {
    const w = mountPage();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    expect(menuEntries().map((e) => e.label)).toContain(en.myShares.copyPin);
    closeRowMenus();

    for (const id of [1, 7]) {
      await openRowMenu(w, actionsOf(id));
      expect(
        menuEntries().map((e) => e.label),
        `row ${id} offers a PIN it does not have`,
      ).not.toContain(en.myShares.copyPin);
      // …and the link is still copyable, which is the whole point of the row.
      expect(menuEntries().map((e) => e.label)).toContain(en.myShares.copyLink);
      closeRowMenus();
    }
  });

  it('copies the server’s canonical link, not one rebuilt from the address bar', async () => {
    const w = mountPage();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-copy-link`);
    closeRowMenus();
    await flushPromises();

    expect(written).toEqual([withPin.url]);
    expect(useToastStore().toasts.map((t) => t.message)).toContain(en.myShares.linkCopied);
  });

  it('fetches the PIN on demand and puts it on the clipboard — never on the screen', async () => {
    const w = mountPage();
    await flushPromises();

    // Before anything is picked, nothing has asked for a PIN and none is drawn.
    expect(vi.mocked(api.get).mock.calls.map((c) => c[0])).not.toContain('/shares/13/pin');
    expect(w.html()).not.toContain(PLAIN_PIN);

    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-copy-pin`);
    closeRowMenus();
    await flushPromises();

    expect(vi.mocked(api.get).mock.calls.map((c) => c[0])).toContain('/shares/13/pin');
    expect(written).toEqual([PLAIN_PIN]);
    expect(useToastStore().toasts.map((t) => t.message)).toContain(en.myShares.pinCopied);
    // ⚠ The decisive one. A PIN that reached the clipboard must not have
    // reached the page on the way.
    expect(w.html()).not.toContain(PLAIN_PIN);
  });

  it('never carries a PIN in the listing itself', async () => {
    const w = mountPage();
    await flushPromises();
    const html = w.html();
    expect(html).not.toContain(PLAIN_PIN);
    expect(html).not.toContain('pin_hash');
    expect(html).not.toContain('pin_enc');
    expect(html).not.toContain('enc:v1:');
  });

  it('says WHICH kind of "cannot be shown" it is, rather than "copy failed"', async () => {
    // ⚠ Three reasons, three sentences. Only `no_secret_key` is something a
    // person can act on (an administrator sets FILEX_SECRET_KEY), so folding
    // the three into one message would send everybody to the wrong place.
    for (const [reason, sentence] of [
      ['no_secret_key', en.myShares.pinReason.no_secret_key],
      ['not_recoverable', en.myShares.pinReason.not_recoverable],
      ['no_pin', en.myShares.pinReason.no_pin],
    ] as const) {
      setActivePinia(createPinia());
      pinAnswers = { 13: { pin: null, reason } };
      const w = mountPage();
      await flushPromises();

      await openRowMenu(w, actionsOf(13));
      await pickMenuItem(`${actionsOf(13)}-copy-pin`);
      closeRowMenus();
      await flushPromises();

      expect(useToastStore().toasts.map((t) => t.message), reason).toContain(sentence);
      expect(written, 'nothing may reach the clipboard when there was no PIN').toEqual([]);
      w.unmount();
    }
  });

  it('a refusal copies nothing and says nothing it does not know', async () => {
    // What a NON-OWNER gets from the endpoint is 403 (the server checks; this
    // page cannot). It reaches the view as a thrown request, and the row must
    // end where it started: no clipboard write, no PIN anywhere, an error the
    // person can see.
    setActivePinia(createPinia());
    pinAnswers = { 13: new Error('403') };
    const w = mountPage();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-copy-pin`);
    closeRowMenus();
    await flushPromises();

    expect(written).toEqual([]);
    expect(w.html()).not.toContain(PLAIN_PIN);
    const toasts = useToastStore().toasts;
    expect(toasts.map((t) => t.level)).toContain('error');
    expect(toasts.map((t) => t.message)).toContain(en.errors.generic);
  });

  it('offers revoke, and confirms before it fires', async () => {
    const w = mountPage();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    expect(menuEntries().find((e) => e.label === en.myShares.revoke)?.danger).toBe(true);
    await pickMenuItem(`${actionsOf(13)}-revoke`);
    closeRowMenus();
    await flushPromises();

    // The verb asks first — an irreversible act behind an unlabelled menu
    // entry is one people fire by accident.
    expect(vi.mocked(api.delete)).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(en.myShares.revokeConfirm);
    // ⚠ The title is the QUESTION and the buttons are the two answers in
    // words (QA, 2026-09-21: "İptal et" over "İptal" / "Onayla").
    expect(document.body.textContent).toContain(en.myShares.revokeTitle);
    expect(lastOf('my-share-revoke-confirm')?.textContent?.trim()).toBe(
      en.myShares.revokeDo,
    );
    expect(lastOf('my-share-revoke-dismiss')?.textContent?.trim()).toBe(
      en.common.dismiss,
    );

    await w.get('[data-testid="my-share-revoke-confirm"]').trigger('click');
    await flushPromises();
    expect(vi.mocked(api.delete)).toHaveBeenCalledWith('/files/share/13');
  });

  it('a revoked link reads as REVOKED — and one that ran out still reads as expired', async () => {
    const w = mountPage('tr');
    await flushPromises();
    const revokedRow = w.get('[data-testid="my-share-row-31"]');
    expect(revokedRow.get('[data-testid="my-share-revoked"]').text()).toBe(tr.myShares.revoked);
    expect(revokedRow.text()).not.toContain(tr.myShares.expired);
    const ranOutRow = w.get('[data-testid="my-share-row-32"]');
    expect(ranOutRow.find('[data-testid="my-share-revoked"]').exists()).toBe(false);
    expect(ranOutRow.text()).toContain(tr.myShares.expired);
    // Neither offers to revoke again.
    await openRowMenu(w, actionsOf(31));
    expect(menuEntries().map((e) => e.label)).not.toContain(tr.myShares.revoke);
    closeRowMenus();
  });

  it('the Turkish revoke dialog never uses one word for both answers', async () => {
    // In Turkish "iptal" is both "cancel" and "revoke": the old dialog said
    // "İptal et" (title) over "İptal" (the SAFE button) / "Onayla".
    const w = mountPage('tr');
    await flushPromises();
    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-revoke`);
    closeRowMenus();
    await flushPromises();
    const dismiss = lastOf('my-share-revoke-dismiss')?.textContent?.trim();
    const confirm = lastOf('my-share-revoke-confirm')?.textContent?.trim();
    expect(dismiss).toBe('Vazgeç');
    expect(confirm).toBe('Bağlantıyı iptal et');
    expect(document.body.textContent).toContain('Bu bağlantı iptal edilsin mi?');
    expect(dismiss).not.toMatch(/iptal/i);
    w.unmount();
  });

  it('speaks Turkish with Turkish letters', async () => {
    const w = mountPage('tr');
    await flushPromises();
    expect(w.get('[data-testid="my-shares-title"]').text()).toBe('Paylaştıklarım');
    expect(tr.myShares.title).toBe('Paylaştıklarım');
    await openRowMenu(w, actionsOf(13));
    expect(menuEntries().map((e) => e.label)).toContain(tr.myShares.copyPin);
    closeRowMenus();
  });
});
