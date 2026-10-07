// Small things the train's commands share (scripts/train/*.mjs).

import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

/**
 * A command's usage: the comment block at the top of its own file, from the
 * line after the shebang up to the first `// ⚠` paragraph. The usage lives
 * once, where the code is read, and `--help` prints that.
 */
export function usageOf(moduleUrl) {
  const lines = fs.readFileSync(fileURLToPath(moduleUrl), 'utf8').replace(/\r\n/g, '\n').split('\n');
  const out = [];
  for (const line of lines.slice(lines[0]?.startsWith('#!') ? 1 : 0)) {
    if (!line.startsWith('//')) break;
    if (line.startsWith('// ⚠')) break;
    out.push(line.replace(/^\/\/ ?/, ''));
  }
  while (out.length && out.at(-1).trim() === '') out.pop();
  return out.join('\n');
}

/** Seconds as "42s" or "12m 05s". */
export function duration(secs) {
  const s = Math.max(0, Math.round(secs));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

/** The time since `t0` (Date.now() milliseconds), as duration() writes it. */
export const since = (t0) => duration((Date.now() - t0) / 1000);
