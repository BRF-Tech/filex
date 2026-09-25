// The storage line — the "Storage · 523.5 MB used" under the navigation panel
// and the matching chip in the admin top bar — and WHICH number it prints.
//
// Measured on a production install (2026-09-25): one drive holding 245.3 GB
// in 153,943 files, its Home card saying "245.3 GB used", and the line two
// hundred pixels to the left saying "523.5 MB used". Both were right about
// different things. The line read `/api/files/quota/me`, which is the
// person's upload counter (`SUM(nodes.size) WHERE owner_id = me`): 228.6 GB
// of that drive arrived through a sync and belongs to nobody, and the rest was
// uploaded by eleven people. The web explorer, the desktop app and the admin
// top bar all printed it under "Storage", in the same sentence the Home card
// uses for the drive's size.
//
// The rule these tests hold:
//   · a person WITH a quota sees their share against it — that ceiling is
//     what refuses their next upload, so it is the number that matters;
//   · a person WITHOUT one sees how full the drives they can open are — the
//     figure the Home cards print, and the one "Storage … used" promises;
//   · a figure the server cannot stand behind in full is a lower bound;
//   · with nothing trustworthy to say, the line says nothing.
import { describe, expect, it } from 'vitest';

import {
  hasCeiling,
  needsMeasuredDrives,
  storageLine,
  type MeasuredDrive,
  type PanelDrive,
  type PersonUsage,
} from '@brftech/filex-core/src/lib/storageLine';

const DRIVE = 'Diyetlif-Bulut-Depolama';
/** The install the report came from: no quota, 523.5 MB uploaded. */
const uploader: PersonUsage = { used_bytes: 523_457_650, quota_bytes: 0, unlimited: true };
/** The same drive, measured by `/api/files/quota/storages`. */
const measured: MeasuredDrive[] = [{ name: DRIVE, used_bytes: 245_276_276_422, coverage: null }];
/** The desktop app's panel: names only, no figures. */
const namesOnly: PanelDrive[] = [{ name: DRIVE }];

describe('storageLine — a person with a quota', () => {
  it('shows their own share against the ceiling, whatever the drives hold', () => {
    const person: PersonUsage = { used_bytes: 2_000_000_000, quota_bytes: 10_000_000_000, unlimited: false };
    expect(storageLine(person, [{ name: DRIVE, usedBytes: 245_276_276_422 }], measured)).toEqual({
      used: 2_000_000_000,
      total: 10_000_000_000,
      unlimited: false,
      partial: false,
    });
  });

  it('asks nobody for the drives — the ceiling is the whole answer', () => {
    expect(needsMeasuredDrives({ used_bytes: 1, quota_bytes: 10, unlimited: false }, namesOnly)).toBe(false);
  });
});

describe('storageLine — a person without one', () => {
  it('shows the drives, not the person’s upload counter (the reported case)', () => {
    expect(storageLine(uploader, namesOnly, measured)).toEqual({
      used: 245_276_276_422,
      total: 0,
      unlimited: true,
      partial: false,
    });
  });

  it('takes the host’s figures when it sent them — the Home cards print those', () => {
    const panel: PanelDrive[] = [
      { name: 'arsiv', usedBytes: 300 },
      { name: 'belgeler', usedBytes: 200 },
    ];
    expect(needsMeasuredDrives(uploader, panel)).toBe(false);
    expect(storageLine(uploader, panel, null)).toEqual({ used: 500, total: 0, unlimited: true, partial: false });
  });

  it('measures only what the host left out', () => {
    const panel: PanelDrive[] = [{ name: 'arsiv', usedBytes: 300 }, { name: 'belgeler' }];
    expect(needsMeasuredDrives(uploader, panel)).toBe(true);
    const rows: MeasuredDrive[] = [
      { name: 'arsiv', used_bytes: 999 },
      { name: 'belgeler', used_bytes: 200 },
    ];
    // arsiv keeps the host's 300: one drive, one number, on Home and here.
    expect(storageLine(uploader, panel, rows)?.used).toBe(500);
  });

  it('counts the drives in the panel and no others', () => {
    const rows: MeasuredDrive[] = [...measured, { name: 'gizli', used_bytes: 31_697_561_600 }];
    expect(storageLine(uploader, namesOnly, rows)?.used).toBe(245_276_276_422);
  });

  it('counts every measured drive when the host lists none', () => {
    const rows: MeasuredDrive[] = [
      { name: 'a', used_bytes: 100 },
      { name: 'b', used_bytes: 50 },
    ];
    expect(needsMeasuredDrives(uploader, [])).toBe(true);
    expect(storageLine(uploader, [], rows)).toEqual({ used: 150, total: 0, unlimited: true, partial: false });
  });
});

