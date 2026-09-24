// What one row of the admin Shares page offers, with and without a PIN.
//
// The product owner, 2026-09-20: "paylaşımlar sayfasında sadece token'ı
// kopyala diye bir şeye gerek yok, pin varsa pini kopyala çıkmalı."
//
// A bare "copy token only" button opens nothing on its own, so it is gone.
//
// ⚠⚠ 2026-09-20, third change of the day, and the one that made this header
// wrong. The paragraph that stood here said "Copy PIN cannot exist:
// internal/share/service.go bcrypts the PIN on the way in and model.Share tags
// PinHash `json:"-"`, so no admin row has ever carried a plain PIN and none
// ever can". That was true when it was written and it is not true now.
// Migration 00049 SEALS a copy of the PIN beside the hash (AES-256-GCM under
// FILEX_SECRET_KEY), every row carries `pin_recoverable`, and
// `GET /api/shares/{id}/pin` hands the plain PIN to the link's creator or to
// an administrator, writing an audit row on every read — answer or refusal.
//
// Four assertions were rewritten for that, and which KIND of rewrite each one
// was matters more than the diff:
//
//   · "offers exactly one copy affordance per row" — the wrong SHAPE now, not
//     merely the wrong count. A row whose PIN can be shown holds two things
//     worth copying and they are different things. What that assertion was
//     really protecting was "no second, silent copy of the SAME thing", so it
//     now names the affordances instead of counting them: link-and-PIN where
//     the PIN is recoverable, link alone everywhere else.
//   · "offers no 'copy PIN' button, because there is no PIN to copy" —
//     INVERTED. The entry must exist, and exactly where the row says the PIN
//     is recoverable. The half of it that still holds is kept and made
//     stronger: neither the PIN nor any hash of it may appear in the row.
//   · "names where the PIN actually was" — MOVED, unchanged in substance, to a
//     fixture that is still in that world (a link minted before 00049). The
//     old sentence is exactly right for those rows, which is why both strings
//     stay in the catalogue and only the recoverable case got a new one.
//   · the Turkish assertions took on the new strings and kept pinning the old
//     ones — ASCII-fied Turkish is a defect of its own (CLAUDE.md).
//
// ⚠ Nothing was weakened about the secret itself: the PIN goes from the
// endpoint to the clipboard and must never reach the DOM, the listing or a
// toast. That is the assertion this file exists for now.
//
// ⚠⚠ 2026-09-20, same day, second change: the row's verbs moved INTO a menu.
// Every admin table row now ends in ONE labelled "Actions" / "Aksiyon" control
// (core RowActions → the same ContextMenu the explorer's ⋮ opens), because
// three unnamed icon buttons per row was the complaint right after this one:
// "adminde aksiyonlar karma karışık; … en dibe sabitli tek buton olmalı."
//
// That moved four of the assertions below, and it is worth being precise about
// WHICH kind of move each one was:
//
//   · unchanged in substance, changed in address — "copies the server's
//     canonical link" and "names what it copied": the verb is picked in the
//     teleported menu instead of clicked in the row. Same act, same assertion.
//   · rewritten deliberately — "exactly one copy affordance" and "every row
//     action has a name of its own" counted BUTTONS IN THE ROW, and after the
//     fold the row holds exactly one button on purpose. Counting buttons would
//     now pass while saying nothing. They count MENU ENTRIES instead, which is
//     where the affordances went, and they gained something: an entry's label
//     is its accessible name by construction, so "no nameless action" is
//     measured on the thing a person actually reads.
//
// Nothing was dropped: copy / revoke / delete are all still offered, revoke
// still disappears on an already-revoked link, and both destructive verbs keep
// `danger`, which is what paints them apart inside the menu.
//
// These go through the real view and the real SharesApi with only the HTTP
// layer faked, because the row's whole job is turning the wire into an offer.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { computed, ref } from 'vue';
import { TABLE_ENV, type LocaleCode } from '@brftech/filex-core';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { useToastStore } from '@/stores/toast';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

