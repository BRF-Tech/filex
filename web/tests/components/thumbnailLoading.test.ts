// Thumbnails in a big folder.
//
// Field report (Windows PC, v0.42.2): opening a folder of 344 files (~240 with
// thumbnails) and clicking any sub-folder killed the Chrome tab ("Out of
// Memory"), and froze Edge and Opera for ~30 s with the fans spinning; on a
// fast Mac the same click took ~4 s. The server saw no request in between: the
// page was busy with itself. useThumbs fetched every thumbnail of the folder at
// once and, as each one arrived, replaced its whole URL map — so every arrival
// re-rendered the entire folder view: ~240 arrivals × 344 rows within seconds.
//
// What these tests hold the loader and the views to:
//   - a thumbnail arriving wakes the tile that shows it, not the folder view;
//   - a tile asks for its thumbnail only once it is near the viewport;
//   - a thumbnail is not fetched again because its signed URL rotated (the
//     signature's expiry moves every hour); a new version of the file is;
//   - past the cache's cap, the oldest thumbnail is dropped without its tile
//     fetching it again (which dropped the next one, and so on, forever).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick, watchEffect } from 'vue';

import { useThumbs } from '@brftech/filex-core/src/composables/useThumbs';
import { __resetNearViewport } from '@brftech/filex-core/src/lib/nearViewport';
import ThumbTile from '@brftech/filex-core/src/components/ThumbTile.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import GalleryView from '@brftech/filex-core/src/components/GalleryView.vue';
import ListView from '@brftech/filex-core/src/components/ListView.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const api = { authHeaders: async () => ({}), credentialsMode: () => 'include' as RequestCredentials };

function pdf(id: number, extra: Partial<FileNode> = {}): FileNode {
  return {
    id,
    path: `Depo://2026/2026.09/rapor-${id}.pdf`,
    basename: `rapor-${id}.pdf`,
    type: 'file',
    extension: 'pdf',
    size: 1000,
    last_modified: 1_790_000_000_000,
    thumb_url: `/api/files/thumb/${id}?exp=100&sig=aa`,
    ...extra,
  };
}

/** fetch that answers only when the test says so, so arrivals can be counted. */
let fetched: string[] = [];
let waiting: Array<() => void> = [];
/** Let every request that has been asked for actually start (the loader
 *  awaits its auth headers before it calls fetch). */
async function started() {
  await flushPromises();
}
async function arriveAll() {
  await started();
  const now = waiting;
  waiting = [];
  now.forEach((go) => go());
  await flushPromises();
  await nextTick();
}
/** The way they really come: one response at a time, each its own event. */
async function arriveOneByOne() {
  await started();
  while (waiting.length) {
    waiting.shift()!();
    await flushPromises();
    await nextTick();
  }
}

/** IntersectionObserver the test drives: nothing is near the viewport until
 *  `enter` says so (or at once, with `immediate`). */
let observed: Element[] = [];
let ioCallback: IntersectionObserverCallback | null = null;
function stubViewport(immediate: boolean) {
  observed = [];
  class FakeIO {
    constructor(cb: IntersectionObserverCallback) {
      ioCallback = cb;
    }
    observe(el: Element) {
      observed.push(el);
      if (immediate) queueMicrotask(() => enter([el]));
    }
    unobserve(el: Element) {
      observed = observed.filter((o) => o !== el);
    }
    disconnect() {
      observed = [];
    }
    takeRecords() {
      return [];
    }
  }
  vi.stubGlobal('IntersectionObserver', FakeIO);
}
function enter(els: Element[]) {
  ioCallback?.(
    els.map((target) => ({ target, isIntersecting: true, intersectionRatio: 1 }) as unknown as IntersectionObserverEntry),
    {} as IntersectionObserver,
  );
}

