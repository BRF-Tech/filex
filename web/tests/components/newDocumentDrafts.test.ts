// New document makes a DRAFT (issue #71) — on a server that keeps drafts for
// this person. Nothing is created in the folder; at the draft limit the
// dialog says so and points at Drafts, and never creates the file instead.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import NewDocumentModal from '@brftech/filex-core/src/modals/NewDocumentModal.vue';
import { requestFailure } from '@brftech/filex-core/src/lib/errorWords';
import type { NewDocType } from '@brftech/filex-core/src/types/FileNode';

const TYPES: NewDocType[] = [{ ext: 'txt', group: 'text', mime: 'text/plain; charset=utf-8', ext_required: false }];

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

let mounted: VueWrapper | null = null;
afterEach(() => {
  mounted?.unmount();
  mounted = null;
});

async function open(drafts: boolean, create?: (...a: unknown[]) => Promise<unknown>, locale = 'en') {
  const index = vi.fn(async () => ({
    adapter: 'main', storages: ['main'], dirname: 'main://', read_only: false, perm: 'owner', files: [],
  }));
  const newFile = vi.fn(async () => ({ path: 'main://Untitled.txt', name: 'Untitled.txt', ext: 'txt', size: 0, mime: 'text/plain' }));
  const draftCreate = vi.fn(
    create ??
      (async (path: unknown, name: unknown) => ({
        path: `main://.filex-drafts/7/0123456789abcdef/${name}`,
        name,
        ext: 'txt',
        size: 0,
        mime: 'text/plain',
        draft: { key: '0123456789abcdef', name, target_dir: path },
      })),
  );
  const w = mount(NewDocumentModal, {
    attachTo: document.body,
    props: {
      open: false,
      locale,
      api: { index, newFile, drafts: { create: draftCreate } } as never,
      types: TYPES,
      currentPath: 'main://',
      storages: ['main'],
      drafts,
    },
  });
  mounted = w;
  await w.setProps({ open: true });
  await settle();
  return { w, newFile, draftCreate };
}

describe('New document, on a server that keeps drafts', () => {
  it('makes a draft, not the file — and says so before Create', async () => {
    const { w, newFile, draftCreate } = await open(true);
    expect(w.get('[data-testid="newdoc-draft-hint"]').text()).toContain('nothing is created in the folder until you save it');
    await w.get('[data-testid="newdoc-create"]').trigger('click');
    await settle();
    expect(newFile).not.toHaveBeenCalled();
    expect(draftCreate).toHaveBeenCalledWith('main://', 'Untitled.txt', 'txt', { exactName: true });
    expect((w.emitted('created')?.[0]?.[0] as { draft?: unknown }).draft).toBeTruthy();
  });

  it('at the limit says so, points at Drafts, and creates nothing instead', async () => {
    const refusal = requestFailure(409, JSON.stringify({ code: 'DRAFT_LIMIT', count: 50, error: 'no', limit: 50 }), 'en');
    const { w, newFile } = await open(true, async () => {
      throw refusal;
    });
    await w.get('[data-testid="newdoc-create"]').trigger('click');
    await settle();
    const box = w.get('[data-testid="newdoc-draft-limit"]');
    expect(box.text()).toContain('You already keep as many drafts as this server allows (50)');
    expect(newFile).not.toHaveBeenCalled();
    expect(w.emitted('created')).toBeUndefined();
    await w.get('[data-testid="newdoc-open-drafts"]').trigger('click');
    expect(w.emitted('open-drafts')).toHaveLength(1);
  });

  it('the limit, in Turkish', async () => {
    const refusal = requestFailure(409, JSON.stringify({ code: 'DRAFT_LIMIT', limit: 50 }), 'tr');
    const { w } = await open(true, async () => {
      throw refusal;
    }, 'tr');
    await w.get('[data-testid="newdoc-create"]').trigger('click');
    await settle();
    expect(w.get('[data-testid="newdoc-draft-limit"]').text()).toContain('Taslaklar’da bazılarını kaydedin ya da silin');
    expect(w.get('[data-testid="newdoc-open-drafts"]').text()).toBe('Taslakları aç');
  });
});

describe('New document, on a server without drafts (or for an app, a confined embed)', () => {
  it('creates the file, as it always did', async () => {
    const { w, newFile, draftCreate } = await open(false);
    expect(w.find('[data-testid="newdoc-draft-hint"]').exists()).toBe(false);
    await w.get('[data-testid="newdoc-create"]').trigger('click');
    await settle();
    expect(newFile).toHaveBeenCalled();
    expect(draftCreate).not.toHaveBeenCalled();
  });
});
