// uyan:s1 — the panel learns that an app wakes up on its own.
//
// An app with the `schedule` permission is woken once an hour and asks the
// host for work of its own: it moves files, signs documents and mints links
// while nobody is watching. The backend has carried all of it for a while
// (`scheduled` on every list row, a `schedule` array on the detail); the
// panel showed NONE of it, so from the admin screen a self-acting app and a
// menu-only app looked exactly alike, and the only way to find out what the
// last hour decided was the server's log.
//
// What has to stay true:
//
//   · the LIST says which apps wake up — a badge in a cell of its own, never
//     in the row's one pinned Actions menu (a badge is not a verb) and never
//     folded into `state`, which a scheduled app shares with every other
//     running app;
//   · an app nobody wakes gets no badge at all, not an em dash;
//   · the DRAWER shows the Schedule section only for an app that is woken —
//     a heading over "never" is a promise the deployment is not making;
//   · for one that is, the section says when the NEXT wake-up is and what the
//     LAST one decided (the app's own note plus the host's tally), and lists
//     the work that wake-up asked for;
//   · a refused or failed item says so, in the reader's language, with the
//     server's reason beside it;
//   · a queued item's `job_id` is a way OUT — a link to the ops queue, not a
//     string to copy;
//   · and both surfaces speak English and Turkish.
//
// ⚠ The wake-up row is `key === ''` and its `error` field is NOT an error: it
// is the decision line ("3 scheduled, 1 refused (unknown action)"). Reading
// it as a failure would paint a healthy app red every hour.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

/** The detail answer, swapped per test. */
const fx = vi.hoisted(() => ({
  schedule: [] as Record<string, unknown>[],
  scheduled: true,
  /** ⚠ `true` = the shape the server actually sends: the app's own fields
   *  nested under `plugin`. See the last describe in this file. */
  envelope: false,
}));

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/logs')) return { data: { lines: [], next: 0 } };
      if (url.endsWith('/locks')) return { data: { locks: [] } };
      if (url === '/admin/app-plugins') return { data: { runtime, plugins: rows } };
      if (/\/admin\/app-plugins\/\d+$/.test(url)) {
        const body: Record<string, unknown> = {
          manifest: { manifest_version: 1, name: 'sweep', version: '1.0.0', label: { en: 'Sweep' }, permissions: [], actions: [], views: [], settings: [] },
          granted: ['files:write', 'schedule'],
          overrides: [],
          settings: {},
          schedule: fx.schedule,
        };
        if (fx.envelope) body.plugin = app({ id: 1, name: 'sweep', scheduled: true });
        return { data: body };
      }
      return { data: {} };
    }),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

import AppPluginsTab from '@/components/plugins/AppPluginsTab.vue';
import AppPluginDetail from '@/components/plugins/AppPluginDetail.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

const runtime = { enabled: true, arch_ok: true, disabled_reason: '', requires_signature: false, engines: {} };

function app(extra: Record<string, unknown>) {
  return {
    id: 1,
    name: 'sweep',
    version: '1.0.0',
    label: { en: 'Sweep', tr: 'Süpür' },
    enabled: true,
    state: 'running',
    source: 'github',
    sha256: 'a9950e0d84a62a1b30c952736b097e5f3cde019564724dc3c0432a36f7c0d091',
    signed: true,
    permissions: ['files:write'],
    scheduled: false,
    actions: 1,
    views: 0,
    public_pages: 0,
    created_at: '2026-09-01T09:00:00Z',
    updated_at: '2026-09-01T09:00:00Z',
    ...extra,
  };
}

/** One app that wakes up, one that does not — the comparison is the point. */
const rows = [
  app({ id: 1, name: 'sweep', scheduled: true }),
  app({ id: 2, name: 'shrink', label: { en: 'Shrink' }, scheduled: false }),
];

/** The heartbeat row: `key === ''`, `due_at` = the NEXT wake-up. */
const WAKEUP = {
  plugin_id: 1,
  key: '',
  due_at: '2026-09-20T18:00:00Z',
  status: 'due',
  attempts: 41,
  claimed_by: 'filex-a1b2',
  error: 'swept 12 folders — 2 scheduled, 1 beyond this window, 1 refused (unknown action)',
  created_at: '2026-09-01T09:00:00Z',
  updated_at: '2026-09-20T17:00:00Z',
};

/** Two pieces of work the last wake-up asked for: one queued, one failed. */
const WORK = [
  {
    plugin_id: 1,
    key: 'nightly-sweep',
    due_at: '2026-09-20T17:30:00Z',
    action_id: 'sweep.run',
    status: 'queued',
    attempts: 1,
    job_id: '0f3c9a21-77de-4b5e-9a10-2c1d55ee0011',
    created_at: '2026-09-20T17:00:00Z',
    updated_at: '2026-09-20T17:00:00Z',
  },
  {
    plugin_id: 1,
    key: 'archive-old',
    due_at: '2026-09-20T17:45:00Z',
    action_id: 'sweep.archive',
    status: 'failed',
    attempts: 3,
    error: 'storage 4 is read-only',
    created_at: '2026-09-20T17:00:00Z',
    updated_at: '2026-09-20T17:05:00Z',
  },
];

