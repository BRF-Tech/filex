// A read-only drive says so on its Home card — on a line of its own.
//
// ⚠ Translator finding (v0.43.0): the caption was ONE string, "81 GB belegt ·
// Nur lesen", in a fixed 124 px slot with an ellipsis. In German it was 4 px too
// wide, and what the ellipsis ate was exactly the part that mattered — "read
// only". Shortening one language's words fixes one language; the tag on a line
// of its own (the same tag the side panel already draws) fits any language that
// fits the panel.
//
// And a grid card's caption carries its whole text on hover: German dates lost
// their year to the same kind of fixed-width ellipsis.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import HomeView from '@brftech/filex-core/src/components/HomeView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import { en } from '@brftech/filex-core/src/locales/en';

function home(storages: Array<Record<string, unknown>>, locale: 'en' | 'tr' = 'en') {
  return mount(HomeView, { props: { storages, recent: [], starred: [], locale } });
}

describe('the read-only tag on a Home card', () => {
  it('is its own element, not the tail of the size caption', () => {
    const w = home([{ name: 'arsiv', readOnly: true, usedBytes: 81_000_000_000 }]);
    const card = w.get('[data-testid="home-storage-card"]');
    expect(card.get('.fe-home__storage-meta').text()).toBe('81 GB used');
    expect(card.get('.fe-home__storage-meta').text()).not.toContain(en['sidenav.storage.readOnly']);
    expect(card.get('[data-testid="home-storage-readonly"]').text()).toBe(en['sidenav.storage.readOnly']);
  });

  it('a writable drive carries no tag', () => {
    const w = home([{ name: 'depo', usedBytes: 1000 }]);
    expect(w.find('[data-testid="home-storage-readonly"]').exists()).toBe(false);
  });

  // ⚠ Merge seam (feat/043-w2-explorer × feat/043-w2-admin): the admin's
  // StorageTags and this card each grew a read-only tag of their own. One
  // fact, one component: the Home card, the side panel and every admin list
  // draw StorageTags' read-only tag.
  it('is the one read-only tag every surface draws (StorageTags)', () => {
    const w = home([{ name: 'arsiv', readOnly: true, usedBytes: 81_000_000_000 }]);
    const tag = w.get('[data-testid="home-storage-readonly"]');
    expect(tag.classes()).toContain('fe-stags');
    expect(tag.get('[data-testid="storage-tag-readonly"]').classes()).toContain('fe-stag--warn');
    expect(tag.find('[data-testid="storage-tag-driver"]').exists()).toBe(false);
  });
});

describe('a grid card’s caption', () => {
  it('carries the whole caption on hover — the card has no room for every language’s date', async () => {
    const file = {
      id: 3,
      type: 'file' as const,
      path: 'depo://diagram.drawio',
      basename: 'diagram.drawio',
      extension: 'drawio',
      size: 110,
      last_modified: Date.UTC(2026, 8, 22, 11, 2),
    };
    const w = mount(GridView, { props: { files: [file], selected: new Set<string>(), locale: 'en' } });
    const meta = w.get('.fe-grid__meta');
    expect(meta.attributes('title')).toBeTruthy();
    expect(meta.attributes('title')).toBe(meta.text());
  });
});

describe('StorageTags', () => {
  // ⚠ Vue casts an ABSENT boolean prop to false, and `enabled: false` draws
  // "Disabled". A caller that passes no `enabled` — the explorer's read-only
  // mark, Admin → Replica's primary — must not be told the storage is off.
  it('draws "Disabled" only when enabled is false, not when it is absent', async () => {
    const { default: StorageTags } = await import('@brftech/filex-core/src/components/StorageTags.vue');
    const absent = mount(StorageTags, { props: { driver: 'local', readOnly: true, locale: 'en' } });
    expect(absent.find('[data-testid="storage-tag-disabled"]').exists()).toBe(false);
    expect(absent.get('[data-testid="storage-tag-readonly"]').exists()).toBe(true);
    const off = mount(StorageTags, { props: { driver: 'local', enabled: false, locale: 'en' } });
    expect(off.find('[data-testid="storage-tag-disabled"]').exists()).toBe(true);
    const on = mount(StorageTags, { props: { driver: 'local', enabled: true, locale: 'en' } });
    expect(on.find('[data-testid="storage-tag-disabled"]').exists()).toBe(false);
  });
});
