// Tests for src/lib/format.ts. All formatters should be pure +
// locale-aware so they're trivial to assert.
import { describe, it, expect } from 'vitest';
import {
  formatBytes,
  formatNumber,
  formatDate,
  formatRelative,
  formatDuration,
  formatPercent,
  truncate,
} from '@/lib/format';
import { formatWhen } from '@brftech/filex-core';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';

describe('formatBytes', () => {
  it('returns 0 B for zero', () => {
    expect(formatBytes(0)).toBe('0 B');
  });

  it('renders bytes under 1 KB', () => {
    expect(formatBytes(512)).toBe('512 B');
  });

  it('renders KB with no decimals when >= 10', () => {
    // 50 KB → "50 KB" (one decimal max because value >= 10 → 1 fractional)
    expect(formatBytes(50_000)).toMatch(/50(\.0)?\sKB/);
  });

  it('renders MB / GB / TB', () => {
    expect(formatBytes(1_500_000)).toMatch(/MB/);
    expect(formatBytes(1_500_000_000)).toMatch(/GB/);
    expect(formatBytes(1_500_000_000_000)).toMatch(/TB/);
  });

  it('returns em-dash for negative or NaN', () => {
    expect(formatBytes(-1)).toBe('—');
    expect(formatBytes(Number.NaN)).toBe('—');
  });

  // ⚠ The unit is the language's own word (`unit.*` of the core catalogue,
  // a language pack's too): the admin panel printed "GB" where the explorer
  // beside it printed the French "Go" (translator report, 2026-09-22).
  it('names the unit in the language the number is in', async () => {
    const { registerLocale, resetLocales } = await import('@brftech/filex-core');
    registerLocale({ code: 'fr', label: 'Français', source: 'plugin', plugin: 'lang-fr', strings: { 'unit.gb': 'Go' } });
    try {
      expect(formatBytes(1_500_000_000, 'fr')).toBe('1,5 Go');
      expect(formatBytes(1_500_000_000, 'tr')).toBe('1,5 GB');
    } finally {
      resetLocales();
    }
  });

  it('uses the supplied locale for thousands separator', () => {
    // German uses dot as thousands sep — render 1500 KB ish.
    const en = formatBytes(1_234_567, 'en');
    const de = formatBytes(1_234_567, 'de');
    expect(en).not.toBe(de);
  });
});

describe('formatNumber', () => {
  it('formats integers with separators', () => {
    expect(formatNumber(1234567, 'en')).toMatch(/1[,.\s]234[,.\s]567/);
  });

  it('returns em-dash for null / undefined / NaN', () => {
    expect(formatNumber(null)).toBe('—');
    expect(formatNumber(undefined)).toBe('—');
    expect(formatNumber(Number.NaN)).toBe('—');
  });
});

describe('formatDate', () => {
  it('returns em-dash for empty input', () => {
    expect(formatDate(null)).toBe('—');
    expect(formatDate(undefined)).toBe('—');
    expect(formatDate('')).toBe('—');
  });

  it('returns em-dash for invalid date', () => {
    expect(formatDate('not-a-date')).toBe('—');
  });

  it('renders a real date', () => {
    const out = formatDate('2026-04-28T12:00:00Z', 'en');
    expect(out).not.toBe('—');
    expect(out).toMatch(/\d{4}/);
  });
});

describe('formatRelative', () => {
  it('returns em-dash for empty', () => {
    expect(formatRelative(null)).toBe('—');
  });

  it('renders relative for recent times', () => {
    const oneMinAgo = new Date(Date.now() - 60_000);
    const out = formatRelative(oneMinAgo, 'en');
    // Should be "1 minute ago" or "now" depending on rounding.
    expect(typeof out).toBe('string');
    expect(out.length).toBeGreaterThan(0);
  });

  it('falls back to formatDate for >7 days', () => {
    const longAgo = new Date(Date.now() - 30 * 24 * 3600 * 1000);
    const out = formatRelative(longAgo, 'en');
    // Should be a date string, not "30 days ago".
    expect(out).toMatch(/\d{4}/);
  });
});

