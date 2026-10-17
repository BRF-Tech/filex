/**
 * mermaidRender - the ONE way a Mermaid diagram is drawn: a `.mmd` file
 * (viewers/MermaidViewer) and a ```mermaid fence of a Markdown file
 * (modals/PreviewModal) alike.
 *
 * ⚠⚠ Mermaid lays a diagram out by drawing it into the page first, and what it
 * draws there can fetch from another server before anything of filex sees the
 * result: an image node (`A@{ img: "https://…" }`) is loaded with `new Image()`
 * to measure it and then drawn as an SVG <image>, a picture in an HTML label
 * is an <img>, a classDef or a `style` line and a `themeCSS` directive can
 * name a url(). Drawing a picture tells its server that the file was opened,
 * and from which IP address, so a diagram is drawn the way a file's preview
 * is (lib/sanitizeHtml, RemoteImages): nothing from elsewhere unless the
 * person allows it.
 *
 *   - An image node's address is taken out of the source before Mermaid reads
 *     it (holdShapeImages): Mermaid draws an empty `data:` stand-in of the
 *     right size whose `#fe-remote=` names the address, and the diagram policy
 *     turns it into a placeholder - or, allowed, into the picture, fetched with
 *     no referrer. An address it cannot read plainly (a YAML escape, an
 *     anchor) is never loaded.
 *   - Everything else is drawn in a frame of its own whose Content Security
 *     Policy fetches nothing (`securityLevel: 'sandbox'`, sealed by RENDER_CSP
 *     the moment Mermaid makes the frame): a label's picture or a url() in a
 *     style is not fetched while the diagram is laid out. The SVG Mermaid
 *     drew there then goes through the diagram policy, which holds the
 *     pictures and drops every url() of a style.
 *   - `themeCSS` and `dompurifyConfig` cannot be set from a diagram
 *     (`secure`).
 */
import { HELD_IMAGE_FRAGMENT, localImageSource, remoteImageUrl, type RemoteImages } from './sanitizeHtml';

/** The part of the Mermaid API filex draws with (an optional peer, loaded by the caller). */
export interface MermaidApi {
  initialize?: (config: Record<string, unknown>) => void;
  render: (id: string, text: string, container?: Element) => Promise<{ svg: string }>;
}

/**
 * What every diagram is drawn with. `secure` is Mermaid's list of settings a
 * diagram's own directives and front matter may not change: its defaults,
 * and the style sheet and the label sanitizer.
 */
export function mermaidConfig(theme?: 'dark' | 'default'): Record<string, unknown> {
  return {
    startOnLoad: false,
    securityLevel: 'sandbox',
    ...(theme ? { theme } : {}),
    secure: [
      'secure',
      'securityLevel',
      'startOnLoad',
      'maxTextSize',
      'suppressErrorRendering',
      'maxEdges',
      'themeCSS',
      'dompurifyConfig',
    ],
  };
}

/** The frame a diagram is laid out in fetches nothing: no picture, style sheet, font or script from anywhere. */
export const RENDER_CSP = "default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; font-src data: blob:";

/** The size of the stand-in of a picture whose own size is not known. */
const STAND_IN = { w: 120, h: 80 } as const;

/** An empty picture of `w` x `h` that names the address it stands for. */
export function standInImage(src: string, w: number = STAND_IN.w, h: number = STAND_IN.h): string {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}"></svg>`;
  return `data:image/svg+xml;base64,${btoa(svg)}${HELD_IMAGE_FRAGMENT}${encodeURIComponent(src)}`;
}

/** A picture's own size, fetched with no referrer (only for one the person allowed). */
async function naturalSize(src: string): Promise<{ w: number; h: number } | null> {
  if (typeof Image === 'undefined') return null;
  try {
    const img = new Image();
    img.referrerPolicy = 'no-referrer';
    img.src = src;
    await img.decode();
    return img.naturalWidth > 0 && img.naturalHeight > 0 ? { w: img.naturalWidth, h: img.naturalHeight } : null;
  } catch {
    return null;
  }
}

/** The end of the `@{ … }` block that opens at `start` (its `{`), quotes respected. */
function blockEnd(text: string, start: number): number {
  let quote = '';
  for (let i = start + 1; i < text.length; i++) {
    const c = text[i];
    if (quote) {
      if (c === '\\' && quote === '"') i++;
      else if (c === quote) quote = '';
    } else if (c === '"' || c === "'") quote = c;
    else if (c === '}') return i;
  }
  return text.length;
}

