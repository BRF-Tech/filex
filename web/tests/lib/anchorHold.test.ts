// The card a link's hash names stays where the router put it while the page
// around it is still being drawn (lib/anchorHold, task #258).
//
// RED PROOF (int/057-wave 60f73d64, e2e 256): Tools → Thumbnail repair's link
// to /admin/settings#thumb-limits scrolled once, while the Tools page was
// still fading out above the Settings page; the Tools page left, everything
// moved up, and the screen showed the bottom of Settings - the card out of
// sight ("viewport ratio 0").
//
// happy-dom lays nothing out: the card's place on the page and the window's
// scroll are the test's own numbers.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { holdInView } from '@/lib/anchorHold';

describe('holdInView', () => {
  let scrollY = 0;
  let cardAt = 0;
  let pageHeight = 4000;

  beforeEach(() => {
    vi.useFakeTimers();
    scrollY = 0;
    cardAt = 0;
    pageHeight = 4000;
    document.body.innerHTML = '<form id="thumb-limits"></form>';
    const card = document.getElementById('thumb-limits')!;
    card.getBoundingClientRect = () => ({ top: cardAt - scrollY }) as DOMRect;
    const page = document.scrollingElement ?? document.documentElement;
    Object.defineProperty(page, 'scrollHeight', { configurable: true, get: () => pageHeight });
    vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => scrollY);
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(800);
    vi.spyOn(window, 'scrollTo').mockImplementation(((o: ScrollToOptions) => {
      scrollY = o.top ?? scrollY;
    }) as typeof window.scrollTo);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('brings the card back each time the page moves under it', () => {
    // The router's jump, with the page being left still above the card.
    cardAt = 2600;
    scrollY = 2584;
    const stop = holdInView('#thumb-limits', 16);

    // The leaving page goes: everything moves up by its height.
    cardAt = 900;
    vi.advanceTimersByTime(100);
    expect(scrollY).toBe(884);

    // A card above it gets its data and grows.
    cardAt = 1300;
    vi.advanceTimersByTime(100);
    expect(scrollY).toBe(1284);
    stop();
  });

  it('goes as far as the page scrolls, and no farther', () => {
    pageHeight = 1500;
    cardAt = 1200;
    const stop = holdInView('#thumb-limits', 16);
    vi.advanceTimersByTime(100);
    expect(scrollY).toBe(700);
    stop();
  });

  it("the person's own scrolling ends it", () => {
    cardAt = 900;
    holdInView('#thumb-limits', 16);
    vi.advanceTimersByTime(100);
    expect(scrollY).toBe(884);

    window.dispatchEvent(new Event('wheel'));
    scrollY = 200;
    cardAt = 1300;
    vi.advanceTimersByTime(500);
    expect(scrollY, 'the page is left where the person put it').toBe(200);
  });

  it('ends by itself after its time, and when the next navigation calls it', () => {
    cardAt = 900;
    holdInView('#thumb-limits', 16, { ms: 300 });
    vi.advanceTimersByTime(400);
    cardAt = 1300;
    vi.advanceTimersByTime(200);
    expect(scrollY, 'over: a later move is not followed').toBe(884);

    const stop = holdInView('#thumb-limits', 16);
    stop();
    cardAt = 2000;
    vi.advanceTimersByTime(200);
    expect(scrollY).toBe(884);
  });
});