describe('storageLine — a lower bound stays one', () => {
  it('when the host says a drive’s figure is partial', () => {
    const panel: PanelDrive[] = [{ name: 'arsiv', usedBytes: 300, usedPartial: true }];
    expect(storageLine(uploader, panel, null)?.partial).toBe(true);
  });

  it('when the server says a drive’s catalogue does not cover it yet', () => {
    const rows: MeasuredDrive[] = [
      { name: DRIVE, used_bytes: 1_000, coverage: { complete: false, reason: 'lazy_filling' } },
    ];
    expect(storageLine(uploader, namesOnly, rows)).toEqual({ used: 1_000, total: 0, unlimited: true, partial: true });
  });

  it('but not for a coverage object that says it is complete', () => {
    const rows: MeasuredDrive[] = [{ name: DRIVE, used_bytes: 1_000, coverage: { complete: true } }];
    expect(storageLine(uploader, namesOnly, rows)?.partial).toBe(false);
  });

  it('when one of the panel’s drives could not be measured at all', () => {
    const panel: PanelDrive[] = [{ name: 'arsiv' }, { name: 'belgeler' }];
    const rows: MeasuredDrive[] = [{ name: 'arsiv', used_bytes: 300 }];
    expect(storageLine(uploader, panel, rows)).toEqual({ used: 300, total: 0, unlimited: true, partial: true });
  });

  it('when a figure is junk, the drive counts as not measured', () => {
    const panel: PanelDrive[] = [{ name: 'arsiv', usedBytes: -1 }, { name: 'belgeler' }];
    const rows = [
      { name: 'arsiv', used_bytes: Number.NaN },
      { name: 'belgeler', used_bytes: 200 },
    ] as MeasuredDrive[];
    expect(storageLine(uploader, panel, rows)).toEqual({ used: 200, total: 0, unlimited: true, partial: true });
  });
});

describe('storageLine — nothing to say, so it says nothing', () => {
  it('without the person’s figure (an older server, an app token)', () => {
    expect(storageLine(null, namesOnly, measured)).toBeNull();
  });

  it('without the drives’ figures (the call failed, an older server)', () => {
    expect(storageLine(uploader, namesOnly, null)).toBeNull();
    expect(storageLine(uploader, namesOnly, [])).toBeNull();
  });

  it('when the drives hold nothing — no "at least 0 B"', () => {
    expect(storageLine(uploader, namesOnly, [{ name: DRIVE, used_bytes: 0, coverage: { complete: false } }])).toBeNull();
  });
});

describe('hasCeiling', () => {
  it('is a positive quota the server did not call unlimited', () => {
    expect(hasCeiling({ used_bytes: 0, quota_bytes: 10, unlimited: false })).toBe(true);
    expect(hasCeiling({ used_bytes: 0, quota_bytes: 10 })).toBe(true);
    expect(hasCeiling({ used_bytes: 0, quota_bytes: 0, unlimited: true })).toBe(false);
    expect(hasCeiling({ used_bytes: 0, quota_bytes: 10, unlimited: true })).toBe(false);
    expect(hasCeiling(null)).toBe(false);
  });
});