let blobs = 0;
beforeEach(() => {
  fetched = [];
  waiting = [];
  blobs = 0;
  __resetNearViewport();
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (url: string) =>
        new Promise<Response>((resolve) => {
          fetched.push(url);
          waiting.push(() => resolve(new Response(new Blob(['jpeg']), { status: 200 })));
        }),
    ),
  );
  vi.spyOn(URL, 'createObjectURL').mockImplementation(() => `blob:thumb-${++blobs}`);
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('useThumbs', () => {
  it('a thumbnail arriving wakes only what shows that thumbnail', async () => {
    const thumbs = useThumbs(undefined, api);
    const a = pdf(1);
    const b = pdf(2);
    let runsA = 0;
    let runsB = 0;
    const stopA = watchEffect(() => {
      runsA++;
      thumbs.src(a);
    });
    const stopB = watchEffect(() => {
      runsB++;
      thumbs.src(b);
    });
    await started();
    expect(fetched).toHaveLength(2);

    waiting.shift()!(); // a's thumbnail arrives, b's is still on its way
    await flushPromises();
    await nextTick();

    expect(runsA).toBe(2);
    expect(runsB).toBe(1);
    stopA();
    stopB();
  });

  it('does not fetch a thumbnail again because its signature rotated', async () => {
    const thumbs = useThumbs(undefined, api);
    const first = pdf(7, { thumb_url: '/api/files/thumb/7?exp=100&sig=aa' });
    thumbs.src(first);
    await arriveAll();
    const shown = thumbs.src(first);
    expect(shown).toMatch(/^blob:/);

    const rotated = { ...first, thumb_url: '/api/files/thumb/7?exp=200&sig=bb' };
    expect(thumbs.src(rotated)).toBe(shown);
    await started();
    expect(fetched).toHaveLength(1);
  });

  it('fetches a new version of the file', async () => {
    const thumbs = useThumbs(undefined, api);
    const first = pdf(7);
    thumbs.src(first);
    await arriveAll();

    const edited = { ...first, last_modified: first.last_modified! + 60_000, thumb_url: '/api/files/thumb/7?exp=200&sig=bb' };
    expect(thumbs.src(edited)).toBeNull();
    await started();
    expect(fetched).toHaveLength(2);
  });

  it('past its cap, drops the oldest thumbnail without fetching it again', async () => {
    // The cache holds 500. With more on screen, dropping the oldest used to
    // wake its tile, which fetched it again, whose arrival dropped the next
    // oldest, whose tile fetched it again — for as long as the folder was open.
    const thumbs = useThumbs(undefined, api);
    const shown = Array.from({ length: 501 }, (_, i) => pdf(i + 1));
    const stops = shown.map((n) => watchEffect(() => void thumbs.src(n)));
    await started();
    expect(fetched).toHaveLength(501);

    for (let i = 0; i < 700 && waiting.length; i++) {
      waiting.shift()!();
      await flushPromises();
      await started();
    }

    expect(fetched).toHaveLength(501);
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1);
    stops.forEach((stop) => stop());
  });
});

describe('ThumbTile', () => {
  it('does not ask for a thumbnail until it is near the viewport', async () => {
    stubViewport(false);
    const srcOf = vi.fn(() => null);
    const w = mount(ThumbTile, {
      props: { node: pdf(1), srcOf },
      slots: { default: '<span class="fallback">PDF</span>' },
    });
    await nextTick();
    expect(srcOf).not.toHaveBeenCalled();
    expect(w.find('.fallback').exists()).toBe(true);

    enter([...observed]);
    await nextTick();
    expect(srcOf).toHaveBeenCalled();
  });

  it('shows the picture with the attributes it was given, and the badge over a video frame', async () => {
    stubViewport(true);
    const w = mount(ThumbTile, {
      props: { node: pdf(1), srcOf: () => 'blob:x', videoBadge: true },
      attrs: { class: 'fe-list__icon', alt: 'rapor-1.pdf' },
      slots: { default: '<span class="fallback">PDF</span>' },
    });
    await flushPromises();
    await nextTick();
    const img = w.get('img');
    expect(img.attributes('src')).toBe('blob:x');
    expect(img.classes()).toContain('fe-list__icon');
    expect(img.attributes('alt')).toBe('rapor-1.pdf');
    expect(img.attributes('draggable')).toBe('false');
    expect(w.find('.fallback').exists()).toBe(false);
    expect(w.find('.fe-thumb__play').exists()).toBe(true);
  });
});

