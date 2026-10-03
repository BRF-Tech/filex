// @vitest-environment jsdom
//
// ⚠ jsdom, not happy-dom: DOMPurify walks the parsed tree with a NodeIterator,
// and happy-dom's loses its place when a node is removed under it
// (previewSanitize.test.ts) - a vector could pass unvisited.
//
// ReleaseNotes draws a release's body - Markdown, from a GitHub release the
// app's or the storage plugin's source published - on both "Review update"
// screens (Apps and Plugins). Whoever controls that release controls these
// bytes, and the page they land in holds an administrator's session. So:
// rendered through the explorer preview's own pipeline (markdown-it, then the
// document sanitizer: core `markdownToSafeHtml`), held to the same vector list
// the preview is (tests/fixtures/previewVectors.ts), plus what only Markdown
// can spell (a `javascript:` or `data:` link target, an image source), and a
// link opens in a new tab with no handle back to the admin page.
//
// RED PROOF (task #122): the component does not exist on 0.49; the review
// printed the body as plain text (`**bold**` as asterisks).
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import ReleaseNotes from '@/components/plugins/ReleaseNotes.vue';
import { activeContent, VECTORS } from '../fixtures/previewVectors';

async function rendered(notes: string): Promise<HTMLElement> {
  const w = mount(ReleaseNotes, { props: { notes, testid: 'notes-md' }, attachTo: document.body });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(w.find('[data-testid="notes-md"]').exists()).toBe(true);
    },
    { timeout: 5000 },
  );
  return w.find('[data-testid="notes-md"]').element as HTMLElement;
}

const MARKDOWN_VECTORS: Record<string, string> = {
  'markdown javascript: link': '[click me](javascript:window.__fxPreview=1)',
  'markdown javascript: link, entity': '[click me](jav&#x61;script:window.__fxPreview=1)',
  'markdown data: HTML link': '[open](data:text/html;base64,PHNjcmlwdD53aW5kb3cuX19meFByZXZpZXc9MTwvc2NyaXB0Pg==)',
  'markdown javascript: image': '![x](javascript:window.__fxPreview=1)',
  'reference-style javascript: link': '[go][r]\n\n[r]: javascript:window.__fxPreview=1',
  'html injection out of the container': '</div></div><div onclick="window.__fxPreview=1">escaped?</div><script>window.__fxPreview=1</script>',
  'autolink javascript:': '<javascript:window.__fxPreview=1>',
};

describe('ReleaseNotes: a release body as Markdown, inert', () => {
  it('renders what a release body uses', async () => {
    const root = await rendered(
      ['## 1.2.0', '', '- **Faster** listings', '- `ctrl+s` saves', '', 'Full Changelog: https://github.com/BRF-Tech/filex-sign/compare/v1.1.0...v1.2.0'].join('\n'),
    );
    expect(root.querySelector('h2')?.textContent).toBe('1.2.0');
    expect(root.querySelector('strong')?.textContent).toBe('Faster');
    expect(root.querySelectorAll('li')).toHaveLength(2);
    expect(root.querySelector('code')?.textContent).toBe('ctrl+s');
    expect(root.textContent).not.toContain('**');
  });

  it('a link opens in a new tab, with no handle back to this page', async () => {
    const root = await rendered('See [the changelog](https://github.com/BRF-Tech/filex-sign/releases) and https://example.test/x.');
    const links = Array.from(root.querySelectorAll('a'));
    expect(links.length).toBe(2);
    for (const a of links) {
      expect(a.getAttribute('target')).toBe('_blank');
      expect(a.getAttribute('rel')).toBe('noopener noreferrer');
      expect(a.getAttribute('href')).toMatch(/^https:\/\//);
    }
  });

  for (const [name, html] of Object.entries({ ...VECTORS, ...MARKDOWN_VECTORS })) {
    it(`nothing runs: ${name}`, async () => {
      delete (window as unknown as { __fxPreview?: number }).__fxPreview;
      const root = await rendered(`Release 1.2.0\n\n${html}\n\nAnd **bold** after it.`);
      expect(activeContent(root)).toEqual([]);
      for (const a of Array.from(root.querySelectorAll('a[href]'))) {
        // eslint-disable-next-line no-control-regex
        const href = (a.getAttribute('href') ?? '').replace(/[\u0000- ]/g, '').toLowerCase();
        expect(href, `a link left with ${href}`).not.toMatch(/^(javascript|vbscript|data):/);
      }
      for (const img of Array.from(root.querySelectorAll('img[src]'))) {
        expect((img.getAttribute('src') ?? '').toLowerCase()).not.toMatch(/^(javascript|vbscript):/);
      }
      // The container still holds everything: nothing closed it from inside.
      expect(document.querySelectorAll('[onclick]')).toHaveLength(0);
      expect((window as unknown as { __fxPreview?: number }).__fxPreview).toBeUndefined();
    });
  }
});
