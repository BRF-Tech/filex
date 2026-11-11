// @vitest-environment jsdom
//
// The `slot` kind (lib/sanitizeHtml): an administrator's text on a public link
// page (the server's templates, slot.*), drawn by PublicShell. The server
// sanitized it already; this is the second wall, and it must hold on its own:
// a slot that reached the page with a script, a handler or a script address
// in it runs nothing. Its pictures are drawn - the administrator chose them,
// like the logo - without a referrer.

import { describe, expect, it } from 'vitest';
import { sanitizeHtml } from '@brftech/filex-core/src/lib/sanitizeHtml';

function parsed(html: string): HTMLElement {
  const el = document.createElement('div');
  el.innerHTML = html;
  return el;
}

describe('sanitizeHtml, the slot kind', () => {
  it('keeps the administrator\'s text and markup', () => {
    const out = parsed(
      sanitizeHtml('<p class="note">Files from <strong>Northwind</strong>. <a href="mailto:studio@example.com">Write to us</a>.</p>', 'slot'),
    );
    expect(out.querySelector('p.note strong')?.textContent).toBe('Northwind');
    expect(out.querySelector('a')?.getAttribute('href')).toBe('mailto:studio@example.com');
  });

  it('runs nothing that reached it', () => {
    const out = parsed(
      sanitizeHtml(
        '<p>hi</p><script>alert(1)</script><img src="x" onerror="alert(2)"><a href="javascript:alert(3)">x</a>' +
          '<iframe src="https://evil.example"></iframe><form action="https://evil.example"><input></form>',
        'slot',
      ),
    );
    expect(out.querySelector('script, iframe, form')).toBeNull();
    expect(out.querySelector('[onerror]')).toBeNull();
    expect(out.querySelector('a')?.getAttribute('href') ?? '').not.toMatch(/^javascript:/i);
  });

  it('draws a picture from elsewhere, without a referrer, where a document holds it back', () => {
    const remote = '<img src="https://cdn.example.com/banner.png" alt="Spring campaign">';
    const img = parsed(sanitizeHtml(remote, 'slot')).querySelector('img');
    expect(img?.getAttribute('src')).toBe('https://cdn.example.com/banner.png');
    expect(img?.getAttribute('referrerpolicy')).toBe('no-referrer');
    // The control: the same markup as a file's preview is held behind a placeholder.
    expect(parsed(sanitizeHtml(remote, 'document')).querySelector('img[src^="https://cdn.example.com"]')).toBeNull();
  });
});
