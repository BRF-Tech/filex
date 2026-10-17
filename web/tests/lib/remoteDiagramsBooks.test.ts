// @vitest-environment jsdom
//
// A Mermaid diagram and an EPUB chapter ask no other server for anything
// until the person says so (0.57), as a file's Markdown preview does
// (sanitizeRemoteImages.test.ts). jsdom for DOMPurify (sanitizeHtml.test.ts
// says why) and for the frame Mermaid lays a diagram out in.
//
// ⚠ Why: Mermaid draws a diagram into the page to lay it out, and an image
// node (`@{ img: … }`), a picture in an HTML label or a url() in a style was
// fetched right there; epub.js draws a chapter in a frame that loaded every
// picture, style sheet and font it named. Each request tells a server the
// file was opened, and from which IP address.

import { describe, expect, it } from 'vitest';
import { HELD_IMAGE_FRAGMENT, sanitizeHtml } from '@brftech/filex-core/src/lib/sanitizeHtml';
import {
  holdShapeImages,
  mermaidConfig,
  renderMermaid,
  RENDER_CSP,
  standInImage,
  svgOfSandboxed,
  type MermaidApi,
} from '@brftech/filex-core/src/lib/mermaidRender';
import {
  allowChapter,
  EPUB_ALLOWED_CSP,
  EPUB_HELD_CSP,
  remoteReferencesOf,
  sealChapter,
  withoutRemoteUrls,
} from '@brftech/filex-core/src/lib/epubRemote';

const FAR = 'https://pictures.example/node.png?id=7';

function parsed(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  return el;
}

const svg = (body: string) =>
  `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 200 100">${body}</svg>`;

describe('the diagram policy holds a picture from another server', () => {
  it('an SVG <image> (href or xlink:href) from elsewhere is a placeholder that keeps its box', () => {
    const el = parsed(
      sanitizeHtml(
        svg(
          `<image href="${FAR}" x="10" y="5" width="60" height="40" transform="translate(3,4)"/>` +
            `<image xlink:href="https://pictures.example/b.png" width="20" height="20"/>`,
        ),
        'diagram',
        { remoteImages: { label: 'Uzak görsel' } },
      ),
    );
    expect(el.querySelector('image')).toBeNull();
    const [a, b] = Array.from(el.querySelectorAll('g.fe-remote-svgimg'));
    expect(a.getAttribute('data-remote-image')).toBe(FAR);
    expect(a.getAttribute('data-remote-host')).toBe('pictures.example');
    expect(a.getAttribute('role')).toBe('button');
    expect(a.getAttribute('transform')).toBe('translate(3,4)');
    expect([a.getAttribute('data-remote-x'), a.getAttribute('data-remote-width'), a.getAttribute('data-remote-height')]).toEqual(['10', '60', '40']);
    expect(a.querySelector('rect')?.getAttribute('width')).toBe('60');
    expect(a.querySelector('text')?.textContent).toBe('Uzak görsel');
    expect(b.getAttribute('data-remote-image')).toBe('https://pictures.example/b.png');
  });

  it("a Mermaid image node's stand-in is held for the address it names; one that names none cannot be loaded", () => {
    const el = parsed(
      sanitizeHtml(svg(`<image href="${standInImage(FAR, 60, 60)}" width="60" height="60"/><image href="${standInImage('')}"/>`), 'diagram'),
    );
    const [named, none] = Array.from(el.querySelectorAll('g.fe-remote-svgimg'));
    expect(named.getAttribute('data-remote-image')).toBe(FAR);
    expect(none.hasAttribute('data-remote-image')).toBe(false);
    expect(none.hasAttribute('role')).toBe(false);
    expect(standInImage(FAR)).toContain(HELD_IMAGE_FRAGMENT + encodeURIComponent(FAR));
  });

  it('a picture the person allowed is an HTML picture with no referrer, in the same box', () => {
    const el = parsed(
      sanitizeHtml(svg(`<image href="${standInImage(FAR, 60, 60)}" x="1" y="2" width="60" height="60"/>`), 'diagram', {
        remoteImages: { load: (s) => s === FAR },
      }),
    );
    const fo = el.querySelector('foreignObject')!;
    expect([fo.getAttribute('x'), fo.getAttribute('y'), fo.getAttribute('width')]).toEqual(['1', '2', '60']);
    const img = fo.querySelector('img')!;
    expect(img.getAttribute('src')).toBe(FAR);
    expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
  });

  it("the diagram's own pictures stay; every other reference out of it is taken off; a label's picture waits as a file's does", () => {
    const own = 'data:image/png;base64,iVBORw0KGgo=';
    const el = parsed(
      sanitizeHtml(
        svg(
          `<image href="${own}" width="10" height="10"/>` +
            '<defs><linearGradient id="g"/><pattern id="p" href="https://pictures.example/p.svg#x"/></defs>' +
            '<use href="https://pictures.example/sprite.svg#icon"/><use href="#g"/>' +
            '<a href="https://example.test/docs"><text>docs</text></a>' +
            `<foreignObject width="100" height="30"><div><img src="${FAR}" alt="logo"> label<video src="https://pictures.example/v.mp4"></video></div></foreignObject>`,
        ),
        'diagram',
      ),
    );
    expect(el.querySelector('image')?.getAttribute('href')).toBe(own);
    expect(el.querySelector('pattern')?.hasAttribute('href')).toBe(false);
    // (DOMPurify drops <use> of itself; whatever is left points into the diagram.)
    const refs = Array.from(el.querySelectorAll('[href], [*|href]')).map((n) => n.getAttribute('href') ?? n.getAttribute('xlink:href'));
    expect(refs.filter((r) => r && !r.startsWith('#') && !r.startsWith('data:') && r !== 'https://example.test/docs')).toEqual([]);
    expect(el.querySelector('a')?.getAttribute('href')).toBe('https://example.test/docs');
    expect(el.querySelector('foreignObject img')).toBeNull();
    expect(el.querySelector('foreignObject .fe-remote-img')?.getAttribute('data-remote-image')).toBe(FAR);
    expect(el.querySelector('video')).toBeNull();
  });
});

