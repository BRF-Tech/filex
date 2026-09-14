// etiket:t1 — the details panel's Tags section, mounted.
//
// ⚠ This file exists for the emit, not for the chips. `TagPicker` already
// knows how to read and write `/api/files/manager/tags`; what is new here is
// the SEAM between the panel and its host: the panel has to tell the host that
// tags changed, because the host is what drops the cached tag list, refreshes
// the sidebar's Tags section and reloads an open tag view. The context-menu
// tag modal has always caused that. A details panel that edited tags without
// it would save correctly and leave the sidebar showing the list from before
// the edit — a staleness nobody notices until they go looking for the tag they
// just made, and by then it looks like the save failed.
//
// The other half pinned here is the refusal: no node id, or a host that wired
// no API base, and the section is not drawn AT ALL. An "+ Add tag" button that
// cannot save is worse than no button.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';

import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';

/** Every section other than Tags refuses, so the panel under test is just the
 *  head, General and Tags. These are the only `api` members it reaches for. */
const api = {
  listShares: vi.fn(async () => {
    throw new Error('no shares here');
  }),
  listVersions: vi.fn(async () => {
    throw new Error('no versions here');
  }),
  listPermissions: vi.fn(async () => {
    throw new Error('no grants here');
  }),
  listComments: vi.fn(async () => {
    throw new Error('no comments here');
  }),
  createShare: vi.fn(),
  restoreVersion: vi.fn(),
  snapshotVersion: vi.fn(),
  addComment: vi.fn(),
  deleteComment: vi.fn(),
};

const NODE = {
  id: 6,
  path: 'demo://app.ts',
  basename: 'app.ts',
  type: 'file' as const,
  extension: 'ts',
  size: 20,
  last_modified: 1_757_000_000_000,
};

/** What the tag routes answered, newest last. */
let posted: Array<{ node_id: number; tags: string[] }> = [];

function mountPanel(extra: Record<string, unknown> = {}) {
  return mount(InspectorPanel, {
    props: {
      api: api as never,
      nodes: [NODE],
      dirLabel: 'demo',
      dirCount: 3,
      locale: 'en' as const,
      apiBase: '',
      authHeaders: () => ({}),
      authCredentials: 'same-origin' as RequestCredentials,
      ...extra,
    },
    attachTo: document.body,
  });
}

describe('InspectorPanel — the Tags section', () => {
  beforeEach(() => {
    posted = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (String(url).includes('/api/files/manager/tags')) {
          if (init?.method === 'POST') {
            posted.push(JSON.parse(String(init.body)));
            return { ok: true, status: 200, json: async () => ({}) } as unknown as Response;
          }
          return {
            ok: true,
            status: 200,
            json: async () => ({ tags: ['alpha'] }),
          } as unknown as Response;
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('draws what the node already carries', async () => {
    const w = mountPanel();
    await vi.waitFor(() => expect(w.find('[data-testid="inspector-tags"]').exists()).toBe(true));
    await vi.waitFor(() => expect(w.find('[data-testid="inspector-tags"]').text()).toContain('alpha'));
    w.unmount();
  });

  it('an edit here TELLS THE HOST, so the sidebar is not left stale', async () => {
    const w = mountPanel();
    const sec = () => w.find('[data-testid="inspector-tags"]');
    await vi.waitFor(() => expect(sec().text()).toContain('alpha'));

    await sec().find('.filex-tag-add-btn').trigger('click');
    await w.vm.$nextTick();
    const field = sec().find('.filex-tag-add input');
    await field.setValue('quarterly');
    await sec().find('.filex-tag-add').trigger('submit');

    // It reached the server…
    await vi.waitFor(() => expect(posted.length).toBe(1));
    expect(posted[0]).toEqual({ node_id: 6, tags: ['alpha', 'quarterly'] });

    // …and, the part this file is for, it reached the HOST.
    await vi.waitFor(() => expect(w.emitted('tags-changed')).toBeTruthy());
    expect(w.emitted('tags-changed')?.[0]).toEqual([['alpha', 'quarterly']]);
    w.unmount();
  });

  it('is not drawn at all when it could not save — no id, or no API base', async () => {
    // A client-synthesized row (a multi-storage virtual folder) has no node id,
    // and the tag routes are keyed by exactly that.
    const noId = mountPanel({ nodes: [{ ...NODE, id: undefined }] });
    await noId.vm.$nextTick();
    expect(noId.find('[data-testid="inspector-tags"]').exists()).toBe(false);
    noId.unmount();

    // ⚠ `apiBase: ''` is a VALID base ("same origin") and must keep the
    // section; only `undefined` — a host that wired nothing — removes it.
    const noBase = mountPanel({ apiBase: undefined });
    await noBase.vm.$nextTick();
    expect(noBase.find('[data-testid="inspector-tags"]').exists()).toBe(false);
    noBase.unmount();
  });
});
