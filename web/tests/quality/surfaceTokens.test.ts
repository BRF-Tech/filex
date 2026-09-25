/**
 * An app's screen wears the theme: no fixed colour, radius or face in a
 * surface node.
 *
 * An app plugin runs in wasm and DESCRIBES its screen (a surface: `form`,
 * `steps`, `people-picker`, `pdf-fields`, `signature-pad`, …); filex draws
 * every node with its own components. So a theme reaches an app's screen for
 * free — as long as those components paint with the `--fe-*` tokens and
 * nothing else. One `color: #fff` is enough to break that, and it only shows
 * on somebody's branded, dark instance: task #57 measured exactly that
 * (2026-09-25, operator theme + dark + Arabic): the active step's number was
 * white on a pale-pink primary on every wizard, and the people picker's
 * avatar likewise, while filex's own share dialog beside them used the
 * palette's `--fe-text-on-primary`.
 *
 * WHAT IT READS
 * -------------
 * The CSS blocks the plugin components OWN — every `fe-<block>` their
 * templates use that no other component in the product uses (derived, so a
 * NEW node is covered the day it is written) — plus `fe-surface`, the
 * vocabulary itself. In every base.css rule that names one of them:
 *
 *   colour  — no hex, rgb()/hsl()/…, or colour name, not even as a `var()`
 *             fallback. Use a token (`--fe-text-on-primary` on a
 *             `--fe-primary` ground, `--fe-border-strong`, …).
 *   radius  — `var(--fe-radius*)`, `0`, `50%` or a pill (`999px`): a theme
 *             sets the radius, and a literal one is the corner it cannot move.
 *   face    — `inherit` or `var(--fe-font*)`: a theme sets the face too.
 *
 * And the components' own script and templates, with the `lib/` modules they
 * import: no colour literal in a string (a `:style`, a canvas `fillStyle`).
 *
 * Sizes and spacing are NOT checked: they are metrics, and no theme can move
 * them (packages/core/src/lib/themes.ts → "What is deliberately NOT themed").
 *
 * WHEN A FIXED COLOUR IS RIGHT
 * ----------------------------
 * ⚠⚠ DOCUMENT SPACE and PAPER. A PDF page is white in every theme, the ink on
 * it is dark, and the signature pad is a piece of that paper: what is drawn
 * there is what gets stamped onto the document (docs/APP-PLUGINS-API.md →
 * "Theme tokens, node by node"). A signer's colour is an identity colour,
 * like a file type's (`--fe-icon-*`), and it is drawn on that page. Add an
 * entry below WITH the reason; an entry that no longer matches anything fails
 * the test, so the list cannot rot into a blanket exemption.
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const CORE = path.join(REPO, 'packages/core/src');
const PLUGIN_DIR = path.join(CORE, 'components/plugin');
const BASE_CSS = path.join(CORE, 'styles/base.css');

const rel = (p: string) => path.relative(REPO, p).split(path.sep).join('/');

function walk(dir: string, ext: RegExp): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    if (/^(node_modules|dist)$/.test(name)) return [];
    return statSync(p).isDirectory() ? walk(p, ext) : ext.test(name) ? [p] : [];
  });
}

/* ── which blocks are the plugin components' own ─────────────────────── */

function blocksIn(files: string[]): Set<string> {
  const out = new Set<string>();
  for (const f of files) {
    const src = readFileSync(f, 'utf8');
    const at = src.indexOf('<template');
    const tpl = at >= 0 ? src.slice(at) : src;
    for (const m of tpl.matchAll(/\bfe-[a-z0-9]+(?:-[a-z0-9]+)*/g)) out.add(m[0].split('__')[0]);
  }
  return out;
}

const pluginVue = walk(PLUGIN_DIR, /\.vue$/);
const elsewhereVue = [
  ...walk(CORE, /\.vue$/).filter((f) => !f.startsWith(PLUGIN_DIR + path.sep)),
  ...walk(path.join(REPO, 'web/src'), /\.vue$/),
];
const shared = blocksIn(elsewhereVue);
export const OWNED = new Set([...[...blocksIn(pluginVue)].filter((b) => !shared.has(b)), 'fe-surface']);

/* ── the exemptions, each with its reason ────────────────────────────── */

interface CssAllow {
  /** The rule's selector list, whitespace-normalised, exactly. */
  selector: string;
  /** The property (a shorthand counts as its own name). */
  prop: string;
  why: string;
}

