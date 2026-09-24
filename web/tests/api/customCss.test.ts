// gorunum:v1 — the operator stylesheet injector.
//
// What is actually load-bearing here: ONE element, carrying the CSS as TEXT
// (never markup), and staying the last stylesheet in <head> — because a theme
// override ties on specificity with the declaration it overrides and source
// order is what breaks the tie.
import { describe, it, expect, beforeEach } from 'vitest';

import {
  applyCustomCss,
  removeCustomCss,
  suspendCustomCss,
  resumeCustomCss,
  isCustomCssSuspended,
} from '@/lib/customCss';

const marker = 'style[data-filex-custom]';

function sheets(): Element[] {
  return Array.from(document.head.querySelectorAll(marker));
}

describe('custom CSS injection', () => {
  beforeEach(() => {
    resumeCustomCss();
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

// tema:v1 — suspension.
//
// ⚠⚠ THE GUARANTEE THAT NOBODY CAN LOCK THEMSELVES OUT. The `@scope` wrapper
// the server puts around the sheet stops an operator selector MATCHING inside
// the admin Appearance panel, but scoping cannot un-apply an inherited or
// ancestor-level property: `:root { display: none }` and `:root { opacity: 0 }`
// blank everything below them, immune subtree included, because neither is a
// question about which elements a rule matches. Nothing a stylesheet can
// express survives the stylesheet not being in the document — so the screen
// that edits and removes the sheet takes it out while it is open.
describe('suspending the operator stylesheet', () => {
  beforeEach(() => {
    resumeCustomCss();
    removeCustomCss();
    document.head.innerHTML = '';
  });

  it('takes the element out of the document and puts the same one back', () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    expect(sheets()).toHaveLength(1);

    suspendCustomCss();
    expect(isCustomCssSuspended()).toBe(true);
    expect(sheets(), 'a suspended sheet must not merely be emptied').toHaveLength(0);

    resumeCustomCss();
    expect(isCustomCssSuspended()).toBe(false);
    expect(sheets()).toHaveLength(1);
    expect(sheets()[0].textContent).toBe('.fe { --fe-primary: #ff0000; }');
  });

  it('does not re-inject while suspended, however the sheet changes', () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    suspendCustomCss();

    // The Appearance screen is editing it; nothing it does may put it back.
    applyCustomCss('.fe { --fe-primary: #00ff00; }');
    expect(sheets()).toHaveLength(0);

    resumeCustomCss();
    expect(sheets()[0].textContent).toBe('.fe { --fe-primary: #00ff00; }');
  });

  it('is idempotent, and resuming a cleared sheet injects nothing', () => {
    applyCustomCss('.fe { --fe-primary: #ff0000; }');
    suspendCustomCss();
    suspendCustomCss();
    resumeCustomCss();
    expect(sheets()).toHaveLength(1);

    suspendCustomCss();
    removeCustomCss();
    resumeCustomCss();
    expect(sheets(), 'a sheet removed while suspended must stay gone').toHaveLength(0);
  });
});