/** An `img:` value as written: the address it plainly says, or null when it says it some other way. */
function plainAddress(raw: string): string | null {
  if (raw.startsWith('"')) {
    const inner = raw.slice(1, -1);
    return raw.endsWith('"') && !inner.includes('\\') ? inner : null;
  }
  if (raw.startsWith("'")) return raw.endsWith("'") ? raw.slice(1, -1).replace(/''/g, "'") : null;
  return /^[A-Za-z0-9/._~:?#[\]@!$&()*+,;=%-]+$/.test(raw) && !/^[&*!|>%@`]/.test(raw) ? raw : null;
}

const IMG_VALUE = /(^|[\s,{])(["']?)img\2\s*:\s*("(?:[^"\\\n]|\\.)*"?|'(?:[^'\n]|'')*'?|[^,}\s]+)/g;

/**
 * The diagram's source with every image node's picture from elsewhere taken
 * out: its `img:` becomes a stand-in (standInImage) that names the address -
 * of the picture's own size when the person allows it now, measured with no
 * referrer. filex's own pictures and `data:` ones are left as they are; a
 * value that is no plain address becomes a stand-in that names none.
 */
export async function holdShapeImages(source: string, policy: RemoteImages): Promise<string> {
  let out = '';
  let from = 0;
  for (let at = source.indexOf('@{'); at >= 0; at = source.indexOf('@{', at + 2)) {
    const open = at + 1;
    const end = blockEnd(source, open);
    const block = source.slice(open, end);
    const edits: Array<{ index: number; length: number; value: string }> = [];
    for (const m of block.matchAll(IMG_VALUE)) {
      const raw = m[3];
      const address = plainAddress(raw);
      if (address !== null && localImageSource(address)) continue;
      const url = address !== null ? remoteImageUrl(address) : null;
      let value = standInImage('');
      if (url) {
        const size = policy.load?.(url.href) ? await naturalSize(url.href) : null;
        value = standInImage(url.href, size?.w, size?.h);
      }
      edits.push({ index: (m.index ?? 0) + m[0].length - raw.length, length: raw.length, value: `"${value}"` });
    }
    let rebuilt = '';
    let cursor = 0;
    for (const e of edits) {
      rebuilt += block.slice(cursor, e.index) + e.value;
      cursor = e.index + e.length;
    }
    rebuilt += block.slice(cursor);
    out += source.slice(from, open) + rebuilt;
    from = end;
    at = Math.max(at, end - 2);
  }
  return out + source.slice(from);
}

/**
 * The SVG Mermaid drew in its frame. In the `sandbox` level Mermaid hands
 * back an <iframe> whose `data:` address holds `<body …>SVG</body>`; this is
 * that SVG, or the answer as it is when it is not that shape.
 */
export function svgOfSandboxed(answer: string): string {
  const m = /src="data:text\/html;charset=UTF-8;base64,([^"]+)"/.exec(answer);
  if (!m) return answer;
  const bytes = Uint8Array.from(atob(m[1]), (c) => c.charCodeAt(0));
  const html = new TextDecoder().decode(bytes);
  return html.replace(/^\s*<body[^>]*>/i, '').replace(/<\/body>\s*$/i, '');
}

/** Seal a frame Mermaid made: its first content is a policy that fetches nothing, and no referrer. */
function seal(frame: HTMLIFrameElement): boolean {
  const doc = frame.contentDocument;
  if (!doc?.documentElement) return false;
  let head = doc.head;
  if (!head) {
    head = doc.createElement('head');
    doc.documentElement.insertBefore(head, doc.documentElement.firstChild);
  }
  const csp = doc.createElement('meta');
  csp.setAttribute('http-equiv', 'Content-Security-Policy');
  csp.setAttribute('content', RENDER_CSP);
  const ref = doc.createElement('meta');
  ref.setAttribute('name', 'referrer');
  ref.setAttribute('content', 'no-referrer');
  head.insertBefore(ref, head.firstChild);
  head.insertBefore(csp, head.firstChild);
  return true;
}

/**
 * Draw `source` with `mermaid` and answer the SVG it drew (not yet sanitized:
 * the caller hands it to the sanitizer's diagram kind with the gate's policy).
 * Throws what Mermaid throws, and refuses to draw at all when the frame it
 * lays the diagram out in could not be sealed.
 */
export async function renderMermaid(
  mermaid: MermaidApi,
  id: string,
  source: string,
  policy: RemoteImages,
  theme?: 'dark' | 'default',
): Promise<string> {
  mermaid.initialize?.(mermaidConfig(theme));
  const text = await holdShapeImages(source, policy);
  const host = document.createElement('div');
  host.setAttribute('aria-hidden', 'true');
  host.style.cssText = 'position:fixed;inset-inline-start:-20000px;top:0;width:1200px;height:900px;opacity:0;pointer-events:none;overflow:hidden;';
  document.body.appendChild(host);
  let sealed = false;
  let unsealed = false;
  // ⚠ Mermaid makes its frame and then awaits the diagram's parser before it
  // draws anything: this observer's callback, a microtask queued when the
  // frame was added, runs in that gap.
  const mo = new MutationObserver((records) => {
    for (const r of records) {
      for (const n of Array.from(r.addedNodes)) {
        if (n instanceof HTMLIFrameElement) {
          if (seal(n)) sealed = true;
          else unsealed = true;
        }
      }
    }
  });
  mo.observe(host, { childList: true, subtree: true });
  try {
    const { svg } = await mermaid.render(id, text, host);
    if (!sealed || unsealed) throw new Error('mermaid: the frame the diagram is laid out in could not be sealed');
    return svgOfSandboxed(svg);
  } finally {
    mo.disconnect();
    host.remove();
  }
}
