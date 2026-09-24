// The admin Notifications page: the explorer's table (feat/043-tables) with
// the columns saying what the release-candidate sweep made them say
// (feat/043-internal).
//
// ⚠ Merge seam. Internal changed what the cells SAY — the kind of event in
// the reader's language instead of the raw id, a person's name instead of
// `user #2` — and tables replaced the table they sit in with DataTable. The
// two met in one file, and either half could have been dropped without a
// conflict: the merged page must be DataTable AND say the words, and a
// sortable column must order by the words it shows.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
// ⚠ The PACKAGE, as the page imports it: `foreignText` asks the registry in
// `@brftech/filex-core` whether a language is right to left, so the Arabic
// row below has to be registered in that same copy, not in `/src/`.
import { DataTable, registerLocale, resetLocales } from '@brftech/filex-core';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const items = [
  {
    id: 11,
    event: 'share.created',
    severity: 'info',
    title: 'share.created',
    body: '',
    user_id: 2,
    user_name: 'Ayşe Yılmaz',
    webhook_status: 'skipped',
    created_at: '2026-09-21T10:00:00Z',
  },
  {
    id: 12,
    event: 'file.uploaded',
    severity: 'info',
    title: 'file.uploaded',
    body: '',
    user_id: null,
    webhook_status: 'sent',
    created_at: '2026-09-21T11:00:00Z',
  },
];

/** What the list answers with — the rows above unless a test says otherwise. */
let rows: Array<Record<string, unknown>> = items;

vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    adminList: vi.fn(async () => ({ items: rows, total: rows.length })),
    getSettings: vi.fn(async () => ({ in_app_enabled: true, muted_events: [] })),
  },
}));

import Notifications from '@/views/Notifications.vue';

function mountPage(locale = 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(Notifications, {
    global: { plugins: [i18n], stubs: { RouterLink: RouterLinkStub } },
    attachTo: document.body,
  });
}

describe('admin Notifications — the shared table, saying the words', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('is DataTable under its own table id', async () => {
    const w = mountPage();
    await flushPromises();
    const table = w.findComponent(DataTable);
    expect(table.exists()).toBe(true);
    expect(table.props('tableId')).toBe('admin.notifications');
    w.unmount();
  });

  it('names the event and the person in the reader’s language', async () => {
    const w = mountPage();
    await flushPromises();
    const text = w.text();
    expect(text).toContain(tr.notifications.kinds.share_created);
    expect(text).toContain(tr.notifications.kinds.file_uploaded);
    expect(text).not.toContain('share.created');
    expect(text).toContain('Ayşe Yılmaz');
    expect(text).toContain(tr.notifications.scopeEveryone);
    w.unmount();
  });

  it('sorts the event and scope columns by the words they show', async () => {
    const w = mountPage();
    await flushPromises();
    const cols = w.findComponent(DataTable).props('columns') as Array<{
      id: string;
      sortValue?: (r: unknown) => unknown;
    }>;
    const byId = Object.fromEntries(cols.map((c) => [c.id, c]));
    const row = { ...items[0], text: { title: '', body: '' } };
    expect(byId.event.sortValue?.(row)).toBe(tr.notifications.kinds.share_created);
    expect(byId.scope.sortValue?.(row)).toBe('Ayşe Yılmaz');
    expect(byId.scope.sortValue?.({ ...row, user_id: null })).toBe(tr.notifications.scopeEveryone);
    w.unmount();
  });

  // ⚠ PR #42 (Berk Başarır) + v0.43.0: who a broadcast reaches is the bells'
  // own rule, said by the server (`audience`). "Everyone" beside a drop notice
  // (administrators only), an antivirus hit (those who can see the file) or a
  // legacy upload row (no bell at all) would be a lie on the audit page.
  it('says who a broadcast reaches, by the server’s audience', async () => {
    const w = mountPage();
    await flushPromises();
    const cols = w.findComponent(DataTable).props('columns') as Array<{
      id: string;
      sortValue?: (r: unknown) => unknown;
    }>;
    const scope = Object.fromEntries(cols.map((c) => [c.id, c])).scope;
    const row = { ...items[1], text: { title: '', body: '' } };
    expect(scope.sortValue?.({ ...row, audience: 'everyone' })).toBe(tr.notifications.scopeEveryone);
    expect(scope.sortValue?.({ ...row, audience: 'viewers' })).toBe(tr.notifications.scopeViewers);
    expect(scope.sortValue?.({ ...row, audience: 'admins', admins_only: true })).toBe(tr.notifications.scopeAdmins);
    expect(scope.sortValue?.({ ...row, audience: 'nobody' })).toBe(tr.notifications.scopeNobody);
    // A server from before `audience` still said admins_only.
    expect(scope.sortValue?.({ ...row, admins_only: true })).toBe(tr.notifications.scopeAdmins);
    w.unmount();
  });
});

