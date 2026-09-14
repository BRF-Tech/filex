// Fixture: NOT duplication, and the scanner must say so.
//
// Two things live here that a careless detector reports:
//
//   1. A declarative table. Two of them, same keys, same shape — a theme map,
//      a route list, a shortcut catalogue all look like this. `$STR: $STR,`
//      repeated carries no logic, and the shape filter is what keeps these out
//      (measured: without it, themes.ts and router/index.ts were four of the
//      top findings in the real tree).
//
//   2. Two functions of similar size that do genuinely different things. If
//      these ever start matching, the renamed pass has been loosened too far
//      and the gate is about to become noise.

export const LIGHT_TOKENS: Record<string, string> = {
  '--x-bg': '#ffffff',
  '--x-fg': '#101319',
  '--x-muted': '#6b7280',
  '--x-border': '#e5e7eb',
  '--x-accent': '#2f6ceb',
  '--x-danger': '#dc2626',
  '--x-ok': '#16a34a',
  '--x-warn': '#d97706',
};

export const DARK_TOKENS: Record<string, string> = {
  '--x-bg': '#0f1115',
  '--x-fg': '#e6e8ee',
  '--x-muted': '#9aa2b1',
  '--x-border': '#2a2f3a',
  '--x-accent': '#5b8cff',
  '--x-danger': '#f87171',
  '--x-ok': '#4ade80',
  '--x-warn': '#fbbf24',
};

/** Longest run of consecutive integers present in the input. */
export function longestRun(values: number[]): number {
  const seen = new Set(values);
  let best = 0;
  for (const value of seen) {
    if (seen.has(value - 1)) continue;
    let length = 1;
    while (seen.has(value + length)) length += 1;
    if (length > best) best = length;
  }
  return best;
}

/** Split a path into segments, resolving `.` and `..` without touching disk. */
export function normalisePath(input: string): string {
  const stack: string[] = [];
  for (const segment of input.split('/')) {
    if (!segment || segment === '.') continue;
    if (segment === '..') {
      stack.pop();
      continue;
    }
    stack.push(segment);
  }
  return stack.join('/');
}
