/**
 * A card a link's hash names (`/admin/settings#thumb-limits`, task #258) kept
 * where the router put it while the page around it is still being drawn.
 *
 * ⚠⚠ Why. The router scrolls ONCE, the moment the element exists - and on
 * Admin → Settings that is before the cards above it have their data (each
 * card fetches its own after it mounts and grows when the answer arrives),
 * and while the page being left is still in the document for its 120ms
 * cross-fade (App.vue). Measured on the 0.57 chain (e2e 256): the scroll went
 * to where the card was with the Tools page still above it, the Tools page
 * left, everything moved up, and the screen showed the bottom of Settings -
 * the card out of sight. So for a few seconds the element is brought back to
 * its place whenever the page around it moves; the person's first wheel,
 * touch, key or press ends that, so their own scrolling is never fought.
 */

const STOP_ON = ['wheel', 'touchstart', 'keydown', 'pointerdown'] as const;

export interface AnchorHoldOptions {
  /** How long the page may still be moving, in milliseconds. */
  ms?: number;
  /** How often its place is looked at, in milliseconds. */
  every?: number;
}

/**
 * Keeps the element `selector` names `top` pixels under the top of the
 * window - as far as the page can scroll - for `ms`. Returns the function
 * that ends it early (the next navigation calls it).
 */
export function holdInView(selector: string, top: number, opts: AnchorHoldOptions = {}): () => void {
  const ms = opts.ms ?? 5000;
  const every = opts.every ?? 100;
  const until = Date.now() + ms;
  const listen: AddEventListenerOptions = { capture: true, passive: true };
  let timer: ReturnType<typeof setInterval> | undefined;

  const stop = () => {
    if (timer !== undefined) clearInterval(timer);
    timer = undefined;
    for (const ev of STOP_ON) window.removeEventListener(ev, stop, listen);
  };

  const place = () => {
    if (Date.now() > until) {
      stop();
      return;
    }
    const el = document.querySelector(selector);
    if (!el) return;
    const page = document.scrollingElement ?? document.documentElement;
    const most = Math.max(0, page.scrollHeight - window.innerHeight);
    const want = Math.min(most, Math.max(0, window.scrollY + el.getBoundingClientRect().top - top));
    if (Math.abs(want - window.scrollY) > 1) window.scrollTo({ top: want });
  };

  for (const ev of STOP_ON) window.addEventListener(ev, stop, listen);
  timer = setInterval(place, every);
  return stop;
}
