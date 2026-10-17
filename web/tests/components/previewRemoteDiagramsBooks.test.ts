// @vitest-environment jsdom
//
// A Mermaid diagram (viewers/MermaidViewer) and an EPUB book
// (viewers/EpubViewer) hold their pictures from other servers the way a
// Markdown preview does (previewRemoteImages.test.ts), each with the same
// gate and dialog (composables/useRemoteImages). Mermaid and epub.js are
// stand-ins here that do what filex sees of the real ones: Mermaid draws in
// the frame it makes and hands the SVG back inside an <iframe>'s data:
// address; epub.js hands each chapter's document to the content hooks before
// it draws it. e2e 276 measures the requests in a browser.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import MermaidViewer from '@brftech/filex-core/src/viewers/MermaidViewer.vue';
import EpubViewer from '@brftech/filex-core/src/viewers/EpubViewer.vue';
import { setRemoteImages } from '@brftech/filex-core/src/lib/remoteImages';
import { answerAccountPrefs } from '../helpers/accountPrefs';
import { teardownDom } from '../helpers/teardown';

answerAccountPrefs();

const FAR = 'https://pictures.example/node.png';
const LABEL = 'https://pictures.example/label.png';

const mermaidSaw: { text: string; sealed: boolean } = { text: '', sealed: false };

// ⚠ Mocked where core finds them: the peers are core's (packages/core's own
// node_modules), and a bare 'mermaid' from this file resolves to nothing.
vi.mock('../../../packages/core/node_modules/mermaid', () => ({
  default: {
    initialize: () => {},
    async render(id: string, text: string, container?: Element) {
      mermaidSaw.text = text;
      const frame = document.createElement('iframe');
      container?.appendChild(frame);
      await new Promise((r) => setTimeout(r, 0));
      mermaidSaw.sealed = !!frame.contentDocument?.head?.querySelector('meta[http-equiv="Content-Security-Policy"]');
      const node = /img: "([^"]+)"/.exec(text)?.[1] ?? '';
      const label = /<img src='([^']+)'/.exec(text)?.[1] ?? '';
      const drawn =
        `<svg xmlns="http://www.w3.org/2000/svg" id="${id}" viewBox="0 0 200 100"><g class="node">` +
        `<image href="${node}" x="0" y="0" width="60" height="60"/></g>` +
        `<foreignObject width="120" height="30"><div xmlns="http://www.w3.org/1999/xhtml"><img src="${label}"/> labelled</div></foreignObject></svg>`;
      const b64 = btoa(String.fromCharCode(...new TextEncoder().encode(`<body style="margin:0">${drawn}</body>`)));
      return { svg: `<iframe src="data:text/html;charset=UTF-8;base64,${b64}" sandbox="allow-popups"></iframe>` };
    },
  },
}));

/** What the stand-in epub.js was handed and did. */
const book = {
  hooks: [] as Array<(doc: Document) => void>,
  docs: [] as Document[],
  renderings: 0,
  shownAt: [] as Array<string | undefined>,
};

function chapter(): Document {
  return new DOMParser().parseFromString(
    '<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>c</title></head>' +
      `<body><h1>Chapter one</h1><img src="${FAR}"/><img src="images/own.png"/></body></html>`,
    'application/xhtml+xml',
  );
}

vi.mock('../../../packages/core/node_modules/epubjs', () => ({
  default: () => ({
    spine: { hooks: { content: { register: (fn: (doc: Document) => void) => book.hooks.push(fn) } } },
    loaded: { navigation: Promise.resolve({ toc: [] }) },
    destroy: () => {},
    renderTo: () => {
      book.renderings++;
      return {
        themes: { fontSize: () => {} },
        destroy: () => {},
        location: { start: { cfi: 'epubcfi(/6/2!/4/1:0)' } },
        async display(at?: string) {
          book.shownAt.push(at);
          // epub.js keeps a chapter's document once loaded: the hooks see it once.
          if (!book.docs.length) {
            const doc = chapter();
            for (const h of book.hooks) h(doc);
            book.docs.push(doc);
          }
        },
      };
    },
  }),
}));

afterEach(async () => {
  await teardownDom();
  vi.unstubAllGlobals();
  book.hooks = [];
  book.docs = [];
  book.renderings = 0;
  book.shownAt = [];
});

const dialog = (): HTMLElement | null => document.querySelector('[data-testid="remote-image-dialog"]');
const button = (id: string): HTMLButtonElement | null => document.querySelector(`[data-testid="${id}"]`);

