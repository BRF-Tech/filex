/**
 * sanitizeHtml — the ONE door every piece of markup a preview draws passes
 * through before it reaches the page.
 *
 * A preview turns somebody's file into HTML inside filex's own page: a
 * Markdown document (markdown-it with inline HTML, the GitHub/GitLab
 * contract), a notebook's cells and its `text/html` outputs, highlighted code,
 * a Mermaid diagram, a KaTeX formula. That markup lands in `v-html` or
 * `innerHTML` of a page that holds the person's session, so it goes through
 * DOMPurify here, with an allow-list narrow enough to keep a README looking
 * like a README (headings, tables, code blocks, pictures, links, <details>)
 * and nothing that runs or submits: no script, no event attribute, no
 * `javascript:`/`data:` link, no form, frame, object or embed, no `url()` in a
 * style. The explorer on the web, the desktop app and every embed (the web
 * component, the React adapter) draw previews from this package, so they all
 * sanitize with this one function — never a local copy.
 *
 * DOMPurify: https://github.com/cure53/DOMPurify, dual-licensed Apache-2.0 OR
 * MPL-2.0, bundled unmodified in @brftech/filex-core.
 *
 * ⚠ Kinds, not options: a caller says WHAT it is drawing and this file owns
 * the policy for it. A new preview surface picks one of the kinds; it does
 * not pass DOMPurify flags of its own.
 */
import DOMPurify, { type Config, type DOMPurify as Purifier } from 'dompurify';

/**
 * What is being drawn:
 *  - `document`: a Markdown rendering or a notebook's HTML output — the
 *    narrow document allow-list below;
 *  - `code`: syntax-highlighted source (spans with classes, nothing else);
 *  - `diagram`: a Mermaid SVG (SVG with its own style sheet and HTML labels);
 *  - `math`: a KaTeX rendering (HTML spans plus the MathML twin for screen
 *    readers).
 */
export type SanitizeKind = 'document' | 'code' | 'diagram' | 'math';

const DOCUMENT_TAGS = [
  'a', 'abbr', 'b', 'bdi', 'bdo', 'blockquote', 'br', 'caption', 'cite', 'code', 'col', 'colgroup',
  'dd', 'del', 'details', 'dfn', 'div', 'dl', 'dt', 'em', 'figcaption', 'figure', 'h1', 'h2', 'h3',
  'h4', 'h5', 'h6', 'hr', 'i', 'img', 'ins', 'kbd', 'li', 'mark', 'ol', 'p', 'picture', 'pre', 'q',
  'rp', 'rt', 'ruby', 's', 'samp', 'small', 'source', 'span', 'strike', 'strong', 'sub', 'summary',
  'sup', 'table', 'tbody', 'td', 'tfoot', 'th', 'thead', 'time', 'tr', 'tt', 'u', 'ul', 'var', 'wbr',
];

const DOCUMENT_ATTRS = [
  'abbr', 'align', 'alt', 'cite', 'class', 'colspan', 'datetime', 'dir', 'height', 'href', 'id',
  'lang', 'loading', 'media', 'open', 'rel', 'reversed', 'rowspan', 'scope', 'span', 'src', 'srcset',
  'start', 'style', 'target', 'title', 'valign', 'width',
];

/** Never, in any kind — whatever a profile would otherwise allow. */
const FORBID_TAGS = [
  'script', 'iframe', 'frame', 'frameset', 'object', 'embed', 'applet', 'form',
  'input', 'button', 'select', 'option', 'textarea', 'base', 'meta', 'link', 'noscript',
  'template', 'portal', 'dialog',
];
const FORBID_ATTR = ['action', 'formaction', 'srcdoc', 'ping', 'background', 'poster'];