describe('a Mermaid image node never reaches Mermaid with an address from elsewhere', () => {
  it('its img: becomes a stand-in naming the address; filex\'s own and data: pictures stay', async () => {
    const src = [
      'flowchart LR',
      `  A@{ img: "${FAR}", label: "Logo", w: 60, h: 60 }`,
      "  B@{ label: 'two', img: 'https://pictures.example/two.png' }",
      '  C@{ img: /api/files/read?path=main%3A%2F%2Fa.png }',
      '  D@{ img: "data:image/png;base64,iVBORw0KGgo=" }',
      '  E@{',
      '    img: "//cdn.example/e.png"',
      '  }',
      '  A --> B --> C --> D --> E',
    ].join('\n');
    const out = await holdShapeImages(src, {});
    expect(out).not.toContain(FAR);
    expect(out).not.toContain('https://pictures.example/two.png');
    expect(out).not.toContain('//cdn.example/e.png');
    expect(out).toContain(encodeURIComponent(FAR));
    expect(out).toContain(encodeURIComponent('https://pictures.example/two.png'));
    expect(out).toContain(encodeURIComponent(`${location.protocol}//cdn.example/e.png`));
    expect(out).toContain('img: /api/files/read?path=main%3A%2F%2Fa.png');
    expect(out).toContain('img: "data:image/png;base64,iVBORw0KGgo="');
    expect(out).toContain('label: "Logo", w: 60, h: 60');
    expect(out).toContain('A --> B --> C --> D --> E');
  });

  it('an address written some other way (an escape, an anchor) is never loaded', async () => {
    const out = await holdShapeImages(
      ['flowchart LR', '  A@{ img: "\\x68ttps://pictures.example/a.png" }', '  B@{ img: *pic }'].join('\n'),
      { load: () => true },
    );
    expect(out).not.toContain('pictures.example');
    expect(out).not.toContain('*pic');
    expect((out.match(new RegExp(HELD_IMAGE_FRAGMENT + '"', 'g')) ?? []).length).toBe(2);
  });

  it('a diagram cannot change its sandbox, its style sheet or its label sanitizer', () => {
    const c = mermaidConfig('dark');
    expect(c.securityLevel).toBe('sandbox');
    expect(c.theme).toBe('dark');
    expect(c.secure).toEqual(expect.arrayContaining(['securityLevel', 'themeCSS', 'dompurifyConfig']));
  });
});

/** A Mermaid that does what the real one does in its `sandbox` level, as far as filex sees it. */
function sandboxMermaid(opts: { frame: boolean }): MermaidApi & { sawPolicy: string | null; sawText: string } {
  const fake = {
    sawPolicy: null as string | null,
    sawText: '',
    initialize: () => {},
    async render(id: string, text: string, container?: Element) {
      fake.sawText = text;
      let frame: HTMLIFrameElement | null = null;
      if (opts.frame && container) {
        frame = document.createElement('iframe');
        frame.id = `i${id}`;
        container.appendChild(frame);
      }
      // Mermaid awaits its parser here, before it draws.
      await new Promise((r) => setTimeout(r, 0));
      fake.sawPolicy = frame?.contentDocument?.head?.firstElementChild?.getAttribute('content') ?? null;
      const drawn = `<svg id="${id}"><text>é</text></svg>`;
      const b64 = btoa(String.fromCharCode(...new TextEncoder().encode(`<body style="margin:0">${drawn}</body>`)));
      return { svg: `<iframe style="width:100%" src="data:text/html;charset=UTF-8;base64,${b64}" sandbox="allow-popups"></iframe>` };
    },
  };
  return fake;
}

