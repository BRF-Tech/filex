// What the web app does with the time zone on the ACCOUNT.
//
// Reported 2026-09-14: on a fresh instance the web app drew a file's time in
// UTC (8:18 PM) while the same explorer embedded elsewhere drew the browser's
// clock (11:18 PM), for an account nobody had configured. Measured, the client
// was right and the server was not: every account was CREATED with timezone
// "UTC" (six call sites and the column default), and the app honours the
// account — so "never chose" arrived looking exactly like "chose UTC". The
// server side is pinned in Go (users_timezone_test.go, firstrun_test.go,
// user_timezone_unset_test.go for the migration). This pins the client half of
// the contract those tests rely on: an EMPTY zone from the server means the
// viewer's own device, and is never read as UTC.
import { describe, expect, it, beforeEach } from 'vitest';

import { activeTimeZone as coreActiveTimeZone, TIMEZONE_ACCOUNT_LS_KEY } from '@brftech/filex-core';
import { applyAccountTimeZone, getStoredTimeZone, setStoredTimeZone } from '@/lib/timezone';
import { formatDate } from '@/lib/format';

beforeEach(() => {
  setStoredTimeZone('');
  localStorage.clear();
});

describe('applyAccountTimeZone', () => {
  it("an empty zone from the server is the device's clock — not UTC", () => {
    setStoredTimeZone('UTC'); // a stale mirror from an account that used to say UTC
    applyAccountTimeZone('');
    expect(getStoredTimeZone()).toBe('');
    expect(coreActiveTimeZone()).toBeUndefined(); // Intl's own live answer
    expect(localStorage.getItem(TIMEZONE_ACCOUNT_LS_KEY)).toBeNull();

    const at = new Date('2026-09-13T20:18:00Z');
    const device = new Intl.DateTimeFormat('en', {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(at);
    expect(formatDate(at, 'en')).toBe(device);
  });

  it('a zone the person chose is applied', () => {
    applyAccountTimeZone('Asia/Tokyo');
    expect(coreActiveTimeZone()).toBe('Asia/Tokyo');
    expect(formatDate(new Date('2026-09-13T20:18:00Z'), 'en')).toContain('05:18');
  });

  it('no opinion in the payload (null / absent) touches nothing', () => {
    setStoredTimeZone('Europe/Istanbul');
    applyAccountTimeZone(null);
    applyAccountTimeZone(undefined);
    expect(coreActiveTimeZone()).toBe('Europe/Istanbul');
  });

  it('a zone the engine rejects falls back to the device, not to a guess', () => {
    applyAccountTimeZone('Mars/Olympus_Mons');
    expect(coreActiveTimeZone()).toBeUndefined();
  });
});
