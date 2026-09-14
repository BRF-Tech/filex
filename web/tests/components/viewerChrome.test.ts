// gorunum:v1-viewer — the viewer overlay's chrome contract.
//
// The viewer stopped being a card with a Download/Close footer and became a
// full-bleed overlay: a top bar (type tile + name + "246.3 KB • Sep 9, 2026 •
// 1 of 9"), centred icon actions, a chevron on each screen edge, and a zoom
// pill at the bottom.
//
// Two things here cannot be measured in a browser today and would otherwise
// rot silently:
//
//   1. The counter needs `index` + `total`, which only the HOST can answer —
//      the modal is handed one file and has no idea what list it came from.
//      Until FileExplorer fills them the counter never appears on screen, so
//      this is the only place that proves the props are still wired.
//   2. The zoom pill must NOT be drawn for pdf / office / drawio. Those three
//      paint their own zoom inside their surface; a second percentage the
//      content does not obey is a control that lies, and "it isn't there" is
//      exactly the kind of absence no screenshot ever catches.
//
// packages/core has no runner of its own; the gate lives here, like coreKeys
// and archiveViewerEndpoint.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

function node(over: Partial<FileNode> = {}): FileNode {
  return {
    path: 'qldemo://photos/beach.jpg',
    basename: 'beach.jpg',
    type: 'file',
    extension: 'jpg',
    size: 252_211,
    last_modified: 1_757_376_000,
    ...over,
  } as FileNode;
}

function mountViewer(props: Record<string, unknown> = {}) {
  return mount(PreviewModal, {
    props: {
      open: true,
      locale: 'en',
      file: node(),
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      ...props,
    },
  });
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({ ok: true, status: 200, statusText: 'OK', text: async () => '' }),
  );
});

describe('viewer overlay chrome', () => {
  it('is a full-bleed overlay, not a card with a footer', () => {
    const w = mountViewer();
    expect(w.find('.fe-modal__card--fullbleed').exists()).toBe(true);
    expect(w.find('.fe-viewer__bar').exists()).toBe(true);
    // The old card chrome is gone: no dialog head, no Download/Close footer.
    expect(w.find('.fe-modal__head').exists()).toBe(false);
    expect(w.find('.fe-modal__actions').exists()).toBe(false);
  });

  it('prints size • date • counter when the host answered index and total', () => {
    const w = mountViewer({ index: 1, total: 9 });
    const meta = w.find('.fe-viewer__meta').text();
    expect(meta).toContain('1 of 9');
    expect(meta).toContain('KB');
    expect(meta.split('•').length).toBe(3);
  });

  it('leaves the counter out rather than guessing it', () => {
    // No index/total: size + date only. A "1 of 1" invented here would be a
    // claim about a listing this component has never seen.
    const w = mountViewer();
    const meta = w.find('.fe-viewer__meta').text();
    expect(meta).not.toContain(' of ');
    expect(meta.split('•').length).toBe(2);
  });

  it('reuses the nav contract QuickLook already emits — one delta, ±1', async () => {
    const w = mountViewer({ index: 3, total: 9 });
    await w.find('.fe-viewer__chev--next').trigger('click');
    await w.find('.fe-viewer__chev--prev').trigger('click');
    expect(w.emitted('nav')).toEqual([[1], [-1]]);
  });

  it('draws no chevrons when there is nowhere to go', () => {
    const w = mountViewer({ index: 1, total: 1 });
    expect(w.find('.fe-viewer__chev--next').exists()).toBe(false);
  });

  it('shows the zoom pill for images', () => {
    const w = mountViewer();
    expect(w.find('.fe-viewer__zoom').exists()).toBe(true);
    expect(w.find('.fe-viewer__zoom-level').text()).toBe('100%');
  });

  it.each([
    ['pdf', 'report.pdf'],
    ['docx', 'contract.docx'],
    ['drawio', 'topology.drawio'],
  ])('hides the zoom pill for %s — those viewers zoom themselves', (extension, basename) => {
    const w = mountViewer({
      file: node({ extension, basename, path: `qldemo://docs/${basename}` }),
    });
    expect(w.find('.fe-viewer__zoom').exists()).toBe(false);
  });

  it('draws share only when the host is listening for it', () => {
    expect(mountViewer().findAll('.fe-viewer__act').some((b) => b.attributes('title') === 'Share')).toBe(
      false,
    );
    const w = mountViewer({ shareEnabled: true });
    const share = w.findAll('.fe-viewer__act').find((b) => b.attributes('title') === 'Share');
    expect(share).toBeTruthy();
    share!.trigger('click');
    expect(w.emitted('share')).toBeTruthy();
  });

  it('keeps chromeless bare — the standalone route IS the container', () => {
    const w = mountViewer({ chromeless: true, index: 1, total: 9 });
    expect(w.find('.fe-modal__card--chromeless').exists()).toBe(true);
    expect(w.find('.fe-modal__card--fullbleed').exists()).toBe(false);
    expect(w.find('.fe-viewer__bar').exists()).toBe(false);
    expect(w.find('.fe-viewer__chev--next').exists()).toBe(false);
    expect(w.find('.fe-viewer__zoom').exists()).toBe(false);
  });
});
