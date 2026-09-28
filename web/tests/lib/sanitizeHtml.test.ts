// @vitest-environment jsdom
//
// ⚠ jsdom, not happy-dom: DOMPurify walks the parsed tree with a NodeIterator,
// and happy-dom's iterator loses its place when a node is removed under it —
// every element after the first removal went unvisited (measured: `<b>` kept
// by the code policy, a whole mutation vector kept by the document policy).
// jsdom is the DOM DOMPurify's own test-suite runs against.
//
// The preview sanitizer itself (packages/core/src/lib/sanitizeHtml.ts): the
// shared vector list, per kind, and the style cleaner.

import { describe, expect, it } from 'vitest';
import { cleanStyle, sanitizeHtml } from '@brftech/filex-core/src/lib/sanitizeHtml';
import { activeContent, VECTORS } from '../fixtures/previewVectors';

function parsed(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  return el;
}

describe('sanitizeHtml — document', () => {
  for (const [name, html] of Object.entries(VECTORS)) {
    it(`${name} comes out inert`, () => {
      const out = sanitizeHtml(html, 'document');
      expect(activeContent(parsed(out))).toEqual([]);
      // Sanitized markup that is parsed and serialised again must stay inert
      // too — the shape a mutation vector relies on.
      expect(activeContent(parsed(parsed(out).innerHTML))).toEqual([]);
    });
  }

  it('keeps what a README uses', () => {
    const out = parsed(
      sanitizeHtml(
        '<h2>T</h2><table><tr><td align="right">1</td></tr></table><pre><code class="language-js">x</code></pre>' +
          '<a href="https://example.test/" target="_blank">l</a><img src="https://example.test/a.png" alt="a" width="10">' +
          '<a href="#section">in page</a><a href="mailto:a@example.test">mail</a><img src="data:image/png;base64,iVBORw0KGgo=" alt="d">',
        'document',
      ),
    );
    expect(out.querySelector('h2')?.textContent).toBe('T');
    expect(out.querySelector('td')?.getAttribute('align')).toBe('right');
    expect(out.querySelector('code')?.className).toBe('language-js');
    const [ext, frag, mail] = Array.from(out.querySelectorAll('a'));
    expect(ext.getAttribute('rel')).toBe('noopener noreferrer');
    expect(frag.getAttribute('href')).toBe('#section');
    expect(mail.getAttribute('href')).toBe('mailto:a@example.test');
    expect(out.querySelectorAll('img')[1].getAttribute('src')).toMatch(/^data:image\/png/);
  });

  it('prefixes ids so a document cannot shadow a page global', () => {
    const out = parsed(sanitizeHtml('<p id="location">x</p>', 'document'));
    expect(out.querySelector('p')?.id).not.toBe('location');
  });
});

describe('sanitizeHtml — code, diagram, math', () => {
  it('code keeps highlight spans and nothing else', () => {
    const out = parsed(sanitizeHtml('<span class="hljs-keyword">const</span><img src=x onerror=1><b>b</b>', 'code'));
    expect(out.querySelector('span.hljs-keyword')).not.toBeNull();
    expect(out.querySelector('img, b')).toBeNull();
  });

  it('a diagram keeps its SVG and style sheet, not what runs', () => {
    const out = parsed(
      sanitizeHtml(
        '<svg xmlns="http://www.w3.org/2000/svg" onload="x"><style>.a{fill:red;background:url(https://example.invalid/)}</style>' +
          '<g class="a"><rect width="1" height="1" marker-end="url(#arrow)"></rect><a href="javascript:x"><text>t</text></a>' +
          '<foreignObject><div>label</div></foreignObject></g><script>x</script></svg>',
        'diagram',
      ),
    );
    expect(out.querySelector('svg')).not.toBeNull();
    expect(out.querySelector('rect')?.getAttribute('marker-end')).toBe('url(#arrow)');
    expect(out.querySelector('style')?.textContent).toContain('fill:red');
    expect(out.querySelector('style')?.textContent).not.toContain('example.invalid');
    expect(out.textContent).toContain('label');
    expect(activeContent(out, ['svg', 'style'])).toEqual([]);
  });

  it('math keeps the formula', () => {
    const out = parsed(sanitizeHtml('<span class="katex"><math><mi>x</mi></math><span style="height:1em">x</span></span>', 'math'));
    expect(out.querySelector('.katex')).not.toBeNull();
    expect((out.querySelector('span[style]') as HTMLElement).style.height).toBe('1em');
  });
});

describe('cleanStyle', () => {
  it('keeps plain declarations and drops ones that could fetch', () => {
    expect(cleanStyle('color: red; background: url(https://example.invalid/)')).toBe('color: red');
    expect(cleanStyle('fill: url(#grad); width: 3px')).toBe('fill: url(#grad); width: 3px');
    expect(cleanStyle('background-image: \\75 rl(x)')).toBe('');
    expect(cleanStyle('width: expression(alert(1))')).toBe('');
    expect(cleanStyle('background: image-set("x.png" 1x)')).toBe('');
  });
});