describe('a diagram is laid out in a frame that fetches nothing', () => {
  it('the frame Mermaid makes is sealed before it draws, and the SVG comes back out of it', async () => {
    const m = sandboxMermaid({ frame: true });
    const out = await renderMermaid(m, 'd1', `flowchart LR\n  A@{ img: "${FAR}" }`, {});
    expect(m.sawPolicy).toBe(RENDER_CSP);
    expect(m.sawText).not.toContain(FAR);
    expect(out).toBe('<svg id="d1"><text>é</text></svg>');
    expect(document.querySelector('iframe')).toBeNull();
    expect(RENDER_CSP).toMatch(/default-src 'none'/);
    expect(RENDER_CSP).not.toMatch(/https?:|\*/);
  });

  it('a diagram whose frame could not be sealed is not drawn at all', async () => {
    await expect(renderMermaid(sandboxMermaid({ frame: false }), 'd2', 'flowchart LR\n  A --> B', {})).rejects.toThrow(/sealed/);
  });

  it('an answer that is no sandbox frame is handed back as it is', () => {
    expect(svgOfSandboxed('<svg></svg>')).toBe('<svg></svg>');
  });
});

describe('an EPUB chapter is sealed before epub.js draws it', () => {
  const chapter = () =>
    new DOMParser().parseFromString(
      '<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>c</title>' +
        '<link rel="stylesheet" href="https://pictures.example/book.css"/><link rel="stylesheet" href="style.css"/>' +
        '<style>@import "https://pictures.example/more.css"; @font-face { src: url(https://pictures.example/f.woff) }</style></head>' +
        `<body><img src="${FAR}"/><img src="images/own.png"/><img srcset="https://pictures.example/2x.png 2x"/>` +
        '<p style="background: url(\'//pictures.example/bg.png\')">x</p>' +
        '<svg xmlns="http://www.w3.org/2000/svg"><image href="https://pictures.example/s.png"/></svg>' +
        '<a href="https://example.test/read-more">read more</a></body></html>',
      'application/xhtml+xml',
    );

  it('names every address from elsewhere it would load, and no link a reader follows', () => {
    expect(remoteReferencesOf(chapter()).sort()).toEqual(
      [
        'https://pictures.example/book.css',
        'https://pictures.example/more.css',
        'https://pictures.example/f.woff',
        FAR,
        'https://pictures.example/2x.png',
        '//pictures.example/bg.png',
        'https://pictures.example/s.png',
      ].sort(),
    );
  });

  it('its head starts with a policy that loads only the book\'s own files, and no referrer', () => {
    const doc = chapter();
    const csp = sealChapter(doc, false)!;
    const head = doc.getElementsByTagName('head')[0];
    expect(head.firstElementChild).toBe(csp);
    expect(csp.getAttribute('http-equiv')).toBe('Content-Security-Policy');
    expect(csp.getAttribute('content')).toBe(EPUB_HELD_CSP);
    expect(head.children[1].getAttribute('name')).toBe('referrer');
    expect(head.children[1].getAttribute('content')).toBe('no-referrer');
    expect(EPUB_HELD_CSP).not.toMatch(/https?:|\*/);
    expect(new XMLSerializer().serializeToString(doc)).toContain('Content-Security-Policy');
  });

  it('allowed, pictures, style sheets and media from elsewhere load, fonts do not; every fetching element sends no referrer', () => {
    const doc = chapter();
    const csp = sealChapter(doc, true)!;
    expect(csp.getAttribute('content')).toBe(EPUB_ALLOWED_CSP);
    expect(EPUB_ALLOWED_CSP).toMatch(/img-src \* /);
    expect(EPUB_ALLOWED_CSP).toMatch(/font-src 'self' data: blob:;/);
    expect(EPUB_ALLOWED_CSP).toMatch(/default-src 'none'/);
    for (const el of Array.from(doc.querySelectorAll('img, link'))) expect(el.getAttribute('referrerpolicy')).toBe('no-referrer');
    const held = sealChapter(chapter(), false)!;
    allowChapter(held);
    expect(held.getAttribute('content')).toBe(EPUB_ALLOWED_CSP);
  });

  it("a url() or @import from elsewhere in the chapter's own styles is taken out, held or allowed; the book's own stay", () => {
    for (const allowed of [false, true]) {
      const doc = chapter();
      sealChapter(doc, allowed);
      const css = Array.from(doc.getElementsByTagName('style')).map((s) => s.textContent).join('');
      expect(css).not.toContain('pictures.example');
      expect(doc.querySelector('p')?.getAttribute('style')).not.toContain('pictures.example');
    }
    expect(withoutRemoteUrls("a { background: url('images/own.png') } b { background: url(https://pictures.example/x.png) }")).toBe(
      "a { background: url('images/own.png') } b { background: none }",
    );
    expect(withoutRemoteUrls('@import url("https://pictures.example/a.css"); @import "own.css"; p{}')).toBe(' @import "own.css"; p{}');
  });
});
