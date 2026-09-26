// Restoring a version, and taking a snapshot, say so while they run.
//
// ⚠ Both copy the whole file on the storage, which for a large one takes a
// while, and the buttons only went grey: nothing said a restore was under way,
// so the pane read as stuck. The pressed button now names its work.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';

let finish: () => void = () => {};

const held = () =>
  new Promise<void>((resolve) => {
    finish = resolve;
  });

const api = {
  listShares: vi.fn(async () => ({ shares: [] })),
  listVersions: vi.fn(async () => [{ id: 3, node_id: 6, version_n: 2, size: 10, created_at: '2026-09-20T10:00:00Z' }]),
  listPermissions: vi.fn(async () => {
    throw new Error('no grants here');
  }),
  listComments: vi.fn(async () => {
    throw new Error('no comments here');
  }),
  createShare: vi.fn(),
  restoreVersion: vi.fn(held),
  snapshotVersion: vi.fn(held),
  addComment: vi.fn(),
  deleteComment: vi.fn(),
};

const NODE = {
  id: 6,
  path: 'demo://Rapor.docx',
  basename: 'Rapor.docx',
  type: 'file' as const,
  extension: 'docx',
  size: 20,
  last_modified: 1_757_000_000_000,
};

const button = (w: VueWrapper, re: RegExp) => w.findAll('button').find((b) => re.test(b.text().trim()));

async function versionsTab(): Promise<VueWrapper> {
  const w = mount(InspectorPanel, {
    props: { api: api as never, nodes: [NODE], dirLabel: 'demo', dirCount: 1, locale: 'en' },
    attachTo: document.body,
  });
  await vi.waitFor(() => expect(button(w, /^Activity$/)).toBeTruthy());
  await button(w, /^Activity$/)!.trigger('click');
  await vi.waitFor(() => expect(button(w, /^Restore$/)).toBeTruthy());
  return w;
}

describe('InspectorPanel — version work under way', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('a restore says it is restoring until it ends', async () => {
    const w = await versionsTab();
    await button(w, /^Restore$/)!.trigger('click');
    await vi.waitFor(() => expect(button(w, /^Confirm$/)).toBeTruthy());
    await button(w, /^Confirm$/)!.trigger('click');
    await flushPromises();

    const pressed = button(w, /^Restoring…$/);
    expect(pressed, 'the pressed button did not say it is restoring').toBeTruthy();
    expect(pressed!.attributes('disabled')).toBeDefined();
    expect(pressed!.attributes('aria-busy')).toBe('true');

    finish();
    await flushPromises();
    expect(button(w, /^Restoring…$/)).toBeUndefined();
    w.unmount();
  });

  it('a snapshot says it is being taken until it ends', async () => {
    const w = await versionsTab();
    await button(w, /^Snapshot current state$/)!.trigger('click');
    await flushPromises();

    const pressed = button(w, /^Taking a snapshot…$/);
    expect(pressed, 'the pressed button did not say a snapshot is being taken').toBeTruthy();
    expect(pressed!.attributes('disabled')).toBeDefined();
    expect(button(w, /^Restore$/)!.attributes('disabled'), 'a restore could start beside it').toBeDefined();

    finish();
    await flushPromises();
    expect(button(w, /^Snapshot current state$/)).toBeTruthy();
    w.unmount();
  });
});