/* ── two defects the pack agent's last look found on this page (v0.43.0) ── */

const LRI = String.fromCharCode(0x2066);
const PDI = String.fromCharCode(0x2069);

/** A row with a failed delivery and a file at the ROOT of a storage. */
const ROOT_FILE_FAILED = {
  id: 21,
  event: 'file.uploaded',
  severity: 'info',
  title: 'file.uploaded',
  body: '',
  user_id: null,
  meta: { node: { path: '/informe.pdf', name: 'informe.pdf' } },
  webhook_status: 'failed',
  webhook_error: 'Post "https://hooks.example.test/filex": dial tcp 10.0.0.1:443: connect: connection refused',
  created_at: '2026-09-24T09:00:00Z',
};

/**
 * ⚠ Every page these tests open is torn down in `afterEach`, and every query
 * is scoped to THAT page. A test that fails before its own `unmount()` would
 * otherwise leave its table in the document, and the next test's
 * `document.querySelector` reads the previous page — measured while
 * break-testing this file: one mutant turned five tests red, three of them
 * reading an Arabic table left behind by the first (lesson #387).
 */
let open: ReturnType<typeof mountPage> | null = null;
function openPage(locale = 'tr') {
  open = mountPage(locale);
  return open;
}
afterEach(() => {
  open?.unmount();
  open = null;
  rows = items;
});

describe('admin Notifications — the Webhook cell is ONE box', () => {
  beforeEach(() => setActivePinia(createPinia()));

  /* ⚠⚠ A DataTable cell is a flex ROW whose children may shrink below their
     content. The badge and the reason were two of them: when the reason
     wrapped, the badge was squeezed narrower than its label and the label
     spilled under the reason (es / ar, and English in a narrow column).
     jsdom has no layout, so the overlap itself is measured in a browser
     (e2e/tests/133); what is held here is the SHAPE that makes it
     impossible — one element per cell (lesson #373). */
  it('every Webhook cell holds exactly one element, with the badge and the reason inside it', async () => {
    rows = [...items, ROOT_FILE_FAILED];
    const w = openPage();
    await flushPromises();
    const cells = [...w.element.querySelectorAll('.fe-list__row .fe-list__cell[data-col="webhook"]')];
    expect(cells.length, 'the scan found the column').toBe(rows.length);
    for (const c of cells) {
      expect(c.children.length, c.innerHTML.slice(0, 200)).toBe(1);
      expect(c.children[0].getAttribute('data-testid')).toBe('notif-webhook');
    }
    // …and the reason is IN it, not beside it.
    const failed = cells.find((c) =>
      c.querySelector('[data-testid="notif-webhook-reason"]')?.textContent?.includes('dial tcp'),
    );
    expect(failed, 'the failed row says why').toBeTruthy();
  });
});

describe('admin Notifications — machine text in a right-to-left panel', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    // The SERVER is the one that says Arabic is right to left; here the
    // offered-language list is what it would have sent.
    registerLocale({ code: 'ar', label: 'العربية', source: 'plugin', plugin: 'lang-ar', rtl: true, strings: {} });
  });
  afterEach(() => resetLocales());

  it('isolates a ROOT file’s path in the body — `/informe.pdf` must not read `informe.pdf/`', async () => {
    /* The body goes through `foreignText` already (useNotificationText); what
       missed it was the SHARED rule, which wanted a path of two segments. A
       file at the root of a storage has one. */
    rows = [ROOT_FILE_FAILED];
    const w = openPage('ar');
    await flushPromises();
    const body = w.element.querySelector('.fe-list__row .fe-list__cell[data-col="body"]')?.textContent ?? '';
    expect(body, JSON.stringify(body)).toContain(`${LRI}/informe.pdf${PDI}`);
  });

  it('isolates the RECEIVER’s error in the Webhook cell — the one text cell that was never routed', async () => {
    rows = [ROOT_FILE_FAILED];
    const w = openPage('ar');
    await flushPromises();
    const reason = w.element.querySelector('[data-testid="notif-webhook-reason"]')?.textContent ?? '';
    expect(reason, JSON.stringify(reason)).toContain(LRI);
    expect(reason).toContain(PDI);
  });

  it('leaves a left-to-right panel byte for byte as it was', async () => {
    rows = [ROOT_FILE_FAILED];
    const w = openPage('tr');
    await flushPromises();
    const row = w.element.querySelector('.fe-list__row')?.textContent ?? '';
    expect(row).not.toContain(LRI);
    expect(row).toContain('/informe.pdf');
  });
});