export const CSS_ALLOW: CssAllow[] = [
  {
    selector: '.fe-ssig__canvas, .fe-ssig__preview',
    prop: 'background',
    why: 'PAPER: the pad is a piece of the document. The ink is dark and the PNG it exports is what is stamped, so the pad is white in every theme — the same reason the PDF page is.',
  },
  {
    selector: '.fe-spdf__page',
    prop: 'background',
    why: 'DOCUMENT SPACE: the PDF page, white in every theme like any viewer draws it.',
  },
  {
    selector: '.fe-spdf__field',
    prop: 'border-radius',
    why: 'DOCUMENT SPACE: a box on the page is drawn at the page’s scale, not the interface’s.',
  },
  {
    selector: '.fe-spdf__field.is-foreign',
    prop: 'background',
    why: 'DOCUMENT SPACE: another signer’s box, a translucent grey on the white page.',
  },
  {
    selector: '.fe-spdf__text',
    prop: 'color',
    why: 'DOCUMENT SPACE: ink typed into a box on the white page.',
  },
  {
    selector: '.fe-spdf__check .fe-aicon',
    prop: 'color',
    why: 'DOCUMENT SPACE: a tick on the white page.',
  },
  {
    selector: '.fe-spdf__val',
    prop: 'color',
    why: 'DOCUMENT SPACE: a filled value on the white page.',
  },
  {
    selector: '.fe-spdf__handle',
    prop: 'border',
    why: 'DOCUMENT SPACE: the resize handle’s white ring separates it from the box it sits on, on the white page.',
  },
];

interface SrcAllow {
  file: string;
  /** A substring of the offending line. */
  line: string;
  why: string;
}

export const SRC_ALLOW: SrcAllow[] = [
  {
    file: 'packages/core/src/lib/signaturePad.ts',
    line: "SIGNATURE_INK = '#111111'",
    why: 'The ink of the PICTURE, not of the screen: the pad exports it as the PNG that is stamped onto the PDF, and it is drawn on the white pad (PAPER above), so it is dark in every theme.',
  },
  {
    file: 'packages/core/src/lib/pdfFields.ts',
    line: "SIGNER_PALETTE = ['#",
    why: 'Identity colours, like a file type’s: they tell signers apart on the white page and on the matching card, and a theme does not repaint them. The ink on them is derived from each colour (inkOn), not from the theme.',
  },
];

/* ── CSS ─────────────────────────────────────────────────────────────── */

interface Rule {
  selector: string;
  body: string;
  line: number;
}

function cssRules(css: string): Rule[] {
  // Comments blanked to spaces, so offsets (and line numbers) survive.
  const src = css.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '));
  const lineAt = (i: number) => src.slice(0, i).split('\n').length;
  const out: Rule[] = [];
  const block = (from: number, to: number) => {
    let i = from;
    while (i < to) {
      const open = src.indexOf('{', i);
      if (open < 0 || open >= to) return;
      const selector = src.slice(i, open).replace(/\s+/g, ' ').trim();
      let depth = 1;
      let j = open + 1;
      while (j < to && depth) {
        if (src[j] === '{') depth++;
        else if (src[j] === '}') depth--;
        j++;
      }
      if (selector.startsWith('@')) block(open + 1, j - 1);
      else out.push({ selector, body: src.slice(open + 1, j - 1), line: lineAt(open) });
      i = j;
    }
  };
  block(0, src.length);
  return out;
}

function decls(body: string): { prop: string; value: string }[] {
  const out: { prop: string; value: string }[] = [];
  let depth = 0;
  let cur = '';
  for (const ch of body) {
    if (ch === '(') depth++;
    if (ch === ')') depth--;
    if (ch === ';' && depth === 0) {
      out.push(cur);
      cur = '';
    } else cur += ch;
  }
  out.push(cur);
  return out
    .map((d) => d.trim())
    .filter((d) => d.includes(':'))
    .map((d) => {
      const at = d.indexOf(':');
      return { prop: d.slice(0, at).trim().toLowerCase(), value: d.slice(at + 1).trim() };
    });
}

const PAINT = /^(color|background(-color|-image)?|border(-(top|right|bottom|left|block|inline)(-(start|end))?)?(-color)?|outline(-color)?|fill|stroke|box-shadow|text-shadow|caret-color|accent-color|text-decoration(-color)?|column-rule(-color)?)$/;
const COLOUR =
  /#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(|\b(?:white|black|red|green|blue|gr[ae]y|silver|yellow|orange|purple|pink|navy|teal)\b/i;
const RADIUS_OK = /^(0|0px|50%|999px|9999px|inherit|var\(--fe-radius[a-z-]*\))$/;
const FACE_OK = /^(inherit|var\(--fe-font(-mono)?\))$/;

function owns(selector: string): string[] {
  const hits: string[] = [];
  for (const b of OWNED) {
    const re = new RegExp(`\\.${b}(?=__|--|[^a-z0-9-]|$)`);
    if (re.test(selector)) hits.push(b);
  }
  return hits;
}