// One PIN-protected link opened by the `sign` app, one plain link opened from
// the browser. Shape is the backend's ShareWithMeta envelope verbatim.
//
// ⚠ `pin_recoverable` is the field the whole PIN half of this page turns on,
// so it is set EXPLICITLY on every fixture, including the ones where it is
// false. Leaving it off would make "no Copy PIN here" pass for the wrong
// reason — an absent field and a false one are the same to `Boolean()`, and
// then the test would not notice the day the backend stopped sending it.
const withPin = {
  share: {
    id: 13,
    node_id: 57,
    token: 'fc8275aa3a8c6c4aabfdc5459815fcfa',
    has_pin: true,
    pin_recoverable: true,
    download_count: 0,
    created_by: 1,
    created_via: 'sign',
    created_at: '2026-09-20T12:56:06Z',
    plugin_id: 38,
    page_id: 'signer',
  },
  creator_email: 'admin@local',
  node_path: '/teklif.pdf',
  storage_name: 'depo',
  plugin_name: 'sign',
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
    created_by: 1,
    created_at: '2026-09-19T09:10:11Z',
  },
  creator_email: 'admin@local',
  node_path: '/rapor.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/bce88390ab12cd34ef56ab78cd90ef12',
};

/** A link that has already been revoked: its row must not offer to revoke it
 *  again. ⚠ Without this fixture the "revoke" branch was never exercised and
 *  the assertion below could not fail — measured. */
const revoked = {
  share: {
    id: 7,
    node_id: 99,
    token: 'aa11bb22cc33dd44ee55ff6677889900',
    has_pin: false,
    pin_recoverable: false,
    download_count: 1,
    created_by: 1,
    created_at: '2026-09-18T08:00:00Z',
    revoked_at: '2026-09-19T08:00:00Z',
  },
  creator_email: 'admin@local',
  node_path: '/eski.pdf',
  storage_name: 'depo',
  url: 'https://files.example.com/s/aa11bb22cc33dd44ee55ff6677889900',
};

/** A PIN-protected link minted BEFORE migration 00049 — or on an instance
 *  with no FILEX_SECRET_KEY. The PIN is required and unknowable, which is the
 *  third state a row is most likely to collapse into one of the other two.
 *  ⚠ It is also what keeps `shares.pinHint` / `shares.pinHintApp` honest: they
 *  are still the exact truth HERE, and only here. */
const legacyPin = {
  share: {
    id: 21,
    node_id: 88,
    token: 'dd44ee55ff6677889900aa11bb22cc33',
    has_pin: true,
    pin_recoverable: false,
    download_count: 2,
    created_by: 1,
    created_via: 'sign',
    created_at: '2026-06-01T10:00:00Z',
    plugin_id: 38,
    page_id: 'signer',
  },
  creator_email: 'admin@local',
  node_path: '/sozlesme-2026.pdf',
  storage_name: 'depo',
  plugin_name: 'sign',
  url: 'https://files.example.com/s/dd44ee55ff6677889900aa11bb22cc33',
};

/** The six digits the signing job printed for this signer. It exists in this
 *  file for one purpose: to be searched for in the DOM and NOT found. */
const PLAIN_PIN = '834595';

/** What `GET /shares/{id}/pin` answers, per share id. Tests rewrite it; an
 *  `Error` is thrown, which is how a 403 reaches the view. */
let pinAnswers: Record<number, unknown> = {};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/shares') {
        return {
          data: { items: [withPin, withoutPin, revoked, legacyPin], total: 4, page: 1, page_size: 50 },
        };
      }
      // ⚠ `/shares/{id}/pin`, NOT `/admin/shares/{id}/pin`. One endpoint
      // serves the link's creator and an administrator alike
      // (handlers/shares_mine.go), and this page calls it through the same
      // `MySharesApi` client the owner's own page uses.
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

