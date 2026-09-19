// The storage form speaks minutes, the row speaks seconds; one conversion,
// shared by the create and the edit page (issue #33: the per-storage cadence
// existed on the row and in the API and was reachable from no form).
import { describe, expect, it } from 'vitest';

import { minutesFromSeconds, secondsFromMinutes } from '@/lib/syncInterval';

describe('syncInterval', () => {
  it('shows the server default as an empty field, not as a number nobody typed', () => {
    expect(minutesFromSeconds(undefined)).toBe('');
    expect(minutesFromSeconds(0)).toBe('');
    expect(minutesFromSeconds(4)).toBe(''); // under the server's 5 s floor = unset
  });

  it('rounds a row cadence to whole minutes, never below one', () => {
    expect(minutesFromSeconds(900)).toBe(15);
    expect(minutesFromSeconds(3600)).toBe(60);
    expect(minutesFromSeconds(90)).toBe(2);
    expect(minutesFromSeconds(5)).toBe(1);
  });

  it('sends whole minutes as seconds and an empty field as "let the server pick"', () => {
    expect(secondsFromMinutes(15)).toBe(900);
    expect(secondsFromMinutes('30')).toBe(1800);
    expect(secondsFromMinutes(2.4)).toBe(120);
    expect(secondsFromMinutes('')).toBe(0);
    expect(secondsFromMinutes(null)).toBe(0);
    expect(secondsFromMinutes(0)).toBe(0);
    expect(secondsFromMinutes(-3)).toBe(0);
    expect(secondsFromMinutes('abc')).toBe(0);
  });

  it('round-trips what the row holds', () => {
    for (const s of [300, 900, 3600, 86400]) {
      expect(secondsFromMinutes(minutesFromSeconds(s))).toBe(s);
    }
  });
});
