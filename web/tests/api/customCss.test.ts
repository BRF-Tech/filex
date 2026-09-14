// gorunum:v1 — the operator stylesheet injector.
//
// What is actually load-bearing here: ONE element, carrying the CSS as TEXT
// (never markup), and staying the last stylesheet in <head> — because a theme
// override ties on specificity with the declaration it overrides and source
// order is what breaks the tie.
import { describe, it, expect, beforeEach } from 'vitest';

import { applyCustomCss, removeCustomCss } from '@/lib/customCss';

const marker = 'style[data-filex-custom]';

function sheets(): Element[] {
  return Array.from(document.head.querySelectorAll(marker));
}

describe('custom CSS injection', () => {
  beforeEach(() => {
    removeCustomCss();
    document.head.innerHTML = '';
  });

  it('injects one marked style element carrying the CSS as text', () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    expect(sheets()).toHaveLength(1);
    expect(sheets()[0].textContent).toBe('.fe { --fe-primary: #ff0000; }');
  });

  it('replaces the same element instead of stacking a second one', () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    applyCustomCss('.fe { --fe-primary: #00ff00; }');
    expect(sheets()).toHaveLength(1);
    expect(sheets()[0].textContent).toBe('.fe { --fe-primary: #00ff00; }');
  });

  it('removes the element when the setting is cleared', () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    applyCustomCss('   ');
    expect(sheets()).toHaveLength(0);
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    applyCustomCss(null);
    expect(sheets()).toHaveLength(0);
  });

  it('never parses the value as markup', () => {
    // textContent, not innerHTML: whatever this is, it is a string inside a
    // style element and no element is created from it.
    applyCustomCss('.fe { --fe-primary: #ff0000; } /* <img src=x onerror=1> */');
    expect(document.head.querySelectorAll('img')).toHaveLength(0);
    expect(sheets()[0].childElementCount).toBe(0);
  });

  it('stays last when a stylesheet is added to <head> after it', async () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    expect(document.head.lastElementChild).toBe(sheets()[0]);

    // A lazily loaded route's CSS chunk — injected long after boot by both the
    // dev server and the production build.
    const late = document.createElement('style');
    late.textContent = '.fe { --fe-primary: #0000ff; }';
    document.head.appendChild(late);
    expect(document.head.lastElementChild).toBe(late);

    // MutationObserver callbacks are microtasks.
    await Promise.resolve();
    await new Promise((r) => setTimeout(r, 0));
    expect(document.head.lastElementChild).toBe(sheets()[0]);
    expect(sheets()).toHaveLength(1);
  });
});
