// The filter row on a phone: the chips scroll, the "⋮" does not (task #264).
//
// The owner, 2026-10-15, at 340px in Turkish: at the end of the chip row "an
// empty box: a stray divider and a ⋮". It was the listing's own "⋮", pinned
// (`position: sticky`) over a row that scrolled sideways, at rest on top of the
// "Boyut" chip: the chip's rounded start edge and the stem of its "B" on one
// side of it, the chip's caret on the other - through the row's end padding,
// which a sticky box does not cover (it stops at the scroller's CONTENT edge).
//
// What this file holds the row to:
//   1. everything but the ⋮ is in ONE track, and the track is what scrolls;
//      the ⋮ is the row's last item outside it, so nothing slides under it;
//   2. the track says which of its edges has more of it waiting
//      (`is-more-start` / `is-more-end`, the fade in base.css), read again on
//      scroll - in a right-to-left row too, where scrollLeft is negative;
//   3. the stylesheet no longer pins the ⋮.
//
// What is PAINTED (the fade, the ⋮ alone at the end, nothing sticking out at
// 340 and 390px) is measured in a browser: e2e/tests/248-narrow-filter-row.spec.ts.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import FilterBar from '@brftech/filex-core/src/components/FilterBar.vue';
import { EMPTY_FILTERS } from '@brftech/filex-core/src/lib/fileFilters';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');

const mounted: VueWrapper[] = [];
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
});

function bar(props: Record<string, unknown> = {}) {
  const w = mount(FilterBar, {
    props: { value: { ...EMPTY_FILTERS }, locale: 'en', ...props },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

/** The track as a browser would measure it: `scrollWidth` of content in a
 *  `clientWidth` box, scrolled `scrollLeft` from its start. */
function measure(el: Element, m: { scrollWidth: number; clientWidth: number; scrollLeft: number }) {
  for (const [k, v] of Object.entries(m)) {
    Object.defineProperty(el, k, { configurable: true, get: () => v });
  }
}

describe('FilterBar — the chips scroll, the ⋮ does not', () => {
  it('chips, the name box and the sort are in the track; the ⋮ is after it, outside', () => {
    const w = bar();
    const row = w.get('[data-testid="filterbar"]');
    const track = w.get('[data-testid="filterbar-track"]');
    expect(track.element.parentElement).toBe(row.element);
    for (const id of ['filter-type', 'filter-people', 'filter-modified', 'filter-size', 'filter-find', 'sort-dir', 'sort-menu']) {
      expect(track.find(`[data-testid="${id}"]`).exists(), id).toBe(true);
    }
    const actions = w.get('[data-testid="listing-actions"]');
    expect(track.element.contains(actions.element), 'the ⋮ is inside the scrolling track').toBe(false);
    expect(actions.element.parentElement).toBe(row.element);
    // The ⋮ is the row's LAST item: nothing after it to show through.
    const items = Array.from(row.element.children).filter((c) => c.tagName !== 'TEMPLATE');
    expect(items[items.length - 1]).toBe(actions.element);
  });

  it('the name box alone (a drive list, Home) is a track with no ⋮', () => {
    const w = bar({ mode: 'find' });
    expect(w.find('[data-testid="filterbar-track"] [data-testid="filter-find"]').exists()).toBe(true);
    expect(w.find('[data-testid="listing-actions"]').exists()).toBe(false);
  });
});

describe('FilterBar — the track says where more of it is waiting', () => {
  const classes = (w: VueWrapper) => w.get('[data-testid="filterbar-track"]').classes();

  it('a row that fits draws no fade at all', async () => {
    const w = bar();
    const track = w.get('[data-testid="filterbar-track"]');
    measure(track.element, { scrollWidth: 300, clientWidth: 300, scrollLeft: 0 });
    await track.trigger('scroll');
    expect(classes(w)).not.toContain('is-more-start');
    expect(classes(w)).not.toContain('is-more-end');
  });

  it('LTR: more at the end, then both, then only at the start', async () => {
    const w = bar();
    const track = w.get('[data-testid="filterbar-track"]');
    measure(track.element, { scrollWidth: 600, clientWidth: 280, scrollLeft: 0 });
    await track.trigger('scroll');
    expect(classes(w)).toContain('is-more-end');
    expect(classes(w)).not.toContain('is-more-start');
    measure(track.element, { scrollWidth: 600, clientWidth: 280, scrollLeft: 150 });
    await track.trigger('scroll');
    expect(classes(w)).toContain('is-more-end');
    expect(classes(w)).toContain('is-more-start');
    measure(track.element, { scrollWidth: 600, clientWidth: 280, scrollLeft: 320 });
    await track.trigger('scroll');
    expect(classes(w)).not.toContain('is-more-end');
    expect(classes(w)).toContain('is-more-start');
  });

  it('RTL: scrollLeft runs negative toward the end and reads the same', async () => {
    const w = bar();
    const track = w.get('[data-testid="filterbar-track"]');
    measure(track.element, { scrollWidth: 600, clientWidth: 280, scrollLeft: -150 });
    await track.trigger('scroll');
    expect(classes(w)).toContain('is-more-end');
    expect(classes(w)).toContain('is-more-start');
    measure(track.element, { scrollWidth: 600, clientWidth: 280, scrollLeft: -320 });
    await track.trigger('scroll');
    expect(classes(w)).not.toContain('is-more-end');
  });

  it('is read again when the row is drawn again (a chip that now names a choice)', async () => {
    const w = bar();
    const track = w.get('[data-testid="filterbar-track"]');
    measure(track.element, { scrollWidth: 280, clientWidth: 280, scrollLeft: 0 });
    await track.trigger('scroll');
    expect(classes(w)).not.toContain('is-more-end');
    // A longer chip label: the content grows without the track being scrolled.
    measure(track.element, { scrollWidth: 420, clientWidth: 280, scrollLeft: 0 });
    await w.setProps({ value: { ...EMPTY_FILTERS, modified: '7d' } });
    await w.vm.$nextTick();
    expect(classes(w)).toContain('is-more-end');
  });
});

describe('the stylesheet — the ⋮ is not pinned over the row any more', () => {
  const css = readFileSync(path.join(REPO, 'packages/core/src/styles/base.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

  it('no rule makes the listing ⋮ sticky, and the row itself does not scroll', () => {
    const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)];
    for (const [, sel, body] of rules) {
      if (/\.fe-filterbar__actions\b/.test(sel)) {
        expect(body, `${sel.trim()} pins the ⋮`).not.toMatch(/position\s*:\s*sticky/);
      }
      if (/(^|,)\s*\.fe-filterbar\s*$/.test(sel.trim()) || /(^|,)\s*\.fe-filterbar\s*(,|$)/.test(sel)) {
        expect(body, `${sel.trim()} scrolls the whole row`).not.toMatch(/overflow-x\s*:\s*auto/);
      }
    }
    // …and the track is what scrolls.
    expect(css).toMatch(/\.fe-filterbar__track\s*\{[^}]*overflow-x\s*:\s*auto/);
  });
});
