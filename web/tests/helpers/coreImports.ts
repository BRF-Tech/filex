/**
 * packages/core's static import graph, read from the sources.
 *
 * Two rules stand on it: the vault is loaded with the first vault
 * (tests/quality/vaultLazy.test.ts) and the dialogs a person opens are loaded
 * with their first use (tests/quality/lazySurfaces.test.ts). Both are about
 * what the web app's main chunk carries, which is what the explorer loads
 * through `import` and `export ... from` - not `import type` / `export type`
 * (erased) and not `import()` (a chunk of its own).
 */
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';

export const CORE = path.resolve(__dirname, '../../../packages/core/src');

/** A path under packages/core/src, with forward slashes. */
export const rel = (abs: string) => path.relative(CORE, abs).split(path.sep).join('/');

/**
 * The script of a module: a .vue file's <script> blocks, comments out. A
 * one-line string that holds a comment's delimiters ('image/*') is emptied
 * first, or the comment stripper would run from it to the next comment's end.
 */
export function scriptOf(file: string): string {
  const text = readFileSync(file, 'utf8');
  const code = file.endsWith('.vue') ? [...text.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map((m) => m[1]).join('\n') : text;
  const delim = (q: string) => (q.includes('/*') || q.includes('*/') ? q[0] + q[0] : q);
  return code
    .replace(/'[^'\n]*'|"[^"\n]*"|`[^`\n]*`/g, delim)
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '');
}

/**
 * What loading `file` loads: its `import ... from`, `import '...'` and
 * `export ... from`, except `import type` / `export type` and an import whose
 * every name is `type` (both erased). `import()` is not among them.
 */
export function staticImports(file: string): string[] {
  const out: string[] = [];
  const re = /^\s*(?:import|export)\s+(type\s+)?(?:([^'";()=]*?)\s+from\s+)?['"]([^'"]+)['"]/gm;
  for (const m of scriptOf(file).matchAll(re)) {
    if (m[1]) continue;
    const names = (m[2] ?? '').trim();
    const braces = names.match(/^\{([\s\S]*)\}$/);
    if (braces) {
      const each = braces[1].split(',').map((s) => s.trim()).filter(Boolean);
      if (each.length > 0 && each.every((s) => s.startsWith('type '))) continue;
    }
    out.push(m[3]);
  }
  return out;
}

/** What `file` loads with `import('...')`, the literal specifiers. */
export function dynamicImports(file: string): string[] {
  return [...scriptOf(file).matchAll(/\bimport\(\s*['"]([^'"]+)['"]\s*\)/g)].map((m) => m[1]);
}

/** A relative specifier of `from`, resolved to a .ts or .vue file of core. */
export function resolveImport(from: string, spec: string): string | null {
  if (!spec.startsWith('.')) return null;
  const base = path.resolve(path.dirname(from), spec);
  for (const p of [base, `${base}.ts`, `${base}.vue`, path.join(base, 'index.ts')]) {
    if (existsSync(p) && !p.endsWith(path.sep) && /\.(ts|vue)$/.test(p)) return p;
  }
  return null;
}

/**
 * Every module of packages/core loaded with `entry`, by static imports, as
 * paths under src. `follow(from, to)` (both under src) may leave an edge out.
 */
export function loadedWith(entry: string, follow: (from: string, to: string) => boolean = () => true): Set<string> {
  const seen = new Set<string>();
  const todo = [entry];
  while (todo.length) {
    const f = todo.pop()!;
    if (seen.has(f)) continue;
    seen.add(f);
    for (const spec of staticImports(f)) {
      const r = resolveImport(f, spec);
      if (r && !seen.has(r) && follow(rel(f), rel(r))) todo.push(r);
    }
  }
  return new Set([...seen].map(rel));
}

/** The modules that import `target` statically, among `among` (paths under src). */
export function staticImportersOf(target: string, among: Iterable<string>): string[] {
  const out: string[] = [];
  for (const f of among) {
    const abs = path.join(CORE, f);
    if (staticImports(abs).some((spec) => resolveImport(abs, spec) === path.join(CORE, target))) out.push(f);
  }
  return out.sort();
}
