// Which editor a file with no telling extension opens in (#56).
//
// The viewer picks its surface from the extension, and `LICENSE` has none:
// before #56 it fell through to "Download". Two answers now:
//
//  - `openAs` — the host knows what the file IS. The New document dialog
//    made it as Plain text, so it opens in the text editor whatever it is
//    called, right after creation.
//  - the file's mime — a name that picks no viewer, whose bytes the server
//    calls text, opens as the plain text it is (every later open of LICENSE).
//
// A name that DOES pick a viewer keeps it: the mime never overrides a known
// extension.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const node = (basename: string, mime?: string): FileNode =>
  ({
    path: `main://${basename}`,
    basename,
    type: 'file',
    extension: basename.lastIndexOf('.') > 0 ? basename.slice(basename.lastIndexOf('.') + 1) : '',
    size: 12,
    ...(mime === undefined ? {} : { mime_type: mime }),
  }) as FileNode;

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

function viewer(file: FileNode, openAs?: string) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('MIT License\n', { status: 200 })));
  return mount(PreviewModal, {
    props: {
      open: true,
      locale: 'en',
      file,
      openMode: 'view',
      ...(openAs ? { openAs } : {}),
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
    },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('a document just created from a type opens as that type', () => {
  it('LICENSE made as Plain text opens in the text surface', async () => {
    const w = viewer(node('LICENSE'), 'txt');
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(true);
    expect(w.text()).toContain('MIT License');
  });

  it('README made as Markdown opens in the markdown surface', async () => {
    const w = viewer(node('README'), 'md');
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(false);
    // Rendered, or its raw text while markdown-it loads — either way the
    // markdown branch, not the download fallback.
    expect(w.find('.fe-preview__md, .fe-preview__pre').exists()).toBe(true);
    expect(w.text()).toContain('MIT License');
  });
});

describe('later, the server’s word on the bytes decides for a name that says nothing', () => {
  it('an extensionless text file opens as text', async () => {
    const w = viewer(node('NOTICE', 'text/plain; charset=utf-8'));
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(true);
  });

  it('an unknown extension with text in it opens as text', async () => {
    const w = viewer(node('example.custom', 'text/plain; charset=utf-8'));
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(true);
  });

  it('binary bytes under such a name still get the download fallback', async () => {
    const w = viewer(node('blob', 'application/octet-stream'));
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(false);
  });

  it('a name that picks a viewer keeps it, whatever the mime says', async () => {
    const w = viewer(node('photo.png', 'text/plain'));
    await settle();
    expect(w.find('.fe-preview__code-wrap').exists()).toBe(false);
  });
});