const rules = cssRules(readFileSync(BASE_CSS, 'utf8'));
const ownedRules = rules.filter((r) => owns(r.selector).length);

function cssFindings(): { where: string; selector: string; prop: string; value: string }[] {
  const found: { where: string; selector: string; prop: string; value: string }[] = [];
  for (const r of ownedRules) {
    for (const { prop, value } of decls(r.body)) {
      let bad = false;
      if (PAINT.test(prop) && COLOUR.test(value)) bad = true;
      if (/radius$/.test(prop) && !value.split(/\s+/).every((v) => RADIUS_OK.test(v))) bad = true;
      if (prop === 'font-family' && !FACE_OK.test(value)) bad = true;
      if (prop === 'font' && !/^inherit$/.test(value) && !/var\(--fe-font/.test(value)) bad = true;
      if (bad) found.push({ where: `styles/base.css:${r.line}`, selector: r.selector, prop, value });
    }
  }
  return found;
}

const cssAllowed = (f: { selector: string; prop: string }) =>
  CSS_ALLOW.some((a) => a.selector === f.selector && a.prop === f.prop);

/* ── the components' script and templates ────────────────────────────── */

function libImports(file: string): string[] {
  const src = readFileSync(file, 'utf8');
  const out: string[] = [];
  for (const m of src.matchAll(/from\s+'(\.[^']*\/lib\/[^']+)'/g)) {
    const p = path.resolve(path.dirname(file), m[1]);
    for (const cand of [p, `${p}.ts`]) {
      try {
        if (statSync(cand).isFile()) out.push(cand);
      } catch {
        /* not this spelling */
      }
    }
  }
  return out;
}

const sourceFiles = [...new Set([...pluginVue, ...pluginVue.flatMap(libImports)])];

function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:'"`\w])\/\/.*$/gm, '$1');
}

const STRING = /'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`/g;

function srcFindings(): { file: string; line: string; lineNo: number }[] {
  const found: { file: string; line: string; lineNo: number }[] = [];
  for (const f of sourceFiles) {
    const lines = stripComments(readFileSync(f, 'utf8')).split('\n');
    lines.forEach((line, i) => {
      for (const s of line.match(STRING) ?? []) {
        if (/#[0-9a-fA-F]{3,8}\b/.test(s) || /\b(?:rgba?|hsla?)\(/.test(s)) {
          found.push({ file: rel(f), line: line.trim(), lineNo: i + 1 });
          return;
        }
      }
    });
  }
  return found;
}

const srcAllowed = (f: { file: string; line: string }) => SRC_ALLOW.some((a) => a.file === f.file && f.line.includes(a.line));

/* ── the tests ───────────────────────────────────────────────────────── */

describe('app-plugin screens paint with theme tokens', () => {
  it('knows what it is guarding (a guard that finds nothing measures nothing)', () => {
    for (const b of ['fe-steps', 'fe-speople', 'fe-ssig', 'fe-spdf', 'fe-spin', 'fe-sfile', 'fe-surface']) {
      expect(OWNED.has(b), `${b} should be a plugin-owned block`).toBe(true);
    }
    // A block every screen shares is filex's own, not the app's.
    expect(OWNED.has('fe-btn')).toBe(false);
    expect(ownedRules.length, 'plugin-owned rules found in base.css').toBeGreaterThan(80);
    expect(sourceFiles.some((f) => f.endsWith(`lib${path.sep}signaturePad.ts`))).toBe(true);
  });

  it('no plugin-owned CSS rule carries a fixed colour, radius or face', () => {
    const bad = cssFindings().filter((f) => !cssAllowed(f));
    expect(
      bad.map((f) => `${f.where}  ${f.selector} { ${f.prop}: ${f.value} }`),
      'paint a surface node with --fe-* tokens (see the header of this file); a document-space exception goes into CSS_ALLOW with its reason',
    ).toEqual([]);
  });

  it('no plugin component (or the lib it imports) writes a colour literal', () => {
    const bad = srcFindings().filter((f) => !srcAllowed(f));
    expect(
      bad.map((f) => `${f.file}:${f.lineNo}  ${f.line}`),
      'a colour in a :style or on a canvas must come from a token (or, for the paper, an exempted constant)',
    ).toEqual([]);
  });

  it('every exemption still matches something (no stale entry)', () => {
    const css = cssFindings();
    const src = srcFindings();
    const staleCss = CSS_ALLOW.filter((a) => !css.some((f) => f.selector === a.selector && f.prop === a.prop));
    const staleSrc = SRC_ALLOW.filter((a) => !src.some((f) => f.file === a.file && f.line.includes(a.line)));
    expect([...staleCss.map((a) => `css ${a.selector} ${a.prop}`), ...staleSrc.map((a) => `src ${a.file} ${a.line}`)]).toEqual([]);
  });
});
