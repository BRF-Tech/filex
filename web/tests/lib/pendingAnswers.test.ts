// #196 - the questions a menu's rows wait on, and the bounded wait for them
// (lib/pendingAnswers). The explorer opens a row's menu once the encryption
// answer, the folder permissions and the desktop's keep state are in, or after
// a short ceiling: a question that never comes back must cost the ceiling, not
// a menu that never opens.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { pendingAnswers } from '../../../packages/core/src/lib/pendingAnswers';

describe('pendingAnswers', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('nothing asked: settled at once, without waiting for the ceiling', async () => {
    const q = pendingAnswers();
    let got: boolean | null = null;
    void q.settled(400).then((v) => (got = v));
    await Promise.resolve();
    expect(got).toBe(true);
    expect(q.count).toBe(0);
  });

  it('settles the moment the last outstanding question is answered', async () => {
    const q = pendingAnswers();
    const a = q.start();
    const b = q.start();
    expect(q.count).toBe(2);
    let got: boolean | null = null;
    void q.settled(400).then((v) => (got = v));
    vi.advanceTimersByTime(30);
    a();
    await Promise.resolve();
    expect(got, 'one question is still out').toBeNull();
    vi.advanceTimersByTime(20);
    b();
    await Promise.resolve();
    expect(got).toBe(true);
    expect(q.count).toBe(0);
  });

  it('a question that never comes back costs the ceiling, and the wait says so', async () => {
    const q = pendingAnswers();
    q.start();
    let got: boolean | null = null;
    void q.settled(400).then((v) => (got = v));
    vi.advanceTimersByTime(399);
    await Promise.resolve();
    expect(got).toBeNull();
    vi.advanceTimersByTime(1);
    await Promise.resolve();
    expect(got).toBe(false);
  });

  it('answering twice counts once', () => {
    const q = pendingAnswers();
    const a = q.start();
    q.start();
    a();
    a();
    expect(q.count).toBe(1);
  });

  it('a wait that timed out is not woken later, and a later wait still works', async () => {
    const q = pendingAnswers();
    const a = q.start();
    let first: boolean | null = null;
    void q.settled(100).then((v) => (first = v));
    vi.advanceTimersByTime(100);
    await Promise.resolve();
    expect(first).toBe(false);
    let second: boolean | null = null;
    void q.settled(100).then((v) => (second = v));
    a();
    await Promise.resolve();
    expect(first).toBe(false);
    expect(second).toBe(true);
  });
});