async function diagram(): Promise<HTMLElement> {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(`flowchart LR\n  A@{ img: "${FAR}", w: 60, h: 60 }\n  B["<img src='${LABEL}'> labelled"]\n  A --> B`)));
  const w = mount(MermaidViewer, { attachTo: document.body, props: { url: '/flow.mmd', ext: 'mmd', locale: 'en' } });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(w.find('.filex-viewer-mermaid__surface svg').exists()).toBe(true);
    },
    { timeout: 5000 },
  );
  return w.find('.filex-viewer-mermaid__surface').element as HTMLElement;
}

describe('a .mmd diagram holds its pictures from elsewhere', () => {
  it('Mermaid never sees the image node\'s address and draws in a sealed frame; the diagram shows placeholders', async () => {
    const surface = await diagram();
    expect(mermaidSaw.text).not.toContain(FAR);
    expect(mermaidSaw.sealed).toBe(true);
    expect(surface.querySelector('image')).toBeNull();
    expect(surface.querySelector(`g.fe-remote-svgimg[data-remote-image="${FAR}"]`)).not.toBeNull();
    expect(surface.querySelector(`.fe-remote-img[data-remote-image="${LABEL}"]`)).not.toBeNull();
    expect(Array.from(surface.querySelectorAll('img')).map((i) => i.getAttribute('src'))).toEqual([]);
  });

  it('a click on the image node asks; Load image draws it as a picture with no referrer, in its box', async () => {
    const surface = await diagram();
    surface.querySelector(`[data-remote-image="${FAR}"]`)!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(document.querySelector('[data-testid="remote-image-host"]')?.textContent).toBe('pictures.example');
    button('remote-image-load')!.click();
    await flushPromises();
    const img = surface.querySelector(`foreignObject img[src="${FAR}"]`)!;
    expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(img.closest('foreignObject')?.getAttribute('width')).toBe('60');
    expect(surface.querySelector(`[data-remote-image="${LABEL}"]`)).not.toBeNull();
  });

  it('with the setting on, both are drawn without asking, without a referrer', async () => {
    setRemoteImages(true);
    const surface = await diagram();
    expect(surface.querySelector('[data-remote-image]')).toBeNull();
    const srcs = Array.from(surface.querySelectorAll('img')).map((i) => [i.getAttribute('src'), i.getAttribute('referrerpolicy')]);
    expect(srcs).toEqual(expect.arrayContaining([[FAR, 'no-referrer'], [LABEL, 'no-referrer']]));
    expect(dialog()).toBeNull();
  });
});

async function reader() {
  const w = mount(EpubViewer, { attachTo: document.body, props: { url: '/book.epub', ext: 'epub', locale: 'en' } });
  await vi.waitFor(
    async () => {
      await flushPromises();
      expect(book.docs.length).toBe(1);
    },
    { timeout: 5000 },
  );
  await flushPromises();
  return w;
}

describe('an EPUB chapter is drawn without its pictures from elsewhere until the person allows them', () => {
  it('the chapter is sealed before it is drawn; the reader says so; Load all draws it again where it was, under the allowed policy', async () => {
    const w = await reader();
    const head = book.docs[0].getElementsByTagName('head')[0];
    expect(head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy');
    expect(head.querySelector('meta[name="referrer"]')?.getAttribute('content')).toBe('no-referrer');
    expect(w.find('[data-testid="epub-remote-notice"]').exists()).toBe(true);

    await w.find('[data-testid="epub-remote-load"]').trigger('click');
    await flushPromises();
    expect(document.querySelector('[data-testid="remote-image-host"]')?.textContent).toBe('pictures.example');
    // One frame, one answer: the whole book or nothing.
    expect(button('remote-image-load')).toBeNull();
    button('remote-image-load-all')!.click();
    await vi.waitFor(async () => {
      await flushPromises();
      expect(book.renderings).toBe(2);
    });
    expect(head.querySelector('meta[http-equiv]')?.getAttribute('content')).toMatch(/img-src \* /);
    expect(head.querySelector('meta[name="referrer"]')).not.toBeNull();
    expect(book.shownAt).toEqual([undefined, 'epubcfi(/6/2!/4/1:0)']);
    expect(w.find('[data-testid="epub-remote-notice"]').exists()).toBe(false);
  });

  it('with the setting on, a chapter is drawn with them from the start: the allowed policy, no notice', async () => {
    setRemoteImages(true);
    const w = await reader();
    const head = book.docs[0].getElementsByTagName('head')[0];
    expect(head.querySelector('meta[http-equiv]')?.getAttribute('content')).toMatch(/img-src \* /);
    expect(head.querySelector('meta[name="referrer"]')).not.toBeNull();
    expect(w.find('[data-testid="epub-remote-notice"]').exists()).toBe(false);
    expect(book.renderings).toBe(1);
  });
});
