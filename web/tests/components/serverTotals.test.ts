// The admin pages print the SERVER's numbers (0.54 audit, D8 and D9).
//
// D8: the duplicate-file report's cards ("groups", "extra copies", "wasted
//     space") summed the 100 groups the page was sent, while the server had
//     counted every group and then cut the list - an install with more than
//     100 groups read a total that was only the top of it. The server now
//     sends total_groups / total_copies / total_waste (handlers/duplicates.go).
// D9: the dashboard added the storage rows up again and found the newest scan
//     by sorting timestamps as text; the usage page re-implemented
//     usage.SumBuckets without its source rule. The server sends the totals,
//     the bucket table and the trend (usage.PerBucket, usage.Trend).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import { formatBytes, formatNumber } from '@/lib/format';

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/duplicates') {
        return {
          data: {
            // ONE group on the page; the report has 250.
            groups: [
              {
                key: '100-aaa',
                size: 100,
                count: 2,
                total_waste: 100,
                nodes: [
                  { id: 1, storage_id: 1, path: '/a.txt', name: 'a.txt', size: 100, etag: 'aaa' },
                  { id: 2, storage_id: 1, path: '/b.txt', name: 'b.txt', size: 100, etag: 'aaa' },
                ],
              },
            ],
            total_groups: 250,
            total_copies: 412,
            total_waste: 987_654_321,
            truncated: true,
          },
        };
      }
      if (url === '/admin/storages') return { data: [] };
      throw new Error('unexpected GET ' + url);
    }),
  },
}));

import Duplicates from '@/views/Duplicates.vue';

const SRC = path.resolve(__dirname, '../../src');

describe("Duplicates: the cards are the whole report's totals", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('prints total_groups / total_copies / total_waste, not a sum of the groups it was sent', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
    const w = mount(Duplicates, { global: { plugins: [i18n] } });
    await flushPromises();
    await flushPromises();
    const text = w.text();
    expect(text).toContain(formatNumber(250, 'en'));
    expect(text).toContain(formatNumber(412, 'en'));
    expect(text).toContain(formatBytes(987_654_321, 'en'));
  });
});

describe('the pages add nothing up', () => {
  it('Duplicates.vue sums no group', () => {
    const src = readFileSync(path.join(SRC, 'views/Duplicates.vue'), 'utf8');
    expect(src).not.toMatch(/groups\.value\.reduce/);
  });

  it('api/dashboard.ts re-adds no storage row and sorts no timestamp', () => {
    const src = readFileSync(path.join(SRC, 'api/dashboard.ts'), 'utf8');
    expect(src).not.toMatch(/\.reduce\(/);
    expect(src).not.toMatch(/\.sort\(\)/);
  });

  it('Usage.vue works out no reading of its own (usage.SumBuckets / PerBucket / Trend are the server’s)', () => {
    const src = readFileSync(path.join(SRC, 'views/Usage.vue'), 'utf8');
    expect(src).not.toMatch(/byte_hours\s*\/\s*24/);
    expect(src).not.toMatch(/account_ops\?\.A/);
  });
});
