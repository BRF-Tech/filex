// The first-use tour is offered to a PERSON once — never once per mount.
//
// ⚠⚠ The bug (v0.43.0 QA): the tour opened 900 ms after EVERY explorer mount
// whose browser had no `filex.tourDone`, and that flag was written only when
// the tour was CLOSED. A second tab opened while the first still showed the
// tour offered it again, so did every remount, every other browser and every
// other device — and a browser-automation run met its card on each fresh
// mount, where it swallowed a click one run in two.
//
// The rule these tests hold: the answer is this browser's flag OR the account
// document's `tour`, it is recorded when the tour is OFFERED, and it only ever
// goes one way.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  TOUR_LS_KEY,
  configurePrefs,
  currentPrefs,
  flushPrefs,
  forgetPersonalPrefs,
  hydratePrefs,
  markTourSeen,
  offerTourOnce,
  resetPrefs,
  resetTourState,
  savePref,
  tourSeen,
} from '@brftech/filex-core';

function answer(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}
const answer200 = (body: unknown) => answer(200, body);

/** An account server: GET answers `doc`, PUT records the body and stores it. */
function account(doc: Record<string, string>) {
  const puts: Array<Record<string, string>> = [];
  let stored = { ...doc };
  const f = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body)) as { prefs: Record<string, string> };
      puts.push(body.prefs);
      stored = { ...body.prefs };
      return answer(200, { ok: true });
    }
    return answer(200, { prefs: stored });
  });
  return { f, puts, stored: () => stored };
}

beforeEach(() => {
  resetPrefs();
  resetTourState();
  localStorage.clear();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  resetTourState();
  resetPrefs();
});

describe('offered once, not once per mount', () => {
  it('a fresh browser with no account copy is offered it once — and a second mount (a new tab) is not', () => {
    const first = vi.fn();
    offerTourOnce(first);
    vi.advanceTimersByTime(1000);
    expect(first).toHaveBeenCalledTimes(1);
    // Recorded when OFFERED: the first tab has not closed it yet.
    expect(localStorage.getItem(TOUR_LS_KEY)).toBeTruthy();

    const secondTab = vi.fn();
    offerTourOnce(secondTab);
    vi.advanceTimersByTime(10_000);
    expect(secondTab).not.toHaveBeenCalled();
  });

  it('an unmount before the moment cancels the offer, and the next mount still gets it', () => {
    const gone = vi.fn();
    const cancel = offerTourOnce(gone);
    cancel();
    vi.advanceTimersByTime(10_000);
    expect(gone).not.toHaveBeenCalled();
    expect(tourSeen()).toBe(false);

    const next = vi.fn();
    offerTourOnce(next);
    vi.advanceTimersByTime(1000);
    expect(next).toHaveBeenCalledTimes(1);
  });
});

describe('per person: the account’s answer counts on every browser', () => {
  it('a person who was offered it elsewhere is not offered it on a new browser', async () => {
    const acc = account({ palette: 'night-blue', tour: 'done' });
    configurePrefs({ surface: 'web', fetchImpl: acc.f as unknown as typeof fetch });
    const shown = vi.fn();
    offerTourOnce(shown);
    // The account's answer arrives AFTER the mount — the offer waits for it.
    vi.advanceTimersByTime(1000);
    expect(shown).not.toHaveBeenCalled();
    await hydratePrefs();
    vi.advanceTimersByTime(10_000);
    expect(shown).not.toHaveBeenCalled();
  });

  it('first time for this person: offered, and recorded on the account WITHOUT wiping the rest of it', async () => {
    const acc = account({ palette: 'night-blue', density: 'compact' });
    configurePrefs({ surface: 'web', fetchImpl: acc.f as unknown as typeof fetch });
    const shown = vi.fn();
    offerTourOnce(shown);
    vi.advanceTimersByTime(1000);
    await hydratePrefs();
    expect(shown).toHaveBeenCalledTimes(1);
    await flushPrefs();
    expect(acc.puts.at(-1)).toEqual({ palette: 'night-blue', density: 'compact', tour: 'done' });

    // Another browser, same person: nothing local, the account says done.
    localStorage.clear();
    resetPrefs();
    resetTourState();
    configurePrefs({ surface: 'web', fetchImpl: acc.f as unknown as typeof fetch });
    await hydratePrefs();
    const elsewhere = vi.fn();
    offerTourOnce(elsewhere);
    vi.advanceTimersByTime(10_000);
    expect(elsewhere).not.toHaveBeenCalled();
  });

  it('never writes the account before its document has arrived (a PUT replaces the whole document)', async () => {
    const acc = account({ palette: 'night-blue', locale: 'tr' });
    configurePrefs({ surface: 'web', fetchImpl: acc.f as unknown as typeof fetch });
    markTourSeen();
    vi.advanceTimersByTime(5_000);
    expect(acc.puts).toHaveLength(0);
    await hydratePrefs();
    await flushPrefs();
    expect(acc.puts.at(-1)).toEqual({ palette: 'night-blue', locale: 'tr', tour: 'done' });
  });

  it('an account that never answers does not hold the tour back for ever', () => {
    const f = vi.fn(() => new Promise<never>(() => {}));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    void hydratePrefs();
    const shown = vi.fn();
    offerTourOnce(shown);
    vi.advanceTimersByTime(1000);
    expect(shown).not.toHaveBeenCalled();
    vi.advanceTimersByTime(5_000);
    expect(shown).toHaveBeenCalledTimes(1);
  });

  it('signing in after a sign-out waits for the NEW account’s answer, not the stale "nobody’s"', async () => {
    // The sign-in page ran with no session: the question was settled as "nobody's".
    forgetPersonalPrefs();
    let resolveFetch: (v: unknown) => void = () => {};
    const f = vi.fn(() => new Promise((r) => (resolveFetch = r)));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    // The person signs in (no reload): their document is on its way.
    const hydrating = hydratePrefs();
    const shown = vi.fn();
    offerTourOnce(shown);
    vi.advanceTimersByTime(1000);
    expect(shown).not.toHaveBeenCalled();
    // The request goes out after the (async) headers resolve.
    while (!f.mock.calls.length) await Promise.resolve();
    resolveFetch(answer200({ prefs: { tour: 'done' } }));
    await hydrating;
    vi.advanceTimersByTime(10_000);
    expect(shown).not.toHaveBeenCalled();
  });

  it('a later palette change keeps the tour on the account (the key is part of the document)', async () => {
    const acc = account({ tour: 'done' });
    configurePrefs({ surface: 'web', fetchImpl: acc.f as unknown as typeof fetch });
    await hydratePrefs();
    savePref('palette', 'forest');
    await flushPrefs();
    expect(acc.puts.at(-1)).toEqual({ tour: 'done', palette: 'forest' });
    expect(currentPrefs().tour).toBe('done');
  });
});

describe('one way only', () => {
  it('signing out does not clear it — a flag that can only say "yes" cannot leak a wrong "no"', () => {
    markTourSeen();
    forgetPersonalPrefs();
    expect(localStorage.getItem(TOUR_LS_KEY)).toBeTruthy();
    expect(tourSeen()).toBe(true);
  });

  it('the flag earlier releases wrote (`1`) still counts', () => {
    localStorage.setItem(TOUR_LS_KEY, '1');
    const shown = vi.fn();
    offerTourOnce(shown);
    vi.advanceTimersByTime(10_000);
    expect(shown).not.toHaveBeenCalled();
  });
});
