// The desktop window's own two-language strings (ui/app.html `STRINGS`), and
// the page's own wording functions, read for the tests that check what the
// window will say.
//
// ⚠ Read from the page's source, not duplicated here: a copy would agree with
// whatever this file believes the page says.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const APP_HTML = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'ui', 'app.html');

function appHtml(): string {
  return fs.readFileSync(APP_HTML, 'utf8');
}

/** key → [English, Turkish] */
export function appStrings(): Record<string, [string, string]> {
  const html = appHtml();
  const start = html.indexOf('const STRINGS = {');
  if (start < 0) throw new Error('ui/app.html has no `const STRINGS = {`');
  const open = html.indexOf('{', start);
  // The table ends at the first line that closes it at its own indentation.
  const close = html.indexOf('\n      };', open);
  if (close < 0) throw new Error('ui/app.html: the end of STRINGS was not found');
  // A plain object literal of string pairs: evaluated as one.
  return new Function(`return ${html.slice(open, close + '\n      }'.length)};`)() as Record<string, [string, string]>;
}

/** The source of `function <name>(…) {…}` in the page, by its braces. */
function functionSource(html: string, name: string): string {
  const start = html.indexOf(`function ${name}(`);
  if (start < 0) throw new Error(`ui/app.html has no function ${name}`);
  let depth = 0;
  for (let i = html.indexOf('{', start); i < html.length; i++) {
    if (html[i] === '{') depth++;
    else if (html[i] === '}' && --depth === 0) return html.slice(start, i + 1);
  }
  throw new Error(`ui/app.html: function ${name} does not end`);
}

/**
 * The page's own functions `names` — with its `T` and `STRINGS` — as the
 * window runs them in `lang`. Only for functions that need nothing else of the
 * page.
 */
export function appFunctions(lang: 'en' | 'tr', names: string[]): Record<string, (...a: unknown[]) => unknown> {
  const html = appHtml();
  const src = ['T', ...names].map((n) => functionSource(html, n)).join('\n');
  return new Function('STRINGS', 'LANG', `${src}\nreturn { ${names.join(', ')} };`)(appStrings(), lang);
}