import Shares from '@/views/Shares.vue';
import { api } from '@/api/client';

const written: string[] = [];

/* ⚠ The table learns the panel's language from the HOST (core lib/tableEnv),
 * which App.vue provides once for the whole admin app. A page mounted on its
 * own has no App above it, so the test provides it the same way — without
 * this the Actions control would say "Actions" on the Turkish page, which is
 * the test being wrong about the product rather than the product being wrong. */
function mountShares(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(Shares, {
    global: {
      plugins: [i18n],
      provide: {
        [TABLE_ENV as symbol]: {
          locale: computed(() => String(i18n.global.locale.value) as LocaleCode),
          theme: ref(undefined),
        },
      },
    },
    attachTo: document.body,
  });
}

/** The row of the PIN-protected link / of the plain one / of the old link
 *  whose PIN can no longer be produced. */
const pinRow = (w: ReturnType<typeof mountShares>) => w.get('[data-testid="share-row-13"]');
const plainRow = (w: ReturnType<typeof mountShares>) => w.get('[data-testid="share-row-1"]');
const legacyRow = (w: ReturnType<typeof mountShares>) => w.get('[data-testid="share-row-21"]');

/** The newest element with this test id. ⚠ A dialog teleports to <body>, and
 *  an earlier test's dialog can still be there — the first match would then be
 *  that test's, in that test's language. */
