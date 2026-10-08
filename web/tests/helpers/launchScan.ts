/**
 * Reads, from the source, how a file starts processes: every call of a
 * function it imports from child_process, with whether the call keeps the
 * child off the screen on Windows (`windowsHide: true`) and whether it starts
 * the child detached. tests/quality/hiddenConsoleWindows.test.ts holds the
 * rules (#197); this file only reads.
 *
 * ⚠ Read from the code alone: comments, strings, the text of template
 * literals and regular expressions are blanked first (codeOnly), so a call
 * quoted in a comment, a test's sample string or a pattern does not count,
 * and a `)` inside a string does not end a call early.
 */

/** child_process's functions that start a process. */
export const LAUNCHERS = new Set(['spawn', 'spawnSync', 'execFile', 'execFileSync', 'exec', 'execSync', 'fork']);
/** The ones the parent does not wait for: the child runs in the background. */
export const BACKGROUND = new Set(['spawn', 'execFile', 'exec', 'fork']);

/** How a call sets `detached`: not at all, false, depending on the platform, or on everywhere. */
export type Detached = 'none' | 'off' | 'platform' | 'on';

export interface Launch {
  /** 1-based line of the call. */
  line: number;
  /** child_process's name for the function, whatever the file calls it. */
  name: string;
  /** `windowsHide: true` among the call's arguments. */
  hidden: boolean;
  detached: Detached;
}

export interface FileLaunches {
  launches: Launch[];
  /** Lines of an import of child_process as a whole (`import * as cp`, `require` into one name): calls through it are not read. */
  opaque: number[];
  /** The file stops anywhere but Linux before its work: nothing it starts runs on Windows. */
  linuxOnly: boolean;
}

const REGEX_AFTER_CHAR = '(,=:[!&|?{};+-*%<>~^';
const REGEX_AFTER_WORD = new Set(['return', 'typeof', 'instanceof', 'in', 'of', 'new', 'delete', 'void', 'throw', 'case', 'do', 'else', 'yield', 'await']);

/**
 * The source with every comment, string, template text and regular
 * expression literal blanked to spaces. Newlines stay, so offsets and line
 * numbers are the source's. A `/` counts as a regular expression after an
 * operator, an opening bracket or a keyword, and as a division after a name,
 * a number or a closing bracket; a regular expression never runs past its
 * line, so a wrong guess blanks one line at most.
 */
export function codeOnly(src: string): string {
  const out = src.split('');
  const n = src.length;
  const blank = (from: number, to: number) => {
    for (let k = from; k < to && k < n; k++) if (out[k] !== '\n' && out[k] !== '\r') out[k] = ' ';
  };
  // The brace depth outside each `${` that is open.
  const holes: number[] = [];
  let depth = 0;
  let prev = '';
  let i = 0;
  if (src.startsWith('#!')) {
    const eol = src.indexOf('\n');
    i = eol < 0 ? n : eol;
    blank(0, i);
  }
  // Template text from `from` up to its closing backtick or its next `${`.
  const template = (from: number): number => {
    let j = from;
    while (j < n) {
      const c = src[j];
      if (c === '\\') {
        j += 2;
        continue;
      }
      if (c === '`') {
        blank(from, j);
        prev = 'x';
        return j + 1;
      }
      if (c === '$' && src[j + 1] === '{') {
        blank(from, j);
        holes.push(depth);
        depth++;
        prev = '{';
        return j + 2;
      }
      j++;
    }
    blank(from, n);
    return n;
  };
  while (i < n) {
    const c = src[i];
    const d = src[i + 1];
    if (c === '/' && d === '/') {
      const eol = src.indexOf('\n', i);
      const stop = eol < 0 ? n : eol;
      blank(i, stop);
      i = stop;
    } else if (c === '/' && d === '*') {
      const end = src.indexOf('*/', i + 2);
      const stop = end < 0 ? n : end + 2;
      blank(i, stop);
      i = stop;
    } else if (c === "'" || c === '"') {
      let j = i + 1;
      while (j < n && src[j] !== c && src[j] !== '\n') j += src[j] === '\\' ? 2 : 1;
      blank(i + 1, j);
      i = j + 1;
      prev = 'x';
    } else if (c === '`') {
      i = template(i + 1);
    } else if (c === '/') {
      if (prev === '' || (prev.length === 1 && REGEX_AFTER_CHAR.includes(prev)) || REGEX_AFTER_WORD.has(prev)) {
        let j = i + 1;
        let inClass = false;
        while (j < n && src[j] !== '\n') {
          const r = src[j];
          if (r === '\\') {
            j += 2;
            continue;
          }
          if (r === '[') inClass = true;
          else if (r === ']') inClass = false;
          else if (r === '/' && !inClass) break;
          j++;
        }
        blank(i + 1, j);
        i = j + 1;
        while (i < n && /[A-Za-z]/.test(src[i])) i++;
        prev = 'x';
      } else {
        prev = '/';
        i++;
      }
    } else if (c === '{') {
      depth++;
      prev = '{';
      i++;
    } else if (c === '}') {
      depth--;
      if (holes.length && holes[holes.length - 1] === depth) {
        holes.pop();
        i = template(i + 1);
      } else {
        prev = '}';
        i++;
      }
    } else if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1;
      while (j < n && /[\w$]/.test(src[j])) j++;
      prev = src.slice(i, j);
      i = j;
    } else {
      if (!/\s/.test(c)) prev = c;
      i++;
    }
  }
  return out.join('');
}