const POLICY: Record<SanitizeKind, Config> = {
  document: {
    ALLOWED_TAGS: DOCUMENT_TAGS,
    ALLOWED_ATTR: DOCUMENT_ATTRS,
    // Ids a document brings are prefixed (`user-content-…`) so they can never
    // shadow a global the page reads (DOM clobbering).
    SANITIZE_NAMED_PROPS: true,
  },
  code: {
    ALLOWED_TAGS: ['span', 'br'],
    ALLOWED_ATTR: ['class'],
  },
  diagram: {
    USE_PROFILES: { svg: true, svgFilters: true, html: true },
    // What Mermaid itself keeps when it sanitizes its output: HTML labels
    // live in <foreignObject> (an HTML integration point for them), and text
    // anchoring needs dominant-baseline.
    ADD_TAGS: ['foreignobject'],
    ADD_ATTR: ['dominant-baseline'],
    HTML_INTEGRATION_POINTS: { foreignobject: true },
  },
  math: {
    USE_PROFILES: { html: true, mathMl: true },
  },
};

let purifier: Purifier | null = null;

/** A private DOMPurify instance with filex's hooks; null outside a browser. */
function instance(): Purifier | null {
  if (purifier) return purifier;
  if (typeof window === 'undefined') return null;
  const p = DOMPurify(window);
  if (!p.isSupported) return null;
  p.addHook('uponSanitizeAttribute', (_node, data) => {
    if (data.attrName === 'style') {
      const clean = cleanStyle(data.attrValue);
      if (clean === '') data.keepAttr = false;
      else data.attrValue = clean;
    }
  });
  p.addHook('uponSanitizeElement', (node, data) => {
    if (data.tagName === 'style' && node.textContent) {
      node.textContent = cleanStyleSheet(node.textContent);
    }
  });
  p.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName === 'A') {
      const target = node.getAttribute('target');
      if (target && target !== '_blank') node.removeAttribute('target');
      if (node.getAttribute('target') === '_blank') node.setAttribute('rel', 'noopener noreferrer');
    }
  });
  purifier = p;
  return p;
}

/**
 * A declaration list with every declaration that could fetch or run
 * something removed: `url()` other than a same-document `#fragment`,
 * `image-set()`, `expression()`, `@import`, legacy `behavior`/`-moz-binding`.
 * CSS escapes (`\75 rl(`) can spell any of those, so a declaration with a
 * backslash is dropped as well.
 */
export function cleanStyle(style: string): string {
  return style
    .split(';')
    .map((d) => d.trim())
    .filter((d) => d !== '' && !unsafeCss(d))
    .join('; ');
}

function cleanStyleSheet(css: string): string {
  if (/\\/.test(css)) return '';
  return css
    .replace(/@import[^;]*;?/gi, '')
    .replace(/url\s*\(\s*(['"]?)(?!#)[^)]*\)/gi, 'none')
    .replace(/image-set\s*\([^)]*\)/gi, 'none')
    .replace(/expression\s*\(/gi, 'x(');
}

function unsafeCss(declaration: string): boolean {
  if (/\\/.test(declaration)) return true;
  if (/expression\s*\(|@import|behavior\s*:|-moz-binding|image-set\s*\(|javascript:/i.test(declaration)) return true;
  const urls = declaration.match(/url\s*\(\s*(['"]?)([^)'"]*)/gi) ?? [];
  return urls.some((u) => !/url\s*\(\s*['"]?#/i.test(u));
}

function escapeText(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

/**
 * Markup safe to hand to `v-html` / `innerHTML`, for `kind`. Outside a
 * browser (no DOM to parse with) the input comes back as escaped TEXT —
 * never as the markup it was.
 */
export function sanitizeHtml(html: string, kind: SanitizeKind = 'document'): string {
  const p = instance();
  if (!p) return escapeText(html);
  const cfg: Config = {
    ...POLICY[kind],
    FORBID_TAGS,
    FORBID_ATTR,
    ALLOW_UNKNOWN_PROTOCOLS: false,
    RETURN_TRUSTED_TYPE: false,
  };
  return String(p.sanitize(html, cfg));
}
