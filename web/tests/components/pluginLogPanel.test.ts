// A plugin's log, one panel for both kinds of plugin (issue #104).
//
// The storage plugins got a log when the sync started writing to it, and the
// app plugins' panel became the shared PluginLogPanel rather than gaining a
// copy. The server counts a line repeated within the last 50 instead of
// writing it again (backend internal/pluginlog): the poll returns the SAME
// line - same `id`, higher `count`, `last` - and the panel must REPLACE it, or
// the repeat the server took care not to write shows up twice anyway.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { formatDate, formatDateFull } from '@/lib/format';
import PluginLogPanel from '@/components/plugins/PluginLogPanel.vue';
import type { PluginLogPage } from '@/api/plugins';

function i18nFor(locale: string) {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

const FIRST = '2026-10-01T09:00:00Z';
const LAST = '2026-10-01T09:05:00Z';

describe('PluginLogPanel', () => {
  it('replaces a repeated line by its id and says how often, with the latest time', async () => {
    vi.useFakeTimers();
    const pages: PluginLogPage[] = [
      {
        lines: [
          { id: 1, seq: 1, ts: FIRST, count: 1, level: 'info', msg: 'plugin up' },
          { id: 2, seq: 2, ts: FIRST, count: 1, level: 'warn', msg: 'storage arsiv: /Proje: no answer' },
        ],
        next: 2,
      },
      // The same line again, counted: same id, a later seq.
      { lines: [{ id: 2, seq: 5, ts: FIRST, last: LAST, count: 4, level: 'warn', msg: 'storage arsiv: /Proje: no answer' }], next: 5 },
    ];
    const asked: number[] = [];
    const fetch = vi.fn(async (after: number) => {
      asked.push(after);
      return pages.shift() ?? { lines: [], next: after };
    });
    const w = mount(PluginLogPanel, {
      props: { fetch, active: true, testid: 'storage-plugin-logs' },
      global: { plugins: [i18nFor('en')] },
    });
    await flushPromises();
    await vi.advanceTimersByTimeAsync(2000);
    await flushPromises();

    expect(asked.slice(0, 2)).toEqual([0, 2]);
    const lines = w.findAll('[data-testid="plugin-log-line"]');
    expect(lines.length, 'a repeated line is drawn twice').toBe(2);
    const repeated = lines[1];
    expect(repeated.text()).toContain('storage arsiv: /Proje: no answer');
    expect(repeated.find('[data-testid="plugin-log-repeats"]').text()).toBe('×4');
    expect(repeated.find('[data-testid="plugin-log-repeats"]').attributes('title')).toBe(
      `First at ${formatDateFull(FIRST, 'en')}`,
    );
    // The time on the line is the latest repeat's.
    expect(repeated.text()).toContain(formatDate(LAST, 'en'));
    // A line written once says no count.
    expect(lines[0].find('[data-testid="plugin-log-repeats"]').exists()).toBe(false);
    expect(w.find('[data-testid="storage-plugin-logs"]').exists()).toBe(true);
    w.unmount();
    vi.useRealTimers();
  });

  it('says so when there is nothing, in the reader’s language', async () => {
    const w = mount(PluginLogPanel, {
      props: { fetch: async (after: number) => ({ lines: [], next: after }), active: true },
      global: { plugins: [i18nFor('tr')] },
    });
    await flushPromises();
    expect(w.text()).toContain(tr.pluginLog.empty);
    expect(w.text()).toContain(tr.pluginLog.title);
    w.unmount();
  });

  it('does not poll while it is not shown', async () => {
    const fetch = vi.fn(async (after: number) => ({ lines: [], next: after }));
    const w = mount(PluginLogPanel, { props: { fetch, active: false }, global: { plugins: [i18nFor('en')] } });
    await flushPromises();
    expect(fetch).not.toHaveBeenCalled();
    w.unmount();
  });
});
