/**
 * The text a source file can put on a screen, read by a real parser and
 * without its comments: string literals and template text, never the notes
 * beside them.
 *
 * A line-based grep cannot tell `// a - b` from `'a - b'`, and every source in
 * this repo is full of comments nobody is shown. So each language is parsed
 * for what it is:
 *
 *   - TypeScript / JavaScript: the TypeScript parser's string, template and
 *     no-substitution literals (their cooked text, so an escape counts too);
 *     regular expressions are patterns, not text, and are left out.
 *   - Vue: the template's text, its static attribute values and the literals
 *     inside its bindings and interpolations (template comments dropped), the
 *     script blocks as TypeScript, the style blocks as CSS.
 *   - CSS: everything outside comments (a `content: "..."` is on screen).
 *   - HTML / SVG: markup outside `<!-- -->`, scripts as TypeScript, styles as
 *     CSS.
 *   - Go: string literals, raw and interpreted, outside `//` and block
 *     comments.
 *   - JSON: every string (a package's description is an installer's text).
 *   - YAML: every line outside `#` comments, block scalars whole (a store
 *     manifest's description is what the store prints).
 *
 * Each hit is a line number and the text that was found there.
 */
import fs from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { parse as parseSfc } from 'vue/compiler-sfc';

export interface Shown {
  line: number;
  text: string;
}

/** Files under `dir` whose name matches `ext`, skipping builds, deps and test data. */
export function sourceFiles(dir: string, ext: RegExp, out: string[] = []): string[] {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (!['node_modules', 'dist', 'testdata', 'vendor'].includes(e.name)) sourceFiles(p, ext, out);
    } else if (ext.test(e.name)) out.push(p);
  }
  return out;
}

const lineAt = (src: string, offset: number) => {
  let n = 1;
  for (let i = 0; i < offset; i++) if (src.charCodeAt(i) === 10) n++;
  return n;
};