function lineAt(src: string, index: number): number {
  let line = 1;
  for (let k = 0; k < index; k++) if (src.charCodeAt(k) === 10) line++;
  return line;
}

const escapeName = (name: string) => name.replace(/\$/g, '\\$');

/** A call's arguments, its `(` to the matching `)`, in the blanked source. */
function argsAt(code: string, open: number): string {
  let depth = 0;
  for (let k = open; k < code.length; k++) {
    if (code[k] === '(') depth++;
    else if (code[k] === ')' && --depth === 0) return code.slice(open, k + 1);
  }
  return code.slice(open);
}

const HIDE = /\bwindowsHide\s*:\s*true\b/;
const PLATFORM = /\bprocess\.platform\b|\bIS_WIN\b|\bisWin(?:dows)?\b/;

/** `detached` in a call's arguments; a bare name is read from its `const` in the same file. */
function detachedIn(args: string, code: string): Detached {
  const m = /\bdetached\b(?:\s*:\s*([^,}\n]+))?/.exec(args);
  if (!m) return 'none';
  let value = (m[1] ?? 'detached').trim();
  if (value === 'false') return 'off';
  if (/^[A-Za-z_$][\w$]*$/.test(value)) {
    const def = new RegExp(`\\bconst\\s+${escapeName(value)}\\s*=\\s*([^;\\n]+)`).exec(code);
    if (def) value = def[1];
  }
  return PLATFORM.test(value) ? 'platform' : 'on';
}

/** Every launch in one file's source. */
export function readLaunches(src: string): FileLaunches {
  const code = codeOnly(src);
  // A match counts only where it is code (an import quoted in a string is blanked).
  const isCode = (at: number) => code[at] === src[at];
  const names = new Map<string, string>();
  const opaque: number[] = [];
  const lists: Array<[RegExp, RegExp]> = [
    [/\bimport\s*\{([^}]*)\}\s*from\s*['"](?:node:)?child_process['"]/g, /\s+as\s+/],
    [/\b(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(\s*['"](?:node:)?child_process['"]\s*\)/g, /\s*:\s*/],
  ];
  for (const [re, sep] of lists) {
    for (const m of src.matchAll(re)) {
      if (!isCode(m.index ?? 0)) continue;
      for (const part of m[1].split(',')) {
        const [orig, local] = part.trim().split(sep).map((s) => s.trim());
        if (orig && LAUNCHERS.has(orig)) names.set(local || orig, orig);
      }
    }
  }
  const whole = /\bimport\s+(?:\*\s*as\s+)?[\w$]+\s+from\s*['"](?:node:)?child_process['"]|\b(?:const|let|var)\s+[\w$]+\s*=\s*require\(\s*['"](?:node:)?child_process['"]\s*\)/g;
  for (const m of src.matchAll(whole)) if (isCode(m.index ?? 0)) opaque.push(lineAt(src, m.index ?? 0));

  let linuxOnly = false;
  for (const m of src.matchAll(/\bif\s*\(\s*process\.platform\s*!==\s*['"]linux['"]\s*\)\s*\{([^}]*)\}/g)) {
    if (isCode(m.index ?? 0) && /\b(?:return|process\.exit)\b/.test(m[1])) linuxOnly = true;
  }

  const launches: Launch[] = [];
  for (const [local, name] of names) {
    const call = new RegExp(`(?<![\\w.$])${escapeName(local)}\\s*\\(`, 'g');
    for (const m of code.matchAll(call)) {
      const at = m.index ?? 0;
      const args = argsAt(code, at + m[0].length - 1);
      launches.push({ line: lineAt(src, at), name, hidden: HIDE.test(args), detached: detachedIn(args, code) });
    }
  }
  launches.sort((a, b) => a.line - b.line);
  return { launches, opaque, linuxOnly };
}
