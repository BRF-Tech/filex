// Fixture: the COPY. Identical tokens to copied-a.ts, comments aside — the
// verbatim pass must find this one. See copied-a.ts for why these files exist.

export interface Quota {
  used: number;
  total: number;
  unlimited: boolean;
}

export function quotaBar(quota: Quota | null, width: number): string {
  if (!quota) return '';
  if (quota.unlimited || quota.total <= 0) return '='.repeat(width);
  const ratio = Math.max(0, Math.min(1, quota.used / quota.total));
  const filled = Math.round(ratio * width);
  const empty = width - filled;
  let out = '';
  for (let i = 0; i < filled; i += 1) out += '#';
  for (let i = 0; i < empty; i += 1) out += '.';
  if (ratio > 0.9) out += ' !';
  else if (ratio > 0.75) out += ' *';
  else out += '  ';
  return out;
}
