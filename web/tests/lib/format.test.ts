// Tests for src/lib/format.ts. All formatters should be pure +
// locale-aware so they're trivial to assert.
import { describe, it, expect } from 'vitest';
import {
  formatBytes,
  formatNumber,
  formatDate,
  formatRelative,
  formatDuration,
  truncate,
} from '@/lib/format';

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

describe('formatDuration', () => {
  it('renders seconds for short', () => {
    expect(formatDuration(45)).toBe('45s');
  });

  it('renders minutes', () => {
    expect(formatDuration(120)).toBe('2m');
  });

  it('renders hours', () => {
    expect(formatDuration(7200)).toBe('2h');
  });

  it('renders days', () => {
    expect(formatDuration(86400 * 3)).toBe('3d');
  });

  it('returns em-dash for negative', () => {
    expect(formatDuration(-1)).toBe('—');
    expect(formatDuration(Number.NaN)).toBe('—');
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