// ⚠ The units are the LANGUAGE's (Intl), not English letters: the Sync page's
// Duration column read "0s" in the Turkish panel (release-candidate sweep,
// 2026-09-21). Intl's short forms, because the narrow Turkish ones are
// ambiguous ("3d" is 3 minutes — dakika — and "3s" 3 hours — saat).
describe('formatDuration', () => {
  // ⚠ It printed "45s" / "2m" / "2h" / "3d" — English letters under every
  // language (wave-2 wording sweep). Intl's unit names now, per language.
  it("renders seconds, minutes, hours and days in the viewer's language", () => {
    expect(formatDuration(45, 'en')).toBe('45 sec');
    expect(formatDuration(120, 'en')).toBe('2 min');
    expect(formatDuration(7200, 'en')).toBe('2 hr');
    expect(formatDuration(86400 * 3, 'en')).toBe('3 days');
    expect(formatDuration(45, 'tr')).toBe('45 sn.');
    expect(formatDuration(120, 'tr')).not.toMatch(/\dm$/);
  });

  it('returns em-dash for negative', () => {
    expect(formatDuration(-1)).toBe('—');
    expect(formatDuration(Number.NaN)).toBe('—');
  });
});

/* ── one format with the explorer ─────────────────────────────────────────
 * ⚠ QA, 2026-09-21: the explorer printed "21 Eyl 2026, 14:50", the admin
 * tables "21 Eyl 2026 15:02"; English "2:50 PM" beside "02:51 PM"; sizes
 * "1,96 KB" beside "1.9 KB". The admin helpers are the explorer's now. */
describe('the admin panel prints what the explorer prints', () => {
  const at = '2026-09-21T11:50:00Z';

  it("a date is the listing's own string, in both languages", () => {
    for (const code of ['en', 'tr']) {
      const explorer = useLocale(() => code).formatDate(Date.parse(at), { time: true });
      expect(formatDate(at, code)).toBe(explorer);
      expect(formatWhen(at, code, { time: true })).toBe(explorer);
    }
  });

  it('English hours carry no leading zero, and the date and clock are joined by a comma', () => {
    const out = formatDate('2026-09-12T08:05:00Z', 'en');
    expect(out).not.toMatch(/\b0\d:\d\d/);
    expect(out).toMatch(/2026, \d{1,2}:\d\d/);
    expect(formatDate('2026-09-12T08:05:00Z', 'tr')).toMatch(/2026, \d{2}:\d\d/);
  });

  // ⚠ Built from LOCAL fields so the hour is before ten in every zone: the case
  //   above only failed where the machine's zone kept 08:05Z before ten (CI is
  //   UTC; +3 turns it into 11:05 and hides the missing zero).
  it("a clock before ten is written the language's way, whatever the machine's zone", () => {
    const early = new Date(2026, 8, 12, 8, 5).toISOString();
    expect(formatDate(early, 'tr')).toMatch(/2026, 08:05$/);
    expect(formatDate(early, 'en')).toMatch(/2026, 8:05\s?AM$/);
  });

  it("a size is written the way the language writes numbers, with the catalogue's units", () => {
    expect(formatBytes(1_960, 'tr')).toBe('1,96 KB');
    expect(formatBytes(1_960, 'en')).toBe('1.96 KB');
    expect(formatBytes(1_200_000, 'tr')).toBe(useLocale(() => 'tr').formatSize(1_200_000));
  });

  it('a percentage too', () => {
    expect(formatPercent(12.5, 'en')).toBe('13%');
    expect(formatPercent(7.25, 'en')).toBe('7.3%');
    expect(formatPercent(7.25, 'tr')).toBe('%7,3');
    expect(formatPercent(Number.NaN, 'en')).toBe('—');
  });
});

describe('truncate', () => {
  it('returns input unchanged when short', () => {
    expect(truncate('hello', 10)).toBe('hello');
  });

  it('cuts and appends ellipsis when too long', () => {
    expect(truncate('1234567890', 5)).toBe('1234…');
  });

  it('handles empty input', () => {
    expect(truncate('', 5)).toBe('');
  });
});
