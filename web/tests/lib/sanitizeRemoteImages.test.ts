// @vitest-environment jsdom
//
// A file's preview loads no picture from another server by itself
// (lib/sanitizeHtml, the `document` policy's RemoteImages). jsdom for the
// reason sanitizeHtml.test.ts gives: DOMPurify's own test-suite DOM.
//
// ⚠ Why: drawing a picture is a request, and the server it is on learns that
// the file was opened, when, and from which IP address. A shared Markdown file
// with a one-pixel picture is a read receipt its reader never agreed to send.
// So every picture from elsewhere becomes a placeholder, made in the inert
// document DOMPurify parsed the markup into, and the person decides.

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  localImageSource,
  remoteImageUrl,
  sanitizeHtml,
  srcsetCandidates,
} from '@brftech/filex-core/src/lib/sanitizeHtml';
import { markdownToSafeHtml } from '@brftech/filex-core/src/lib/markdownHtml';

const PIXEL = 'https://tracker.example/p.png?file=report';
const DATA = 'data:image/png;base64,iVBORw0KGgo=';

function parsed(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  return el;
}

/** Every address in `el` the browser would fetch from elsewhere, drawing it. */
function fetchesFromElsewhere(el: HTMLElement): string[] {
  const out: string[] = [];
  for (const n of Array.from(el.querySelectorAll('*'))) {
    const tag = n.tagName.toLowerCase();
    const src = n.getAttribute('src');
    if (src && (tag === 'img' || tag === 'source') && !localImageSource(src)) out.push(`${tag} src=${src}`);
    for (const c of srcsetCandidates(n.getAttribute('srcset') ?? '')) {
      if (!localImageSource(c.url)) out.push(`${tag} srcset=${c.url}`);
    }
    for (const attr of ['poster', 'background', 'data']) {
      const v = n.getAttribute(attr);
      if (v) out.push(`${tag} ${attr}=${v}`);
    }
    if (tag === 'link' || tag === 'object' || tag === 'embed' || tag === 'video' || tag === 'audio') out.push(tag);
    const style = n.getAttribute('style');
    if (style && /url\s*\(/i.test(style)) out.push(`${tag} style=${style}`);
  }
  return out;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('a file preview holds back every picture from another server', () => {
  it('a remote picture in Markdown is a placeholder: icon, alt text, the label, the server', async () => {
    const html = await markdownToSafeHtml(`Read me.\n\n![Quarterly chart](${PIXEL})`, 'document', {
      remoteImages: { label: 'Uzak görsel' },
    });
    const el = parsed(html!);
    expect(el.querySelector('img')).toBeNull();
    expect(fetchesFromElsewhere(el)).toEqual([]);
    const ph = el.querySelector('.fe-remote-img')!;
    expect(ph.getAttribute('data-remote-image')).toBe(PIXEL);
    expect(ph.getAttribute('data-remote-host')).toBe('tracker.example');
    expect(ph.getAttribute('role')).toBe('button');
    expect(ph.getAttribute('tabindex')).toBe('0');
    expect(ph.getAttribute('title')).toBe(PIXEL);
    expect(ph.querySelector('.fe-remote-img__icon')?.getAttribute('aria-hidden')).toBe('true');
    expect(ph.querySelector('.fe-remote-img__alt')?.textContent).toBe('Quarterly chart');
    expect(ph.querySelector('.fe-remote-img__label')?.textContent).toBe('Uzak görsel');
    expect(ph.querySelector('.fe-remote-img__host')?.textContent).toBe('tracker.example');
  });

  it('no other way to fetch survives: srcset, <picture>/<source>, a style url(), background, poster, <link>, <object>, a protocol-relative address', () => {
    const el = parsed(
      sanitizeHtml(
        `<img src="${PIXEL}" srcset="${PIXEL} 2x" alt="a">` +
          `<img src="${DATA}" srcset="${PIXEL} 2x, ${DATA} 3x" alt="d">` +
          `<img srcset="${PIXEL} 1x" alt="only-srcset">` +
          `<picture><source srcset="${PIXEL}"><source srcset="/api/x.png 2x"><img src="${PIXEL}" alt="p"></picture>` +
          `<p style="background-image: url(${PIXEL}); color: red">x</p>` +
          `<table background="${PIXEL}"><tr><td background="${PIXEL}">t</td></tr></table>` +
          '<img src="//cdn.example/logo.png" alt="rel">' +
          `<video poster="${PIXEL}"></video><link rel="prefetch" href="${PIXEL}">` +
          `<object data="${PIXEL}"></object><embed src="${PIXEL}">`,
        'document',
      ),
    );
    expect(fetchesFromElsewhere(el)).toEqual([]);
    // The data: picture is drawn, with its own candidate kept.
    const imgs = Array.from(el.querySelectorAll('img'));
    expect(imgs.map((i) => i.getAttribute('src'))).toEqual([DATA]);
    expect(imgs[0].getAttribute('srcset')).toBe(`${DATA} 3x`);
    // The <picture> keeps filex's own candidate and holds the rest.
    const picture = el.querySelector('picture')!;
    expect(Array.from(picture.querySelectorAll('source')).map((s) => s.getAttribute('srcset'))).toEqual(['/api/x.png 2x']);
    expect(picture.querySelector('.fe-remote-img')?.getAttribute('data-remote-image')).toBe(PIXEL);
    // A picture named only by its srcset, and a protocol-relative one, are held too.
    const held = Array.from(el.querySelectorAll('.fe-remote-img')).map((p) => p.getAttribute('data-remote-alt'));
    expect(held).toEqual(['a', 'only-srcset', 'p', 'rel']);
    expect(el.querySelector('[data-remote-alt="rel"]')?.getAttribute('data-remote-image')).toBe(
      `${location.protocol}//cdn.example/logo.png`,
    );
    expect((el.querySelector('p') as HTMLElement).style.color).toBe('red');
  });

  it("filex's own pictures, data: images and blob:s of this page are drawn as they are", () => {
    const own = `${location.origin}/api/files/manager?action=download&path=main%3A%2F%2Fa.png`;
    const el = parsed(
      sanitizeHtml(
        `<img src="${DATA}" alt="d"><img src="${own}" alt="o"><img src="images/r.png" alt="r"><img src="/api/x.png" alt="root">`,
        'document',
      ),
    );
    expect(Array.from(el.querySelectorAll('img')).map((i) => i.getAttribute('alt'))).toEqual(['d', 'o', 'r', 'root']);
    expect(el.querySelector('.fe-remote-img')).toBeNull();
    expect(el.querySelector('img[referrerpolicy]')).toBeNull();
    expect(localImageSource(`blob:${location.origin}/7a0c-11`)).toBe(true);
    expect(localImageSource('blob:https://tracker.example/7a0c-11')).toBe(false);
  });

  it('a picture the person allowed is drawn, without a referrer; the others still wait', () => {
    const other = 'https://cdn.example/b.png';
    const el = parsed(
      sanitizeHtml(`<img src="${PIXEL}" alt="a" width="72"><img src="${other}" alt="b">`, 'document', {
        remoteImages: { load: (src) => src === PIXEL },
      }),
    );
    const img = el.querySelector('img')!;
    expect(img.getAttribute('src')).toBe(PIXEL);
    expect(img.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(img.getAttribute('width')).toBe('72');
    expect(el.querySelector('.fe-remote-img')?.getAttribute('data-remote-image')).toBe(other);
  });

  it('with the setting on every picture is drawn, its srcset and <source> kept, none with a referrer', () => {
    const el = parsed(
      sanitizeHtml(
        `<img srcset="${PIXEL} 1x" alt="s"><picture><source srcset="${PIXEL}"><img src="${DATA}" alt="p"></picture>`,
        'document',
        { remoteImages: { load: () => true } },
      ),
    );
    expect(el.querySelector('.fe-remote-img')).toBeNull();
    const [s, p] = Array.from(el.querySelectorAll('img'));
    expect(s.getAttribute('srcset')).toBe(`${PIXEL} 1x`);
    expect(s.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(el.querySelector('source')?.getAttribute('srcset')).toBe(PIXEL);
    expect(p.getAttribute('referrerpolicy')).toBe('no-referrer');
  });

  it("a file cannot write a placeholder of its own: filex's attributes are taken off what it brought", () => {
    const el = parsed(
      sanitizeHtml(
        '<span class="fe-remote-img" data-remote-image="https://tracker.example/x.png" data-remote-host="trusted.example">trusted.example</span>',
        'document',
      ),
    );
    const span = el.querySelector('span')!;
    expect(span.hasAttribute('data-remote-image')).toBe(false);
    expect(span.hasAttribute('data-remote-host')).toBe(false);
  });

  it('an address that is not http(s) is held without a way to load it', () => {
    const el = parsed(sanitizeHtml('<img src="cid:part1@example" alt="mail part"><img src="ftp://files.example/a.png" alt="f">', 'document'));
    const held = Array.from(el.querySelectorAll('.fe-remote-img'));
    expect(held).toHaveLength(2);
    expect(held[0].hasAttribute('data-remote-image')).toBe(false);
    expect(held[0].hasAttribute('role')).toBe(false);
    expect(held[0].textContent).toContain('mail part');
    expect(held[1].hasAttribute('data-remote-image')).toBe(false);
    expect(remoteImageUrl('ftp://files.example/a.png')).toBeNull();
  });

  it('nothing is fetched on the way: no element of this page is given an address while the markup is reshaped', () => {
    const live: string[] = [];
    const real = Element.prototype.setAttribute;
    vi.spyOn(Element.prototype, 'setAttribute').mockImplementation(function (this: Element, name: string, value: string) {
      if (this.ownerDocument === document && (name === 'src' || name === 'srcset')) live.push(`${name}=${value}`);
      return real.call(this, name, value);
    });
    const out = sanitizeHtml(
      `<img src="${PIXEL}"><img srcset="${PIXEL} 2x"><picture><source srcset="${PIXEL}"><img src="${PIXEL}"></picture>`,
      'document',
      { remoteImages: { load: (src) => src.includes('never') } },
    );
    expect(live).toEqual([]);
    expect(fetchesFromElsewhere(parsed(out))).toEqual([]);
  });

  it('srcset is split as the parser splits it: a data: address keeps its comma', () => {
    expect(srcsetCandidates(`${DATA} 2x, /a.png 640w,/b.png`)).toEqual([
      { url: DATA, descriptor: '2x' },
      { url: '/a.png', descriptor: '640w' },
      { url: '/b.png', descriptor: '' },
    ]);
    expect(srcsetCandidates('/a.png,, /b.png 2x')).toEqual([
      { url: '/a.png', descriptor: '' },
      { url: '/b.png', descriptor: '2x' },
    ]);
  });
});
