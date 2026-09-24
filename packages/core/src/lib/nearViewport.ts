/**
 * whenNearViewport — call `cb` once `el` comes within NEAR of the viewport.
 *
 * A folder view puts every tile on the page at once, and a thumbnail is a
 * request: a folder of 344 files asked for ~240 of them the moment it opened,
 * most for tiles nobody had scrolled to. ThumbTile asks only once this says
 * its tile is close.
 *
 * ONE IntersectionObserver for the page, not one per tile, and each element is
 * heard once and then forgotten. With a null root it watches the viewport, and
 * a tile hidden by the scroll box it sits in (the explorer's pane scrolls on
 * its own) does not count as near. Where IntersectionObserver does not exist
 * (an old engine, a test DOM) every element counts as near at once — which is
 * how every tile behaved before.
 */

/** How far outside the viewport a tile already counts as near: a thumbnail
 *  should be on its way before the tile scrolls in, not after. */
const NEAR = '300px';

let io: IntersectionObserver | null = null;
const waiting = new Map<Element, Set<() => void>>();

function observer(): IntersectionObserver {
  if (!io) {
    io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue;
          const cbs = waiting.get(e.target);
          if (!cbs) continue;
          waiting.delete(e.target);
          io?.unobserve(e.target);
          cbs.forEach((cb) => cb());
        }
      },
      // `rootMargin` widens the viewport only: a tile in the pane's own scroll
      // box is still cut off at that box's edge. `scrollMargin` widens the
      // scroll boxes too (Chromium 120+; an engine that does not know it
      // ignores it, and the picture is asked for as the tile scrolls in).
      { rootMargin: NEAR, scrollMargin: NEAR } as IntersectionObserverInit,
    );
  }
  return io;
}

/** Call `cb` once `el` is near the viewport. Returns a function that cancels
 *  the wait (call it when the element goes away first). */
export function whenNearViewport(el: Element | null | undefined, cb: () => void): () => void {
  if (!el || typeof IntersectionObserver === 'undefined') {
    cb();
    return () => {};
  }
  let cbs = waiting.get(el);
  if (!cbs) {
    cbs = new Set();
    waiting.set(el, cbs);
    observer().observe(el);
  }
  cbs.add(cb);
  return () => {
    const set = waiting.get(el);
    if (!set) return;
    set.delete(cb);
    if (set.size === 0) {
      waiting.delete(el);
      io?.unobserve(el);
    }
  };
}

/** Tests only: drop the shared observer so the next wait builds a fresh one. */
export function __resetNearViewport(): void {
  io?.disconnect();
  io = null;
  waiting.clear();
}
