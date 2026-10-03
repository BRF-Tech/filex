// The details panel's "Create link" asks the rule the explorer's Share row
// asks (lib/e2eLinks publicLinksOff): never a public link for an end-to-end
// encrypted folder or anything in it (0.50, #113). The Share row was hidden
// there, but the panel still drew "Create link" inside an encrypted folder,
// and the server refused the click with 409 E2E_ENCRYPTED.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import InspectorPanel from '@brftech/filex-core/src/components/InspectorPanel.vue';

describe('the details panel: no "Create link" in an encrypted folder', () => {
  const file = {
    id: 6,
    path: 'depo://kasa/rapor.pdf',
    basename: 'rapor.pdf',
    type: 'file' as const,
    extension: 'pdf',
    size: 5,
    last_modified: 1_757_000_000_000,
    perm: 'owner',
  };
  function api() {
    return {
      listShares: async () => ({ shares: [] }),
      listVersions: async () => [],
      listPermissions: async () => ({ direct: [], inherited: [], storage_rbac: true }),
      listComments: async () => [],
      createShare: vi.fn(async () => ({ share: { url: 'x' } })),
    };
  }
  function panel(node: Record<string, unknown>, extra: Record<string, unknown> = {}) {
    const a = api();
    const w = mount(InspectorPanel, {
      props: { api: a as never, nodes: [node] as never, dirLabel: 'kasa', dirCount: 1, locale: 'en', ...extra },
      attachTo: document.body,
    });
    return { w, a };
  }
  const createLink = (w: ReturnType<typeof panel>['w']) => w.find('[data-testid="inspector-create-link"]');

  it('a file inside an encrypted folder (the host says so) gets no Create link', async () => {
    const { w } = panel(file, { inEncrypted: true });
    await flushPromises();
    // The link row itself is still drawn: it says there is no link.
    expect(w.find('[data-testid="inspector-shares"]').exists()).toBe(true);
    expect(createLink(w).exists()).toBe(false);
    w.unmount();
  });

  it("the encrypted folder's own row in its parent gets none either", async () => {
    const { w } = panel({ ...file, path: 'depo://kasa', basename: 'kasa', type: 'dir', extension: '', e2e: true });
    await flushPromises();
    expect(createLink(w).exists()).toBe(false);
    w.unmount();
  });

  it('a row from Recent, Starred or a search that names its encrypted folder gets none', async () => {
    const { w } = panel({ ...file, e2e_root: 'depo://kasa' });
    await flushPromises();
    expect(createLink(w).exists()).toBe(false);
    w.unmount();
  });

  it('an ordinary file, and a single .fxe outside any folder, still get it - and the click makes a link', async () => {
    const { w, a } = panel({ ...file, path: 'depo://rapor.pdf' });
    await flushPromises();
    expect(createLink(w).exists()).toBe(true);
    await createLink(w).trigger('click');
    await flushPromises();
    expect(a.createShare).toHaveBeenCalledTimes(1);
    w.unmount();

    const fxe = panel({ ...file, path: 'depo://rapor.pdf.fxe', basename: 'rapor.pdf.fxe', extension: 'fxe' }, { inEncrypted: false });
    await flushPromises();
    expect(createLink(fxe.w).exists()).toBe(true);
    fxe.w.unmount();
  });
});

describe('the explorer tells the panel where its item is', () => {
  const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

  it('passes the encrypted-folder state of the item it shows', () => {
    expect(src).toMatch(/<InspectorPanel[\s\S]{0,4000}:in-encrypted="inspectorInEncrypted/);
  });
});