/** String-like literals of a TypeScript/JavaScript text; `firstLine` is the line its first character sits on. */
export function tsStrings(code: string, firstLine = 1): Shown[] {
  const out: Shown[] = [];
  const sf = ts.createSourceFile('shown.ts', code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const visit = (n: ts.Node) => {
    switch (n.kind) {
      case ts.SyntaxKind.StringLiteral:
      case ts.SyntaxKind.NoSubstitutionTemplateLiteral:
      case ts.SyntaxKind.TemplateHead:
      case ts.SyntaxKind.TemplateMiddle:
      case ts.SyntaxKind.TemplateTail:
        out.push({
          line: firstLine + sf.getLineAndCharacterOfPosition(n.getStart(sf)).line,
          text: (n as ts.LiteralLikeNode).text,
        });
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

/** CSS outside comments, one entry per line that has anything left. */
export function cssStrings(css: string, firstLine = 1): Shown[] {
  const bare = css.replace(/\/\*[\s\S]*?\*\//g, (c) => c.replace(/[^\n]/g, ' '));
  const out: Shown[] = [];
  bare.split('\n').forEach((l, i) => {
    if (l.trim()) out.push({ line: firstLine + i, text: l.trim() });
  });
  return out;
}

// The template AST's node types (@vue/compiler-core NodeTypes).
const ELEMENT = 1;
const TEXT = 2;
const INTERPOLATION = 5;
const ATTRIBUTE = 6;
const DIRECTIVE = 7;

interface TplNode {
  type: number;
  content?: string | { content?: string; loc: { source: string; start: { line: number } } };
  loc: { start: { line: number } };
  props?: TplProp[];
  children?: TplNode[];
}
interface TplProp {
  type: number;
  name: string;
  value?: { content: string; loc: { start: { line: number } } };
  exp?: { loc: { source: string; start: { line: number } } };
}

/** A single-file component: template text, attributes and bindings, scripts, styles. */
export function vueStrings(src: string): Shown[] {
  const out: Shown[] = [];
  const { descriptor } = parseSfc(src, { filename: 'shown.vue' });
  const ast = descriptor.template?.ast as unknown as { children: TplNode[] } | undefined;
  const node = (n: TplNode) => {
    if (n.type === TEXT && typeof n.content === 'string') {
      out.push({ line: n.loc.start.line, text: n.content });
    } else if (n.type === INTERPOLATION && n.content && typeof n.content === 'object') {
      out.push(...tsStrings(n.content.loc.source, n.content.loc.start.line));
    } else if (n.type === ELEMENT) {
      for (const p of n.props ?? []) {
        if (p.type === ATTRIBUTE && p.value) out.push({ line: p.value.loc.start.line, text: p.value.content });
        if (p.type === DIRECTIVE && p.exp) out.push(...tsStrings(p.exp.loc.source, p.exp.loc.start.line));
      }
      (n.children ?? []).forEach(node);
    }
  };
  ast?.children.forEach(node);
  for (const block of [descriptor.script, descriptor.scriptSetup]) {
    if (block) out.push(...tsStrings(block.content, block.loc.start.line));
  }
  for (const style of descriptor.styles) out.push(...cssStrings(style.content, style.loc.start.line));
  return out;
}

/** An HTML (or SVG) page: markup outside comments, inline scripts and styles. */
export function htmlStrings(src: string): Shown[] {
  const out: Shown[] = [];
  const blank = (s: string) => s.replace(/[^\n]/g, ' ');
  let rest = src.replace(/<!--[\s\S]*?-->/g, blank);
  rest = rest.replace(/(<script\b[^>]*>)([\s\S]*?)(<\/script>)/g, (_all, open: string, body: string, close: string, at: number) => {
    out.push(...tsStrings(body, lineAt(src, at + open.length)));
    return open + blank(body) + close;
  });
  rest = rest.replace(/(<style\b[^>]*>)([\s\S]*?)(<\/style>)/g, (_all, open: string, body: string, close: string, at: number) => {
    out.push(...cssStrings(body, lineAt(src, at + open.length)));
    return open + blank(body) + close;
  });
  rest.split('\n').forEach((l, i) => {
    if (l.trim()) out.push({ line: i + 1, text: l.trim() });
  });
  return out;
}

/** Every string value (and key) of a JSON document, at the line it first appears on. */
export function jsonStrings(src: string): Shown[] {
  const out: Shown[] = [];
  const add = (s: string) => {
    const at = src.indexOf(JSON.stringify(s).slice(1, -1));
    out.push({ line: at < 0 ? 1 : lineAt(src, at), text: s });
  };
  const walk = (v: unknown) => {
    if (typeof v === 'string') add(v);
    else if (Array.isArray(v)) v.forEach(walk);
    else if (v && typeof v === 'object') {
      for (const [k, x] of Object.entries(v)) {
        add(k);
        walk(x);
      }
    }
  };
  walk(JSON.parse(src));
  return out;
}

/** Go string literals (interpreted and raw), comments skipped; rune literals are not text. */
export function goStrings(src: string): Shown[] {
  const out: Shown[] = [];
  const n = src.length;
  let i = 0;
  let line = 1;
  while (i < n) {
    const c = src[i];
    if (c === '\n') {
      line++;
      i++;
    } else if (c === '/' && src[i + 1] === '/') {
      while (i < n && src[i] !== '\n') i++;
    } else if (c === '/' && src[i + 1] === '*') {
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) {
        if (src[i] === '\n') line++;
        i++;
      }
      i += 2;
    } else if (c === '`') {
      const start = line;
      let j = i + 1;
      while (j < n && src[j] !== '`') {
        if (src[j] === '\n') line++;
        j++;
      }
      out.push({ line: start, text: src.slice(i + 1, j) });
      i = j + 1;
    } else if (c === '"' || c === "'") {
      let j = i + 1;
      while (j < n && src[j] !== c && src[j] !== '\n') j += src[j] === '\\' ? 2 : 1;
      if (c === '"') out.push({ line, text: src.slice(i + 1, j) });
      i = j + 1;
    } else {
      i++;
    }
  }
  return out;
}

/**
 * A YAML file without its comments: every key and value line, and the lines
 * of a block scalar (`|`, `>-` …) whole, since a `#` inside one is text.
 *
 * Read as text, not parsed: a store manifest's `description: >-` is what an
 * app store prints, the `# …` beside it is a note to the next maintainer.
 */
export function yamlStrings(src: string): Shown[] {
  const out: Shown[] = [];
  let block = -1; // the indentation of the line that opened a block scalar
  src.split('\n').forEach((raw, i) => {
    const line = raw.replace(/\r$/, '');
    const indent = line.length - line.trimStart().length;
    if (block >= 0) {
      if (!line.trim()) return;
      if (indent > block) {
        out.push({ line: i + 1, text: line.trim() });
        return;
      }
      block = -1;
    }
    const code = yamlCode(line);
    if (code.trim()) out.push({ line: i + 1, text: code.trim() });
    if (/(^|[\s:-])[|>][-+0-9]*\s*$/.test(code)) block = indent;
  });
  return out;
}

/** One YAML line up to its comment: a `#` after whitespace, outside a quoted scalar. */
function yamlCode(line: string): string {
  let quote = '';
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === quote) quote = '';
    } else if ((c === '"' || c === "'") && (i === 0 || /[\s:[{,-]/.test(line[i - 1]!))) {
      quote = c;
    } else if (c === '#' && (i === 0 || /\s/.test(line[i - 1]!))) {
      return line.slice(0, i);
    }
  }
  return line;
}
