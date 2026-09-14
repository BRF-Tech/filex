/**
 * Every SVG the product ships has to be an SVG.
 *
 * Both `web/public/icons/icon.svg` (the PWA + install-banner icon) and
 * `web/public/favicon.svg` (the browser tab) stopped being images on
 * 2026-09-13, and the cause was a COMMENT:
 *
 *     (comment) flat product blue (#2f6ceb, the light value of the primary
 *     token, written with the two leading dashes) (/comment)
 *
 * A double hyphen is illegal inside an XML comment, and every `--fe-*` token
 * name contains one. The moment the comment named the token, the file stopped
 * parsing: `new Image().decode()` threw `EncodingError: The source image
 * cannot be decoded`, the install banner rendered a broken-image box on every
 * page of the app, and the favicon vanished from the tab.
 *
 * Nothing caught it. It is invisible in a diff (the comment reads fine),
 * invisible in the build (nothing parses these files — they are copied
 * verbatim out of `public/`), and invisible in every other test. It shows up
 * only as a blank box, in a browser, to somebody looking.
 *
 * ⚠ The `data:` URI case is worse than the file case: a broken data-URI
 * favicon fails with nothing in the network tab to inspect, so those are
 * parsed here too — `site/index.html` carries the landing page's favicon that
 * way.
 *
 * ⚠ The checker below is hand-rolled on purpose. Node has no XML parser, the
 * suite runs in happy-dom, and happy-dom's `DOMParser` is not one either:
 * measured, it returns a document with NO `parsererror` for the broken comment
 * above, for an unclosed tag and for a mismatched close tag — a gate built on
 * it would have been green against the very files that were broken. Adding an
 * XML library for four small hand-written files is the wrong trade, so this
 * checks exactly the well-formedness rules those files can break.
 */
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');

/** Directories whose .svg files are shipped to a browser as images. */
const ROOTS = ['web/public', 'site'];

function walk(dir: string): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

const VOID_OK = new Set<string>(); // SVG has no void elements; every tag closes.

/**
 * Strict-enough XML well-formedness for hand-written SVG. Returns [] when the
 * document is fine, otherwise the first problems it found, each naming the
 * rule that was broken rather than a column number.
 */