const lastOf = (id: string): HTMLElement | null => {
  const all = document.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`);
  return all.length ? all[all.length - 1] : null;
};

/** A row's ONE control, and the ids its entries take (`<control>-<verb>`). */
const actionsOf = (id: number) => `share-actions-${id}`;

describe('Shares row actions', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    closeRowMenus();
    written.length = 0;
    vi.mocked(api.get).mockClear();
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

  it('offers one copy affordance per THING worth copying, and never two for the same thing', async () => {
    // ⚠⚠ Rewritten twice, and the second time changed what it measures.
    //
    // It began as "exactly one copy affordance per row", counting BUTTONS in
    // the row — written against a token cell that carried a silent second copy
    // of the link plus a bare "copy token". When the verbs moved into the menu
    // it kept the count and moved to menu entries. Now a row with a
    // recoverable PIN honestly has TWO things to copy, so a bare count would
    // either forbid the feature or stop meaning anything.
    //
    // So it asserts the SET, per row: the link everywhere, the PIN only where
    // the PIN can be produced, nothing else — which is the property the
    // original was protecting (no second affordance for the same thing).
    const w = mountShares();
    await flushPromises();

    const copiesIn = () => menuEntries().filter((e) => e.label.toLowerCase().includes('copy'));

    await openRowMenu(w, actionsOf(13));
    expect(copiesIn().map((e) => e.label)).toEqual([en.shares.copyLink, en.shares.copyPin]);
    closeRowMenus();

    for (const id of [1, 21]) {
      await openRowMenu(w, actionsOf(id));
      const copies = copiesIn();
      expect(copies.map((e) => e.label), `row ${id}`).toEqual([en.shares.copyLink]);
      closeRowMenus();
    }
  });

  it('copies the server\'s canonical link, not one built from the browser address', async () => {
    const w = mountShares();
    await flushPromises();

    // Same act, new address: the verb is picked in the menu the row's one
    // control opens. What it copies is the point, and it is unchanged — the
    // server's `url` from the ShareWithMeta envelope, never a link rebuilt
    // from `window.location`.
    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-copy`);
    closeRowMenus();
    await flushPromises();

    expect(written).toEqual([withPin.url]);
  });

  it('names what it copied in the toast', async () => {
    const w = mountShares();
    await flushPromises();

    await openRowMenu(w, actionsOf(1));
    await pickMenuItem(`${actionsOf(1)}-copy`);
    closeRowMenus();
    await flushPromises();

    const toast = useToastStore();
    expect(toast.toasts.map((t) => t.message)).toContain(en.shares.linkCopied);
    expect(en.shares.linkCopied.toLowerCase()).toContain('link');
  });

  it('says in words that a PIN-protected link asks for a PIN', async () => {
    const w = mountShares();
    await flushPromises();

    const pin = pinRow(w).get('[data-testid="share-pin"]');
    expect(pin.text()).toContain(en.shares.pinProtected);
    expect(pin.text()).not.toBe('PIN'); // the bare three-letter chip it used to be
  });

  it('names where the PIN actually was — on a link whose PIN really cannot be shown', async () => {
    // ⚠ MOVED, not weakened. This assertion was written against row 13, which
    // has since become a link whose PIN CAN be produced; the sentence it pins
    // ("…filex cannot show this one again — sign showed it once, when it
    // created the link") is still the exact truth for a link minted before
    // migration 00049, and row 21 is one. Left on row 13 it would have gone on
    // passing only until the day the page told the truth.
    const w = mountShares();
    await flushPromises();

    const hint = legacyRow(w).get('[data-testid="share-pin"]').attributes('aria-label') ?? '';
    expect(hint).toBe(en.shares.pinHintApp.replace('{app}', 'sign'));
    expect(hint).toContain('sign');
    expect(hint).toContain('once');
  });

  it('stops claiming the PIN is unknowable on a row that can hand it over', async () => {
    // The defect this whole change exists for: the hint said "PINs are stored
    // hashed, so filex cannot show this one again" on EVERY PIN row, including
    // the ones with a sealed PIN and a Copy PIN entry an inch away. A screen
    // that says a thing is impossible while offering it is worse than a screen
    // that says nothing.
    const w = mountShares();
    await flushPromises();

    const hint = pinRow(w).get('[data-testid="share-pin"]').attributes('aria-label') ?? '';
    expect(hint).toBe(en.shares.pinHintRecoverable);
    expect(hint).not.toContain('cannot');
    // It says the two things an operator needs before they copy: where it is,
    // and that the read is not invisible.
    expect(hint.toLowerCase()).toContain('copy');
    expect(hint.toLowerCase()).toContain('audit');
    // The old sentences stay in the catalogue — row 21 still needs them.
    expect(en.shares.pinHint).toContain('cannot show this one again');
  });

  it('says nothing about a PIN on a link that has none', async () => {
    const w = mountShares();
    await flushPromises();

    expect(plainRow(w).find('[data-testid="share-pin"]').exists()).toBe(false);
  });

  it('offers "Copy PIN" exactly where the row says the PIN can be shown', async () => {
    // ⚠⚠ INVERTED. This used to assert that no such entry could exist, on the
    // reasoning that the backend only ever kept a bcrypt hash. Migration 00049
    // seals the PIN too, so the entry must exist — and it must be governed by
    // the row's own `pin_recoverable`, not by `has_pin`: a link can require a
    // PIN and still be unable to produce it (row 21), and that row must not be
    // offered a copy that would come back empty.
    const w = mountShares();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    expect(menuEntries().map((e) => e.label)).toContain(en.shares.copyPin);
    closeRowMenus();

    // ⚠ HIDDEN, not greyed, on the other two: a disabled entry invites a click
    // that can never work, and the badge beside it already says which case
    // this row is.
    for (const id of [1, 21]) {
      await openRowMenu(w, actionsOf(id));
      const entries = menuEntries();
      expect(entries.map((e) => e.label), `row ${id} offers a PIN it cannot produce`).not.toContain(
        en.shares.copyPin,
      );
      expect(entries.every((e) => !e.disabled), `row ${id} greys instead of hiding`).toBe(true);
      closeRowMenus();
    }

    // The half of the old assertion that still holds, kept and strengthened:
    // no PIN, and nothing derived from one, anywhere in the row.
    for (const html of [pinRow(w).html(), legacyRow(w).html()]) {
      expect(html).not.toContain('pin_hash');
      expect(html).not.toContain('pin_enc');
      expect(html).not.toContain(PLAIN_PIN);
    }
  });

  it('asks the audited endpoint once and hands the clipboard exactly what the server sent', async () => {
    const w = mountShares();
    await flushPromises();

    // Nothing has asked for a PIN before anybody picked the verb: the listing
    // does not carry it and the page does not prefetch it. Every read is an
    // audit row, so a read nobody asked for is a lie in the audit table.
    expect(vi.mocked(api.get).mock.calls.map((c) => c[0])).not.toContain('/shares/13/pin');

    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-copy-pin`);
    closeRowMenus();
    await flushPromises();

    const pinCalls = vi.mocked(api.get).mock.calls.filter((c) => c[0] === '/shares/13/pin');
    expect(pinCalls, 'one pick, one audited read').toHaveLength(1);
    expect(written).toEqual([PLAIN_PIN]);
    expect(useToastStore().toasts.map((t) => t.message)).toContain(en.shares.pinCopied);

    // ⚠⚠ The decisive one. A PIN that reached the clipboard must not have
    // reached the page on the way — not the row, not the menu, not the toast.
    expect(w.html()).not.toContain(PLAIN_PIN);
    expect(document.body.textContent ?? '').not.toContain(PLAIN_PIN);
  });

  it('says WHICH kind of refusal it got, copies nothing, and re-reads the row', async () => {
    // ⚠ Three refusals, three sentences, and only `no_secret_key` is something
    // anybody can act on. They are the same sentences the owner's own page
    // uses (`myShares.pinReason.*`) because the fact is about the LINK, not
    // about which page asked.
    for (const [reason, sentence] of [
      ['no_secret_key', en.myShares.pinReason.no_secret_key],
      ['not_recoverable', en.myShares.pinReason.not_recoverable],
      ['no_pin', en.myShares.pinReason.no_pin],
    ] as const) {
      setActivePinia(createPinia());
      written.length = 0;
      vi.mocked(api.get).mockClear();
      pinAnswers = { 13: { pin: null, reason } };
      const w = mountShares();
      await flushPromises();

      await openRowMenu(w, actionsOf(13));
      await pickMenuItem(`${actionsOf(13)}-copy-pin`);
      closeRowMenus();
      await flushPromises();

      expect(useToastStore().toasts.map((t) => t.message), reason).toContain(sentence);
      expect(written, 'nothing may reach the clipboard when there was no PIN').toEqual([]);
      // The row promised something the server would not give, so the page
      // re-reads the listing instead of leaving the offer standing.
      expect(
        vi.mocked(api.get).mock.calls.filter((c) => c[0] === '/admin/shares'),
        `${reason}: the stale row was left offering a PIN`,
      ).toHaveLength(2);
      w.unmount();
    }
  });

  it('a 403 says who may read a PIN, and copies nothing', async () => {
    // The server decides, not this page: an admin of ANOTHER tenant, or an app
    // token with no person behind it, gets 403 — whose body is the machine
    // word `forbidden`. Putting that on screen would be the page repeating a
    // code at somebody instead of telling them the rule.
    setActivePinia(createPinia());
    pinAnswers = { 13: Object.assign(new Error('Request failed'), { response: { status: 403 } }) };
    const w = mountShares();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-copy-pin`);
    closeRowMenus();
    await flushPromises();

    const toasts = useToastStore().toasts;
    expect(toasts.map((t) => t.level)).toContain('error');
    expect(toasts.map((t) => t.message)).toContain(en.shares.pinForbidden);
    expect(en.shares.pinForbidden.toLowerCase()).not.toContain('forbidden');
    expect(written).toEqual([]);
    expect(w.html()).not.toContain(PLAIN_PIN);
  });

  it('no longer offers a bare "copy token" — a token on its own opens nothing', async () => {
    const w = mountShares();
    await flushPromises();

    await openRowMenu(w, actionsOf(13));
    const names = [
      ...pinRow(w)
        .findAll('button')
        .map((b) => b.attributes('aria-label') ?? ''),
      ...menuEntries().map((e) => e.label),
    ];
    closeRowMenus();
    expect(names.some((n) => n.toLowerCase().includes('token'))).toBe(false);
    // The generic CopyButton (accessible name "Copy") is what carried both
    // token-cell buttons; neither may come back under that anonymous name.
    expect(names).not.toContain(en.common.copy);
    // The key behind it is gone from both locales too.
    expect('copyToken' in en.shares).toBe(false);
    expect('copyToken' in tr.shares).toBe(false);
  });

  it('gives every row action a name of its own, reachable from the keyboard', async () => {
    // ⚠ Rewritten when the verbs moved into the menu. The original counted
    // `aria-label`s on the row's buttons, which was the only way an icon-only
    // button could have a name at all; a menu entry's name is its visible
    // label, so this now measures the thing a person actually reads — and it
    // still refuses a nameless or duplicated action.
    const w = mountShares();
    await flushPromises();

    // The handle itself is a real, named, focusable button.
    const control = pinRow(w).get(`[data-testid="${actionsOf(13)}"]`);
    expect(control.element.tagName).toBe('BUTTON');
    expect(control.text().trim().length).toBeGreaterThan(0);
    expect(control.attributes('disabled')).toBeUndefined();

    await openRowMenu(w, actionsOf(13));
    const entries = menuEntries();
    const names = entries.map((e) => e.label);
    expect(names.every((n) => n.trim().length > 0)).toBe(true);
    expect(new Set(names).size).toBe(names.length);
    expect(names).toContain(en.shares.copyLink);
    expect(names).toContain(en.shares.copyPin);
    expect(names).toContain(en.shares.revoke);
    expect(names).toContain(en.common.delete);
    // ⚠ "Copy PIN" says what it copies. The anonymous "Copy" that carried the
    // old token-cell buttons is exactly the name this assertion refuses.
    expect(names).not.toContain(en.common.copy);
    // Nothing is offered and then refused.
    expect(entries.every((e) => !e.disabled)).toBe(true);
    // Both destructive verbs stay visually apart inside the menu.
    expect(entries.find((e) => e.label === en.shares.revoke)?.danger).toBe(true);
    expect(entries.find((e) => e.label === en.common.delete)?.danger).toBe(true);
    // Real <button>s, so Tab reaches them and Enter/Space fires them.
    expect(
      Array.from(document.querySelectorAll('.fe-ctx .fe-ctx__item')).every(
        (el) => el.tagName === 'BUTTON',
      ),
    ).toBe(true);
    closeRowMenus();
  });

  it('does not offer to revoke a link that is already revoked', async () => {
    // The verb goes rather than greys: the row already SAYS "revoked" in its
    // token cell, so a greyed-out "Revoke" beside it would be noise. Delete
    // and copy stay — a revoked link is still a row you may want gone.
    const w = mountShares();
    await flushPromises();

    await openRowMenu(w, actionsOf(7));
    const names = menuEntries().map((e) => e.label);
    expect(names).not.toContain(en.shares.revoke);
    expect(names).toContain(en.shares.copyLink);
    expect(names).toContain(en.common.delete);
    closeRowMenus();

    // ...and it IS offered on a live one, or the assertion above would pass
    // for the wrong reason.
    await openRowMenu(w, actionsOf(13));
    expect(menuEntries().map((e) => e.label)).toContain(en.shares.revoke);
    closeRowMenus();
  });

  it('a revoked link says REVOKED, in the reader’s language — not a past expiry', async () => {
    // ⚠ QA, 2026-09-21: the badge was a hard-coded English `revoked` that
    // never drew (no server sent `revoked_at`), and the expiry column said
    // "expires 21 Sep 2026 15:55 · 9 seconds ago" for a link revoked then.
    const w = mountShares('tr');
    await flushPromises();
    const row = w.get('[data-testid="share-row-7"]');
    expect(row.get('[data-testid="share-revoked-on"]').text()).toContain('İptal edildi');
    expect(row.text()).not.toMatch(/\brevoked\b/);
    // Said once — not a badge AND a column saying the same word.
    expect(row.text().split('İptal edildi')).toHaveLength(2);
    // A live row does not say it.
    expect(pinRow(w).find('[data-testid="share-revoked-on"]').exists()).toBe(false);
  });

  it('the revoke dialog asks: the title is the question, the buttons are the two answers', async () => {
    const w = mountShares('tr');
    await flushPromises();
    await openRowMenu(w, actionsOf(13));
    await pickMenuItem(`${actionsOf(13)}-revoke`);
    closeRowMenus();
    await flushPromises();
    const body = document.body.textContent ?? '';
    expect(body).toContain('Bu paylaşım iptal edilsin mi?');
    // ⚠ The success toast is NOT the title any more — it announced as done
    // what the dialog was asking permission for.
    expect(body).not.toContain(tr.shares.revokedOk);
    expect(lastOf('share-revoke-dismiss')?.textContent?.trim()).toBe('Vazgeç');
    expect(lastOf('share-revoke-confirm')?.textContent?.trim()).toBe(
      'Paylaşımı iptal et',
    );
    expect(vi.mocked(api.post)).not.toHaveBeenCalled();
    (lastOf('share-revoke-confirm') as HTMLElement).click();
    await flushPromises();
    expect(vi.mocked(api.post)).toHaveBeenCalledWith('/admin/shares/13/revoke');
    w.unmount();
  });

  it('says the same things in Turkish, with Turkish letters', async () => {
    const w = mountShares('tr');
    await flushPromises();

    expect(pinRow(w).get('[data-testid="share-pin"]').text()).toContain(tr.shares.pinProtected);
    // The control says "Aksiyon" in Turkish, and its menu says the verbs.
    expect(pinRow(w).get(`[data-testid="${actionsOf(13)}"]`).text()).toContain('Aksiyon');
    await openRowMenu(w, actionsOf(13));
    const trNames = menuEntries().map((e) => e.label);
    expect(trNames).toContain(tr.shares.copyLink);
    expect(trNames).toContain(tr.shares.copyPin);
    closeRowMenus();
    expect(tr.shares.pinProtected).toBe('PIN gerekli');
    // The recoverable row says the new sentence in Turkish too, and says it
    // where a person reads it — the badge's own label.
    expect(pinRow(w).get('[data-testid="share-pin"]').attributes('aria-label')).toBe(
      tr.shares.pinHintRecoverable,
    );
    // ASCII-fied Turkish is a defect of its own (CLAUDE.md), so pin it here.
    expect(tr.shares.pinHint).toContain('Ziyaretçinin');
    expect(tr.shares.pinHint).toContain('saklandığı');
    expect(tr.shares.pinHintApp).toContain('oluştururken');
    expect(tr.shares.pinHintRecoverable).toContain('Ziyaretçinin');
    expect(tr.shares.pinHintRecoverable).toContain('kopyalayabilirsiniz');
    expect(tr.shares.pinHintRecoverable).toContain('denetim kaydına');
    expect(tr.shares.copyPin).toBe("PIN'i kopyala");
    expect(tr.shares.pinCopied).toContain('panoya kopyalandı');
    expect(tr.shares.pinForbidden).toContain('yönetici');
    expect(tr.shares.revoke).toBe('İptal et');
    expect(tr.shares.pinHint).not.toMatch(/Ziyaretcinin|saklandigi|Iptal et/);
    expect(tr.shares.pinHintRecoverable).not.toMatch(/Ziyaretcinin|kaydina/);
    expect(tr.shares.pinForbidden).not.toMatch(/yonetici|baglanti/);
  });
});
