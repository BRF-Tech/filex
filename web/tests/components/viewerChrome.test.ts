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
import { readFileSync } from 'node:fs';
import path from 'node:path';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const coreStyles = readFileSync(
  path.resolve(__dirname, '../../../packages/core/src/styles/base.css'),
  'utf8',
).replace(/\r\n/g, '\n');

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

function mountViewer(props: Record<string, unknown> = {}, stubs: Record<string, boolean> = {}) {
  return mount(PreviewModal, {
    props: {
      open: true,
      locale: 'en',
      file: node(),
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
      ...props,
    },
    global: { stubs },
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

  it('scopes zero-padding to the preview body so nested dialogs keep standard padding', () => {
    expect(coreStyles).toContain('.fe-modal__card--fullbleed > .fe-modal__body {');
    expect(coreStyles).not.toContain('.fe-modal__card--fullbleed .fe-modal__body {');
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

  // #110: the chevrons were drawn over a surface that fills the stage, and
  // covered its edge: filextext's page list and its scroll bar (measured in
  // Firefox and WebKit, two files in the folder). Such a stage keeps a gutter
  // for them, the same on both sides; a photo, with ground around it, does not.
  it.each([
    ['pdf', 'report.pdf'],
    ['markdown', 'README.md'],
    ['code', 'main.go'],
    ['office', 'contract.docx'],
    ['viewer', 'topology.drawio'],
  ])('keeps a gutter for the chevrons beside a %s surface', (_kind, basename) => {
    const extension = basename.split('.').pop()!;
    const file = node({ extension, basename, path: `qldemo://docs/${basename}` });
    const nav = mountViewer({ file, index: 2, total: 3 });
    expect(nav.find('[data-testid="viewer-stage"]').classes()).toContain('fe-viewer__stage--gutter');
    // The first file draws only "next" in some hosts' minds; the gutter does
    // not depend on which chevron, so the surface does not move.
    const first = mountViewer({ file, index: 1, total: 3 });
    expect(first.find('[data-testid="viewer-stage"]').classes()).toContain('fe-viewer__stage--gutter');
    // Nowhere to go: no chevrons, no gutter.
    const alone = mountViewer({ file, index: 1, total: 1 });
    expect(alone.find('[data-testid="viewer-stage"]').classes()).not.toContain('fe-viewer__stage--gutter');
  });

  it("keeps a gutter beside an app's own interface", () => {
    const w = mountViewer({
      file: node({ extension: 'fxtxt', basename: 'notes.fxtxt', path: 'qldemo://docs/notes.fxtxt' }),
      index: 2,
      total: 3,
      appViewer: { plugin: 'filextext', id: 'editor', label: { en: 'filextext' }, ui: { version: '0.1.1' } },
      api: {},
    }, { AppFrame: true });
    expect(w.find('[data-testid="viewer-stage"]').classes()).toContain('fe-viewer__stage--gutter');
  });

  it('leaves a photo where it was: no gutter', () => {
    const w = mountViewer({ index: 2, total: 3 });
    expect(w.find('[data-testid="viewer-stage"]').classes()).not.toContain('fe-viewer__stage--gutter');
  });

  it('sizes the gutter from the chevrons themselves, so the two cannot disagree', () => {
    const rule = (sel: string) => {
      const i = coreStyles.indexOf(`${sel} {`);
      expect(i, sel).toBeGreaterThanOrEqual(0);
      return coreStyles.slice(i, coreStyles.indexOf('}', i));
    };
    expect(rule('.fe-viewer__stage--gutter')).toMatch(
      /padding-inline:\s*calc\(var\(--fe-viewer-chev-size\)\s*\+\s*var\(--fe-viewer-chev-inset\)\s*\*\s*2\)/,
    );
    expect(rule('.fe-viewer__chev')).toMatch(/width:\s*var\(--fe-viewer-chev-size\)/);
    expect(coreStyles).toMatch(/\.fe-viewer__chev--prev\s*\{\s*inset-inline-start:\s*var\(--fe-viewer-chev-inset\)/);
    expect(coreStyles).toMatch(/\.fe-viewer__chev--next\s*\{\s*inset-inline-end:\s*var\(--fe-viewer-chev-inset\)/);
  });

  // #110: after a save made in the viewer the header still said the size and
  // date the file had when it was opened (filextext re-keyed its workspace
  // after a password reset: a new version on the storage, "1.92 KB" on top).
  it("says the new size after an app's interface saved, and tells the host", async () => {
    const file = node({ extension: 'fxtxt', basename: 'notes.fxtxt', path: 'qldemo://docs/notes.fxtxt', size: 252_211 });
    const w = mountViewer({
      file,
      appViewer: { plugin: 'filextext', id: 'editor', label: { en: 'filextext' }, ui: { version: '0.1.1' } },
      api: {},
    }, { AppFrame: true });
    expect(w.find('.fe-viewer__meta').text()).toContain('252.2');
    const frame = w.findComponent({ name: 'AppFrame' });
    // Another of its files saved: the header is about the first one.
    frame.vm.$emit('saved', { path: 'qldemo://docs/other.fxtxt', size: 9, index: 1 });
    await w.vm.$nextTick();
    expect(w.find('.fe-viewer__meta').text()).toContain('252.2');
    frame.vm.$emit('saved', { path: 'qldemo://docs/notes.fxtxt', size: 1_048_576, index: 0 });
    await w.vm.$nextTick();
    const meta = w.find('.fe-viewer__meta').text();
    expect(meta).not.toContain('252.2');
    expect(meta).toContain('MB');
    expect(w.emitted('saved')).toEqual([[{ path: 'qldemo://docs/notes.fxtxt', size: 1_048_576 }]]);
  });

  it('says the new size after the Markdown editor saved', async () => {
    // Fake timers: the editor saves 1.5 s after the last keystroke, and that
    // timer must not outlive the test (it would reach for the network later).
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const file = node({ extension: 'md', basename: 'notes.md', path: 'qldemo://docs/notes.md', size: 252_211 });
      const w = mountViewer({ file, openMode: 'edit', saveTextEndpoint: '/api/files/save-text' });
      await vi.advanceTimersByTimeAsync(0);
      const area = w.find('.fe-preview__md-split-input');
      expect(area.exists()).toBe(true);
      expect(w.find('.fe-viewer__meta').text()).toContain('252.2');
      await area.setValue('hello');
      await vi.advanceTimersByTimeAsync(1600);
      await w.vm.$nextTick();
      expect(w.find('.fe-viewer__meta').text()).not.toContain('252.2');
      expect(w.emitted('saved')?.[0]).toEqual([{ path: 'qldemo://docs/notes.md', size: 5 }]);
    } finally {
      vi.useRealTimers();
    }
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