export function xmlProblems(source: string): string[] {
  const problems: string[] = [];
  const stack: string[] = [];
  let i = 0;

  while (i < source.length) {
    const lt = source.indexOf('<', i);
    if (lt === -1) break;

    // Text between tags: a bare & that is not an entity is not well-formed.
    const text = source.slice(i, lt);
    if (/&(?!#?[a-zA-Z0-9]+;)/.test(text)) problems.push('unescaped "&" in text');

    if (source.startsWith('<!--', lt)) {
      const end = source.indexOf('-->', lt + 4);
      if (end === -1) {
        problems.push('unterminated comment');
        break;
      }
      const body = source.slice(lt + 4, end);
      if (body.includes('--')) {
        problems.push(
          `"--" inside a comment: "${body.trim().split('\n')[0].slice(0, 60)}" — illegal in XML`,
        );
      }
      i = end + 3;
      continue;
    }
    if (source.startsWith('<![CDATA[', lt)) {
      const end = source.indexOf(']]>', lt);
      if (end === -1) {
        problems.push('unterminated CDATA');
        break;
      }
      i = end + 3;
      continue;
    }
    if (source.startsWith('<?', lt) || source.startsWith('<!', lt)) {
      const end = source.indexOf('>', lt);
      if (end === -1) {
        problems.push('unterminated declaration');
        break;
      }
      i = end + 1;
      continue;
    }

    const gt = source.indexOf('>', lt);
    if (gt === -1) {
      problems.push('unterminated tag');
      break;
    }
    const raw = source.slice(lt + 1, gt).trim();

    if (raw.startsWith('/')) {
      const name = raw.slice(1).trim();
      const open = stack.pop();
      if (open !== name) {
        problems.push(`</${name}> closes <${open ?? 'nothing'}>`);
      }
    } else {
      const selfClosing = raw.endsWith('/');
      const body = selfClosing ? raw.slice(0, -1) : raw;
      const m = body.match(/^([A-Za-z_][\w.:-]*)([\s\S]*)$/);
      if (!m) {
        problems.push(`malformed tag "<${raw.slice(0, 40)}>"`);
      } else {
        const [, name, attrs] = m;
        // Every attribute is name="value" or name='value'. An unquoted value
        // is HTML's leniency, not XML's.
        const rest = attrs
          .replace(/\s+[A-Za-z_][\w.:-]*\s*=\s*("[^"]*"|'[^']*')/g, '')
          .trim();
        if (rest) problems.push(`<${name}>: attribute syntax "${rest.slice(0, 40)}"`);
        if (!selfClosing && !VOID_OK.has(name)) stack.push(name);
      }
    }
    i = gt + 1;
  }

  if (stack.length) problems.push(`unclosed <${stack.join('>, <')}>`);
  return problems;
}

const svgFiles = ROOTS.flatMap((r) => walk(path.join(REPO, r))).filter((f) => f.endsWith('.svg'));

describe('shipped SVGs', () => {
  it('finds the files it is meant to check', () => {
    // A guard for the guard: an empty list would make every case below pass by
    // checking nothing at all.
    expect(svgFiles.length, 'no .svg found under web/public or site').toBeGreaterThanOrEqual(2);
  });

  it('the checker itself catches what broke the icons', () => {
    // Red proof, kept in the file: these are the four ways these files can
    // stop being XML, including the exact comment that shipped.
    const good = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
      <!-- flat product blue, the fe-primary token -->
      <rect width="32" height="32" fill="#2f6ceb" />
    </svg>`;
    expect(xmlProblems(good)).toEqual([]);

    expect(
      xmlProblems(`<svg><!-- the light value of --fe-primary --><rect/></svg>`)[0],
    ).toContain('"--" inside a comment');
    expect(xmlProblems(`<svg><rect></svg>`)[0]).toContain('</svg> closes <rect>');
    expect(xmlProblems(`<svg><rect/>`)[0]).toContain('unclosed <svg>');
    expect(xmlProblems(`<svg><rect width=32 /></svg>`)[0]).toContain('attribute syntax');
  });

  it('every shipped .svg is well-formed XML', () => {
    const broken: string[] = [];
    for (const f of svgFiles) {
      for (const p of xmlProblems(readFileSync(f, 'utf8'))) {
        broken.push(`${path.relative(REPO, f)} — ${p}`);
      }
    }
    expect(
      broken,
      'a file with a .svg name that no browser can decode renders as a broken-image box',
    ).toEqual([]);
  });

  it('every svg data: URI in a shipped page is well-formed XML', () => {
    const pages = ['web/index.html', 'site/index.html', 'site/social-preview.src.html']
      .map((p) => path.join(REPO, p))
      .filter((p) => existsSync(p));
    expect(pages.length).toBeGreaterThan(0);

    const broken: string[] = [];
    for (const page of pages) {
      const src = readFileSync(page, 'utf8');
      // ⚠ Stop at the attribute's own closing quote: the payload itself is full
      // of single quotes, so a lazier pattern truncates it and reports a defect
      // that is not there. (Measured — that false alarm cost a round.)
      const uris = [...src.matchAll(/"(data:image\/svg\+xml,[^"]*)"/g)].map((m) => m[1]);
      for (const [n, uri] of uris.entries()) {
        const decoded = decodeURIComponent(uri.slice(uri.indexOf(',') + 1));
        for (const p of xmlProblems(decoded)) {
          broken.push(`${path.relative(REPO, page)} data-uri#${n + 1} — ${p}`);
        }
      }
    }
    expect(broken, 'a broken data: URI fails silently — nothing in the network tab').toEqual([]);
  });
});
