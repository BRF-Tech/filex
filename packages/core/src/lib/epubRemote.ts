/**
 * epubRemote - an EPUB chapter asks no other server for anything until the
 * person says so (0.57).
 *
 * epub.js draws each chapter in a frame of its own (viewers/EpubViewer), from
 * the book's own files - which it hands the frame as `blob:` addresses - and
 * from whatever else the chapter names: a picture, a style sheet, a font, a
 * url() in a style on another server. Drawing it tells that server that the
 * book was opened, and from which IP address, so before a chapter is drawn
 * (sealChapter):
 *
 *   - its head starts with a Content Security Policy. Held (the default), the
 *     frame loads only the book's own files (`blob:`, `data:`) and filex's
 *     (`'self'`): pictures, style sheets, fonts and media from anywhere else
 *     are not fetched, whatever names them (an attribute, a style sheet, an
 *     escape in CSS). Allowed (the person's setting "Load remote images in
 *     previews", or "Load all remote images in this file" for this opening:
 *     one frame, so one answer for the whole book), pictures, style sheets
 *     and media from elsewhere load; fonts from elsewhere never do. No
 *     script, either way;
 *   - every element that fetches by itself (img, link, media) gets
 *     `referrerpolicy="no-referrer"`, and the head `<meta name="referrer">`;
 *   - a url() or `@import` from elsewhere in the chapter's own styles (a
 *     `style` attribute, a `<style>`) is taken out, as a file's preview takes
 *     every url() of a style out: ⚠ measured in Chromium, a request a frame's
 *     CSS makes carries filex's origin as its Referer whatever the frame's
 *     referrer policy says, so it would tell that server where the book was
 *     opened even when the person allowed the pictures.
 */

import { localImageSource, srcsetCandidates } from './sanitizeHtml';

/** What a held chapter's frame may load: the book's own files and filex's, nothing else, and no script. */
export const EPUB_HELD_CSP =
  "default-src 'none'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline' data: blob:; font-src 'self' data: blob:; media-src 'self' data: blob:";

/** What an allowed chapter's frame may load: pictures, style sheets and media from anywhere; fonts only the book's; no script. */
export const EPUB_ALLOWED_CSP =
  "default-src 'none'; img-src * data: blob:; style-src * 'unsafe-inline' data: blob:; font-src 'self' data: blob:; media-src * data: blob:";

const XHTML_NS = 'http://www.w3.org/1999/xhtml';

/** An address that leaves the book: `https://…`, `http://…`, `//host/…`. */
const ELSEWHERE = /^\s*(?:https?:)?\/\//i;

const URL_IN_CSS = /url\(\s*(['"]?)([^'")]*)\1\s*\)|@import\s+(['"])([^'"]+)\3/gi;

function elsewhere(v: string | null | undefined): v is string {
  return !!v && ELSEWHERE.test(v) && !localImageSource(v);
}

function cssAddresses(css: string): string[] {
  const out: string[] = [];
  for (const m of css.matchAll(URL_IN_CSS)) out.push((m[2] ?? m[4] ?? '').trim());
  return out;
}

/** `css` with every url() and `@import` from elsewhere taken out (a url() of the book's own stays). */
export function withoutRemoteUrls(css: string): string {
  return css
    .replace(/@import\s+(?:url\(\s*)?(['"]?)([^'")\s;]+)\1\s*\)?[^;]*;?/gi, (all, _q, addr: string) => (elsewhere(addr) ? '' : all))
    .replace(/url\(\s*(['"]?)([^'")]*)\1\s*\)/gi, (all, _q, addr: string) => (elsewhere(addr.trim()) ? 'none' : all));
}

/**
 * Every address from elsewhere a chapter names for its browser to load: a
 * picture (`src`, `srcset`, an SVG `<image>`'s `href`), a style sheet, a
 * url() or an `@import` in a style, media, an object. A link a reader follows
 * (`<a href>`) is not one, nor epub.js's own `<base>` and canonical link, nor
 * an address of filex's.
 */
export function remoteReferencesOf(doc: Document): string[] {
  const out = new Set<string>();
  const add = (v: string | null | undefined): void => {
    if (elsewhere(v)) out.add(v.trim());
  };
  for (const el of Array.from(doc.getElementsByTagName('*'))) {
    const tag = el.localName.toLowerCase();
    if (tag === 'base' || (tag === 'link' && (el.getAttribute('rel') ?? '').toLowerCase() === 'canonical')) continue;
    for (const name of ['src', 'poster', 'data', 'background', 'xlink:href', 'href']) {
      if (name === 'href' && (tag === 'a' || tag === 'area')) continue;
      add(el.getAttribute(name));
    }
    for (const c of srcsetCandidates(el.getAttribute('srcset') ?? '')) add(c.url);
    const style = el.getAttribute('style');
    if (style) cssAddresses(style).forEach(add);
    if (tag === 'style') cssAddresses(el.textContent ?? '').forEach(add);
  }
  return [...out];
}

function meta(doc: Document, attrs: Record<string, string>): Element {
  const m = doc.createElementNS(XHTML_NS, 'meta');
  for (const [k, v] of Object.entries(attrs)) m.setAttribute(k, v);
  return m;
}

/**
 * Seal a chapter before it is drawn (see the head of this file). Answers the
 * policy's element, to be switched to EPUB_ALLOWED_CSP (allowChapter) once
 * the person allows the book's pictures, or null when the chapter has no
 * document element.
 */
export function sealChapter(doc: Document, allowRemote: boolean): Element | null {
  const root = doc.documentElement;
  if (!root) return null;
  let head = doc.getElementsByTagName('head')[0] ?? null;
  if (!head) {
    head = doc.createElementNS(XHTML_NS, 'head');
    root.insertBefore(head, root.firstChild);
  }
  for (const el of Array.from(doc.getElementsByTagName('*'))) {
    const tag = el.localName.toLowerCase();
    if (tag === 'img' || tag === 'link' || tag === 'video' || tag === 'audio' || tag === 'source') {
      el.setAttribute('referrerpolicy', 'no-referrer');
    }
    const style = el.getAttribute('style');
    if (style) el.setAttribute('style', withoutRemoteUrls(style));
    if (tag === 'style' && el.textContent) el.textContent = withoutRemoteUrls(el.textContent);
  }
  head.insertBefore(meta(doc, { name: 'referrer', content: 'no-referrer' }), head.firstChild);
  const csp = meta(doc, { 'http-equiv': 'Content-Security-Policy', content: allowRemote ? EPUB_ALLOWED_CSP : EPUB_HELD_CSP });
  head.insertBefore(csp, head.firstChild);
  return csp;
}

/** A sealed chapter's policy, once the person allowed the book's pictures from elsewhere. */
export function allowChapter(csp: Element): void {
  csp.setAttribute('content', EPUB_ALLOWED_CSP);
}
