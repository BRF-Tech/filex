// Fixture: the ORIGINAL of a copy-paste pair. Never imported by anything.
// Paired with copied-b.ts, which is this block with the names left alone —
// what happens when a block is moved with the mouse.
//
// ⚠ Do not "tidy" these fixtures by extracting the shared part. They exist to
// be duplicated; web/tests/quality/duplication.test.ts asserts that the scanner
// still finds them, and a scanner that has quietly stopped finding anything is
// the failure mode the whole check is guarding against.

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