/** Mount a folder view over `count` files with a real useThumbs behind it and
 *  count how often each file's thumbnail is asked for — and how often each
 *  component re-renders (`renders`, by component name, from every
 *  component's `updated` hook). */
async function folder(view: typeof GridView | typeof GalleryView | typeof ListView, count: number, immediate: boolean) {
  stubViewport(immediate);
  const thumbs = useThumbs(undefined, api);
  const asked = new Map<number, number>();
  const renders = new Map<string, number>();
  const thumbSrc = (n: FileNode) => {
    asked.set(n.id!, (asked.get(n.id!) ?? 0) + 1);
    return thumbs.src(n);
  };
  const countRenders = {
    updated(this: { $options: { __name?: string; name?: string } }) {
      const name = this.$options.__name ?? this.$options.name ?? '?';
      renders.set(name, (renders.get(name) ?? 0) + 1);
    },
  };
  const files = Array.from({ length: count }, (_, i) => pdf(i + 1));
  const w = mount(view, {
    props: { files, selected: new Set<string>(), locale: 'en', thumbSrc },
    global: { mixins: [countRenders] },
  });
  await flushPromises();
  await nextTick();
  return { w, asked, renders, total: () => [...asked.values()].reduce((s, n) => s + n, 0) };
}

describe.each([
  ['grid', GridView],
  ['gallery', GalleryView],
  ['list', ListView],
])('the %s view of a big folder', (_name, view) => {
  it('asks for each thumbnail a bounded number of times, not once per arrival', async () => {
    const f = await folder(view, 60, true);
    await started();
    expect(fetched).toHaveLength(60);
    await arriveOneByOne();

    expect(f.w.findAll('img').length).toBe(60);
    // Old behaviour: every arrival re-rendered the view, which asked for all
    // 60 thumbnails again — ~60 × 60 = 3,600 asks.
    expect(f.total()).toBeLessThanOrEqual(60 * 3);
  });

  it('fetches only the thumbnails of tiles near the viewport', async () => {
    const f = await folder(view, 60, false);
    await started();
    expect(fetched).toHaveLength(0);
    enter(observed.slice(0, 10));
    await flushPromises();
    await nextTick();
    await started();
    expect(fetched).toHaveLength(10);
    expect(f.total()).toBeGreaterThan(0);
  });

  // Counted in RENDERS, not asks: the claim is that an arrival re-renders the
  // tile that shows it. On v0.43.0 one arrival re-rendered GridView,
  // GalleryView, or the explorer's DataTable — every tile of the folder.
  it('an arrival re-renders its own tile, and neither the view nor its table', async () => {
    const f = await folder(view, 40, true);
    await started();
    expect(fetched).toHaveLength(40);
    const viewName = (view as { __name?: string }).__name!;
    f.renders.clear();

    waiting.shift()!();
    await flushPromises();
    await nextTick();
    expect(f.renders.get(viewName) ?? 0, `${viewName} re-rendered`).toBe(0);
    expect(f.renders.get('DataTable') ?? 0, 'the table re-rendered').toBe(0);
    expect(f.renders.get('ThumbTile') ?? 0, 'tiles re-rendered by one arrival').toBe(1);

    f.renders.clear();
    await arriveOneByOne();
    expect(f.renders.get(viewName) ?? 0).toBe(0);
    expect(f.renders.get('DataTable') ?? 0).toBe(0);
    expect(f.renders.get('ThumbTile') ?? 0).toBe(39);
  });

  // The eviction loop, through the real view: on v0.43.0, 501 tiles and 700
  // arrivals allowed left 313 thumbnails still being fetched again.
  it('past the cache cap with every tile on screen, an eviction does not loop', async () => {
    await folder(view, 501, true);
    await started();
    expect(fetched).toHaveLength(501);
    for (let i = 0; i < 700 && waiting.length; i++) {
      waiting.shift()!();
      await flushPromises();
      await nextTick();
      await started();
    }
    expect(fetched).toHaveLength(501);
    expect(waiting).toHaveLength(0);
  }, 60_000);
});
