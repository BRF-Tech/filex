import { describe, expect, it } from 'vitest';
import { opPercent } from '@brftech/filex-core/src/lib/opProgress';
import { normalizeOp } from '@/api/ops';

/**
 * Issue #27: "during move status icon is like frozen and does not reflect
 * actual status". A single-source move is 0 of 1 until it ends; drawn as a
 * percentage that is a bar frozen at 0%.
 */
describe('opPercent', () => {
  it('uses bytes when a running transfer reports them', () => {
    expect(opPercent({ status: 'running', progress_total: 1, progress_done: 0, bytes_total: 4_000, bytes_done: 1_000 })).toBe(25);
  });

  it('has no percentage for a single source without bytes — not a frozen 0%', () => {
    expect(opPercent({ status: 'running', progress_total: 1, progress_done: 0 })).toBeNull();
  });

  it('has no percentage while bytes move but the total is not known yet', () => {
    expect(opPercent({ status: 'running', progress_total: 1, progress_done: 0, bytes_total: 0, bytes_done: 512 })).toBeNull();
  });

  it('still counts sources when there are several', () => {
    expect(opPercent({ status: 'running', progress_total: 4, progress_done: 1 })).toBe(25);
  });

  it('is 100 when done and clamps overshoot', () => {
    expect(opPercent({ status: 'done', progress_total: 1, progress_done: 0 })).toBe(100);
    expect(opPercent({ status: 'running', progress_total: 1, progress_done: 0, bytes_total: 10, bytes_done: 12 })).toBe(100);
  });
});

describe('admin ops normalization', () => {
  it('carries the byte counters the backend merges into a running op', () => {
    const op = normalizeOp({ id: 7, kind: 'move', status: 'running', total: 1, done: 0, bytes_total: 2048, bytes_done: 512 });
    expect(op.bytes_total).toBe(2048);
    expect(op.bytes_done).toBe(512);
    expect(opPercent(op)).toBe(25);
  });
});