function i18nFor(locale: string) {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

/** ⚠ A real router: the job id is a RouterLink and a stub would not prove it
 *  resolves to a route that exists. `queue` is the ops page the id leads to. */
function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div />' } },
      { path: '/queue', name: 'queue', component: { template: '<div />' } },
    ],
  });
}

async function mountTab(locale = 'en') {
  const w = mount(AppPluginsTab, { global: { plugins: [i18nFor(locale)] } });
  await flushPromises();
  return w;
}

async function openDetail(plugin: Record<string, unknown>, locale = 'en') {
  const r = router();
  r.push('/');
  await r.isReady();
  const w = mount(AppPluginDetail, {
    props: { plugin },
    global: { plugins: [i18nFor(locale), r] },
  });
  await flushPromises();
  return w;
}

describe('Apps list — a badge for an app that wakes itself', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fx.schedule = [];
    fx.envelope = false;
  });

  it('badges the scheduled app and leaves the other row alone', async () => {
    const w = await mountTab();
    expect(w.find('[data-testid="app-plugin-scheduled-sweep"]').text()).toBe(
      en.appPlugins.scheduledBadge,
    );
    // ⚠ Nothing at all on the app nobody wakes — not an em dash, not an empty
    // badge: one column of noise per row for a fact that is only true once.
    expect(w.find('[data-testid="app-plugin-scheduled-shrink"]').exists()).toBe(false);
  });

  it('says it in Turkish too', async () => {
    const w = await mountTab('tr');
    expect(w.find('[data-testid="app-plugin-scheduled-sweep"]').text()).toBe(
      tr.appPlugins.scheduledBadge,
    );
    expect(tr.appPlugins.scheduledBadge).toBe('Saatte bir uyanır');
  });

  it('is a cell of its own — not a row in the pinned Actions menu', async () => {
    const w = await mountTab();
    const badge = w.find('[data-testid="app-plugin-scheduled-sweep"]');
    expect(badge.exists()).toBe(true);
    // A badge is not a verb. `.tbl-rowactions` is the one pinned control per
    // row; the badge must live outside it, in the cell the header names.
    expect(badge.element.closest('.tbl-rowactions')).toBeNull();
    expect(w.text()).toContain(en.appPlugins.fields.schedule);
  });
});

