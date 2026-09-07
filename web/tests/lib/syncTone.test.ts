// The badge colour for a storage's last sync.
//
// Red proof for the defect: the two views matched `'error'` and the backend
// writes `'failed'` (backend/internal/sync/poll.go:45), so a storage whose
// last sync FAILED was painted 'zinc' — the same neutral dot as a storage
// that had never synced at all.
import { describe, it, expect } from 'vitest';
import { syncTone } from '@/lib/syncTone';

describe('syncTone', () => {
  it("paints the backend's own failure spelling as a failure", () => {
    expect(syncTone('failed')).toBe('rose');
  });

  it("also accepts the sync-runs list's translated spelling", () => {
    expect(syncTone('error')).toBe('rose');
  });

  it('keeps the other states', () => {
    expect(syncTone('ok')).toBe('emerald');
    expect(syncTone('running')).toBe('sky');
    expect(syncTone('pending')).toBe('amber');
  });

  it('is neutral only when there is genuinely nothing to report', () => {
    expect(syncTone(undefined)).toBe('zinc');
    expect(syncTone(null)).toBe('zinc');
    expect(syncTone('')).toBe('zinc');
  });
});
