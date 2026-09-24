// A teleported popup has to be above whatever opened it — `popupLayer`.
//
// ⚠⚠ The bug this locks. `ContextMenu` teleports to `<body>` so no ancestor
// with `overflow: hidden` can clip it, and in doing so it leaves that
// ancestor's PLACE in the stack behind: the menu's backdrop is `z-index: 80`,
// the explorer's Connections / API-keys overlay is `z-index: 130`, so a row's
// Actions menu opened inside that overlay was painted underneath it. The owner
// read that as the menu not opening at all.
//
// ⚠ The browser measurement is e2e/tests/128-dialog-row-menu.spec.ts and it is
// the one that speaks for the user (`elementFromPoint` at the menu's centre).
// This file is the arithmetic underneath it, which a browser run is a slow and
// indirect way to pin: that an UNRAISED chain leaves the base layer alone (so
// the orderings the stylesheet sets on purpose — the onboarding tour above the
// filter popovers — survive), that a raised ancestor lifts the popup one above
// itself, that the HIGHEST ancestor wins, and that a `z-index` on a `static`
// element is correctly ignored, because it is inert in CSS too.
import { describe, expect, it, afterEach } from 'vitest';

import { POPUP_BASE_Z, popupLayer } from '@brftech/filex-core/src/lib/popupLayer';

/** Build `<div>…<div id="opener">` nesting, outermost first, and return the leaf. */
function chain(...styles: Array<Partial<CSSStyleDeclaration> | null>): HTMLElement {
  let parent: HTMLElement = document.body;
  let leaf = document.body;
  for (const style of styles) {
    const el = document.createElement('div');
    if (style) Object.assign(el.style, style);
    parent.appendChild(el);
    parent = el;
    leaf = el;
  }
  return leaf;
}

afterEach(() => {
  document.body.innerHTML = '';
});

describe('popupLayer', () => {
  it('leaves the base layer alone when nothing in the chain is raised', () => {
    const opener = chain({}, {}, {});
    expect(popupLayer(opener)).toBe(POPUP_BASE_Z);
  });

  it('sits one above a positioned ancestor that outranks the base layer', () => {
    // `.fe-overlay` — position: fixed, z-index: 130.
    const opener = chain({ position: 'fixed', zIndex: '130' }, {}, {});
    expect(popupLayer(opener)).toBe(131);
  });

  it('takes the HIGHEST ancestor, not the nearest', () => {
    const opener = chain(
      { position: 'fixed', zIndex: '130' },
      { position: 'relative', zIndex: '2' },
      {},
    );
    expect(popupLayer(opener)).toBe(131);
  });

  it('ignores a z-index on a static element, exactly as CSS does', () => {
    // The pinned Actions cell carries `z-index: 1` — and when it is not
    // positioned that number means nothing. Honouring it would push every menu
    // in the product onto a layer it did not ask for.
    const opener = chain({ zIndex: '900' }, {});
    expect(popupLayer(opener)).toBe(POPUP_BASE_Z);
  });

  it('never drops BELOW the base layer for a low positioned ancestor', () => {
    const opener = chain({ position: 'sticky', zIndex: '2' }, {});
    expect(popupLayer(opener)).toBe(POPUP_BASE_Z);
  });

  it('takes the best answer from several candidate openers', () => {
    // The caller passes two imperfect clues (the focused element, the element
    // under the pointer). One being wrong must not lose the other's answer.
    const inDialog = chain({ position: 'fixed', zIndex: '130' }, {});
    const looseElsewhere = chain({});
    expect(popupLayer([looseElsewhere, inDialog])).toBe(131);
    expect(popupLayer([null, undefined, inDialog])).toBe(131);
  });

  it('answers the base layer for no opener at all', () => {
    expect(popupLayer(null)).toBe(POPUP_BASE_Z);
    expect(popupLayer([])).toBe(POPUP_BASE_Z);
  });

  it('honours an explicit base', () => {
    expect(popupLayer(chain({}), 5)).toBe(5);
    expect(popupLayer(chain({ position: 'fixed', zIndex: '40' }), 5)).toBe(41);
  });
});