describe('App detail — the Schedule section', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fx.schedule = [];
    fx.envelope = false;
  });

  it('is absent for an app nobody wakes', async () => {
    const w = await openDetail(app({ id: 2, name: 'shrink', scheduled: false }));
    expect(w.find('[data-testid="app-plugin-schedule"]').exists()).toBe(false);
    // The rest of the drawer is untouched — the gate is one section.
    expect(w.find('[data-testid="app-plugin-locks"]').exists()).toBe(true);
  });

  it('says when the next wake-up is and what the last one decided', async () => {
    fx.schedule = [WAKEUP, ...WORK];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    const section = w.find('[data-testid="app-plugin-schedule"]');
    expect(section.exists()).toBe(true);

    const line = w.find('[data-testid="app-plugin-schedule-wakeup"]').text();
    // The next wake-up, on the reader's clock — not the raw RFC 3339 string.
    expect(line).toContain('2026');
    expect(line).not.toContain('2026-09-20T18:00:00Z');
    // ⚠ The decision, verbatim. It arrives in `error` and is NOT a failure:
    // it is the app's own note plus the host's tally, and a healthy hour
    // reads exactly like this.
    expect(w.find('[data-testid="app-plugin-schedule-decision"]').text()).toContain(
      '2 scheduled, 1 beyond this window, 1 refused (unknown action)',
    );
    // And the two facts that diagnose a schedule that has quietly stopped:
    // how many times it has been woken, and which node took it.
    expect(line).toContain('41');
    expect(line).toContain('filex-a1b2');
  });

  it('lists the work that wake-up asked for, and never the wake-up itself', async () => {
    fx.schedule = [WAKEUP, ...WORK];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    const section = w.find('[data-testid="app-plugin-schedule"]');
    expect(section.text()).toContain('sweep.run');
    expect(section.text()).toContain('sweep.archive');
    // The heartbeat is the LINE above, not a row in the table: it has no
    // action and no due work, and listing it would read as a third job.
    expect(w.find('[data-testid="schedule-status-"]').exists()).toBe(false);
    expect(w.find('[data-testid="schedule-status-nightly-sweep"]').text()).toBe(
      en.appPlugins.detail.schedule.statuses.queued,
    );
  });

  it('a failed item says so, with the server’s reason beside it', async () => {
    fx.schedule = [WAKEUP, ...WORK];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    expect(w.find('[data-testid="schedule-status-archive-old"]').text()).toBe(
      en.appPlugins.detail.schedule.statuses.failed,
    );
    expect(w.find('[data-testid="schedule-error-archive-old"]').text()).toBe(
      'storage 4 is read-only',
    );
    // ⚠ And the queued one is NOT painted as a failure: it carries no error.
    expect(w.find('[data-testid="schedule-error-nightly-sweep"]').exists()).toBe(false);
  });

  it('a queued item’s job id is a way out to the ops queue', async () => {
    fx.schedule = [WAKEUP, ...WORK];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    const link = w.find('[data-testid="schedule-job-nightly-sweep"]');
    expect(link.exists()).toBe(true);
    // Resolved by the real router — a name nobody routes would render '/'.
    expect(link.attributes('href')).toBe('/queue');
    // Shortened on screen, whole in the title: the column is 160px and the
    // id is a UUID.
    expect(link.text()).toBe('0f3c9a21…');
    expect(link.attributes('title')).toContain('0f3c9a21-77de-4b5e-9a10-2c1d55ee0011');
    // An item that has not reached the queue says so instead of showing a
    // link to nothing.
    expect(w.find('[data-testid="schedule-job-archive-old"]').exists()).toBe(false);
    expect(w.find('[data-testid="app-plugin-schedule"]').text()).toContain(
      en.appPlugins.detail.schedule.notQueued,
    );
  });

  it('an hour that asked for nothing says so rather than showing an empty box', async () => {
    fx.schedule = [WAKEUP];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    expect(w.find('[data-testid="app-plugin-schedule"]').text()).toContain(
      en.appPlugins.detail.schedule.empty,
    );
  });

  it('an app that has never been woken says that, not a blank decision', async () => {
    fx.schedule = [{ ...WAKEUP, attempts: 0, claimed_by: '', error: '' }];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    const line = w.find('[data-testid="app-plugin-schedule-wakeup"]').text();
    expect(line).toContain(en.appPlugins.detail.schedule.neverWoken);
    expect(w.find('[data-testid="app-plugin-schedule-decision"]').exists()).toBe(false);
  });

  it('speaks Turkish, statuses included', async () => {
    fx.schedule = [WAKEUP, ...WORK];
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }), 'tr');
    const section = w.find('[data-testid="app-plugin-schedule"]');
    expect(section.text()).toContain(tr.appPlugins.detail.schedule.title);
    expect(w.find('[data-testid="schedule-status-archive-old"]').text()).toBe('Başarısız');
    expect(w.find('[data-testid="schedule-status-nightly-sweep"]').text()).toBe(
      'Kuyruğa verildi',
    );
  });
});

/**
 * ⚠⚠ Not the schedule — a defect found while building it, fixed in the same
 * pass because the screen it breaks is the one the Schedule section was added
 * to.
 *
 * `GET /api/admin/app-plugins/{id}` answers an ENVELOPE: the app's own fields
 * sit under `plugin`, everything else is top level. `AppPluginsApi.get`
 * handed that straight back as an `AppPluginDetail`, which is declared as
 * though the whole of it were flat — so every `detail.<app field>` was
 * `undefined` and TypeScript never blinked. Measured in a browser against a
 * real instance: the drawer's facts list drew Name, Version and Source blank,
 * the dates as `—`, and "Unsigned" for a SIGNED app. A screen that omits a
 * fact is incomplete; one that states the opposite is wrong.
 *
 * ⚠ Both shapes are covered on purpose. The flat one is what every fixture in
 * this repo was written against (`appPluginSettingsFields.test.ts` included),
 * so the flattening must not break it — which is also what makes it safe if
 * the server ever stops nesting.
 */
describe('App detail — the endpoint’s envelope is unwrapped', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fx.schedule = [];
    fx.envelope = false;
  });

  it('reads the app’s own facts out of the nested `plugin` key', async () => {
    fx.envelope = true;
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    const facts = w.find('[data-testid="app-plugin-detail"]').find('dl').text();
    expect(facts).toContain('sweep');
    expect(facts).toContain('1.0.0');
    // ⚠ The lie: this app is signed, and the drawer used to say it was not.
    expect(facts).toContain(en.appPlugins.detail.signed);
    expect(facts).not.toContain(en.appPlugins.detail.unsigned);
    expect(facts).toContain('a9950e0d');
  });

  it('still reads a flat answer, so every fixture written against one holds', async () => {
    fx.envelope = false;
    const w = await openDetail(app({ id: 1, name: 'sweep', scheduled: true }));
    // Nothing nested and nothing flat to fall back on → the facts are empty
    // rather than wrong, and the drawer still renders its other sections.
    expect(w.find('[data-testid="app-plugin-detail"]').exists()).toBe(true);
    expect(w.find('[data-testid="app-plugin-locks"]').exists()).toBe(true);
  });
});
