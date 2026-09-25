// The admin top bar's storage chip (QuotaWidget) — the same line as the
// explorer panel's, by the same rule (`lib/storageLine`, tested in
// tests/lib/storageLine.test.ts).
//
// It printed the person's upload counter for everybody: "● 523.5 MB" in the
// top bar of a dashboard whose own card, a hand's width below, said the drive
// holds 245.3 GB (production, 2026-09-25). Without a quota that counter
// answers nothing anybody asked; with one, it is exactly what matters.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

const me = vi.fn();
const storages = vi.fn();
vi.mock('@/api/quota', () => ({
  quotaApi: {
    me: (...a: unknown[]) => me(...a),
    storages: (...a: unknown[]) => storages(...a),
  },
}));

import QuotaWidget from '@/components/QuotaWidget.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const live: VueWrapper[] = [];

/** ⚠ Unmounted after every test: the widget polls on a 60 s interval. */
async function chip(locale = 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(QuotaWidget, { global: { plugins: [i18n] } });
  live.push(w);
  await flushPromises();
  return w;
}

const figure = (w: VueWrapper) => w.find('[data-testid="quota-widget-figure"]').text();
const tooltip = (w: VueWrapper) => w.find('button').attributes('title');

const UPLOADER = { used_bytes: 523_457_650, quota_bytes: 0, percent_used: 0, unlimited: true };
const DRIVE = { name: 'Diyetlif-Bulut-Depolama', used_bytes: 245_276_276_422, file_count: 153_943 };

describe('QuotaWidget', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    me.mockReset();
    storages.mockReset();
  });
  afterEach(() => {
    while (live.length) live.pop()!.unmount();
  });

  it('without a quota, shows how full the drives are — not the upload counter', async () => {
    me.mockResolvedValue(UPLOADER);
    storages.mockResolvedValue([DRIVE]);
    const w = await chip();
    expect(figure(w)).toBe('245,3 GB');
    expect(tooltip(w)).toBe('245,3 GB kullanıldı · sınırsız');
    const w2 = await chip('en');
    expect(figure(w2)).toBe('245.3 GB');
    expect(tooltip(w2)).toBe('245.3 GB used · unlimited');
  });

  it('says a lower bound as one', async () => {
    me.mockResolvedValue(UPLOADER);
    storages.mockResolvedValue([{ ...DRIVE, coverage: { complete: false, reason: 'lazy_filling' } }]);
    const w = await chip();
    expect(figure(w)).toBe('≥ 245,3 GB');
    expect(tooltip(w)).toBe('en az 245,3 GB kullanıldı · sınırsız');
  });

  it('with a quota, shows the person against it and asks nothing about the drives', async () => {
    me.mockResolvedValue({ used_bytes: 2_500_000_000, quota_bytes: 10_000_000_000, percent_used: 25, unlimited: false });
    const w = await chip();
    expect(figure(w)).toBe('2,5 GB / 10 GB');
    expect(storages).not.toHaveBeenCalled();
  });

  it('without a quota and without the drives’ figure, stays out of the bar', async () => {
    me.mockResolvedValue(UPLOADER);
    storages.mockRejectedValue(new Error('404'));
    const w = await chip();
    expect(w.find('button').exists()).toBe(false);
  });
});
