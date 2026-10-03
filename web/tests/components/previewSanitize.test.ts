// @vitest-environment jsdom
//
// ⚠ jsdom, not happy-dom: DOMPurify walks the parsed tree with a NodeIterator,
// and happy-dom's iterator loses its place when a node is removed under it —
// every element after the first removal went unvisited (measured: `<b>` kept
// by the code policy, a whole mutation vector kept by the document policy).
// jsdom is the DOM DOMPurify's own test-suite runs against.
//
// A preview draws somebody's file as HTML inside filex's own page, so what it
// draws must be inert: the Markdown preview (inline HTML allowed, the
// GitHub/GitLab contract) and a notebook's cells and HTML outputs are judged
// against the shared vector list (tests/fixtures/previewVectors.ts), and a
// normal README must still look like a README.
//
// Both surfaces sanitize through packages/core/src/lib/sanitizeHtml.ts — one
// function for the web explorer, the desktop app and every embed.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import IpynbViewer from '@brftech/filex-core/src/viewers/IpynbViewer.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { activeContent, BENIGN_MARKDOWN, VECTORS } from '../fixtures/previewVectors';
import { teardownDom } from '../helpers/teardown';

const mdNode = (basename: string): FileNode =>
  ({ path: `main://${basename}`, basename, type: 'file', extension: 'md', size: 100 }) as FileNode;

async function renderedMarkdown(text: string): Promise<HTMLElement> {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(text, { status: 200 })));
  mount(PreviewModal, {
    attachTo: document.body,
    props: {
      open: true,
      locale: 'en',
      file: mdNode('README.md'),
      openMode: 'view',
      previewUrl: (p: string) => `/preview?path=${p}`,
      downloadUrl: (p: string) => `/download?path=${p}`,
    },
  });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(document.querySelector('.fe-preview__md')).not.toBeNull();
    },
    { timeout: 5000 },
  );
  return document.querySelector('.fe-preview__md') as HTMLElement;
}

async function renderedNotebook(nb: unknown): Promise<HTMLElement> {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(nb), { status: 200 })));
  const w = mount(IpynbViewer, { attachTo: document.body, props: { url: '/nb.ipynb', ext: 'ipynb' } });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(w.find('.filex-viewer-ipynb, [class*="ipynb"]').exists()).toBe(true);
      expect(w.text()).toContain('CELL-END');
    },
    { timeout: 5000 },
  );
  return w.element as HTMLElement;
}

// Pages down first (in-flight work lands, pages unmount, <body> empties),
// while this file's mocks still answer; only then are the mocks taken away.
afterEach(async () => {
  await teardownDom();
  vi.unstubAllGlobals();
});

describe('the Markdown preview draws inline HTML inert', () => {
  for (const [name, html] of Object.entries(VECTORS)) {
    it(name, async () => {
      const el = await renderedMarkdown(`Before.\n\n${html}\n\nAfter.`);
      expect(activeContent(el)).toEqual([]);
      expect(el.textContent).toContain('Before.');
    });
  }

  it('a normal README keeps its headings, table, code, picture, link and details', async () => {
    const el = await renderedMarkdown(BENIGN_MARKDOWN);
    expect(activeContent(el)).toEqual([]);
    expect(el.querySelector('h1')?.textContent).toBe('Project title');
    expect(el.querySelectorAll('table td').length).toBe(2);
    expect(el.querySelector('pre code')?.textContent).toContain('const x = 1 < 2;');
    const img = el.querySelector('img');
    expect(img?.getAttribute('src')).toBe('https://example.test/logo.png');
    expect(img?.getAttribute('width')).toBe('72');
    expect(el.querySelector('a')?.getAttribute('href')).toBe('https://example.test/docs');
    expect(el.querySelector('details summary')?.textContent).toBe('More');
    expect((el.querySelector('p[style]') as HTMLElement | null)?.style.color).toBe('red');
    expect(el.querySelectorAll('kbd').length).toBe(2);
  });
});

describe('a notebook draws its cells and HTML outputs inert', () => {
  const nb = (html: string) => ({
    nbformat: 4,
    metadata: { kernelspec: { language: 'python' } },
    cells: [
      { cell_type: 'markdown', source: ['# Notes\n', 'text'] },
      {
        cell_type: 'code',
        source: ['print(1)'],
        execution_count: 1,
        outputs: [{ output_type: 'display_data', data: { 'text/html': [html] } }],
      },
      { cell_type: 'markdown', source: ['CELL-END'] },
    ],
  });

  for (const [name, html] of Object.entries(VECTORS)) {
    it(name, async () => {
      const el = await renderedNotebook(nb(html));
      expect(activeContent(el)).toEqual([]);
    });
  }

  it('a table output still renders as a table', async () => {
    const el = await renderedNotebook(nb('<table><tr><th>k</th></tr><tr><td>v</td></tr></table>'));
    expect(el.querySelector('table td')?.textContent).toBe('v');
    expect(el.querySelector('h1')?.textContent).toBe('Notes');
  });
});
