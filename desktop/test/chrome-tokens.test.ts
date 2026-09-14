// gorunum:v2-desktop — the shell's chrome is drawn with the product's tokens,
// and it stays that way.
//
// Run:  node --experimental-strip-types --test desktop/test/chrome-tokens.test.ts
//
// Why a test and not a code review: the desktop shell is two hand-written HTML
// pages with their own <style> blocks, so there is no build step, no linter and
// no component boundary between "the product's look" and "whatever colour was
// to hand". It drifted exactly that way once already — measured on 2026-09-12,
// the shell carried a second palette (--accent #2f6df6 against the product's
// --fe-primary #2f6ceb, --danger #c0392b against #dc2626), a 14px system-ui
// type scale against the explorer's 13px Inter, and 37px buttons where the
// product's control heights are 28/34/40. Near-miss values are the worst kind:
// they do not read as a different design, they read as a printing error.
//
// The two rules below are the whole contract.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const UI = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'ui');
const read = (name: string) => fs.readFileSync(path.join(UI, name), 'utf8');

/**
 * The <style> blocks of a page, with the surrounding markup AND the CSS
 * comments dropped.
 *
 * ⚠ The comments have to go. Half of them cite the measured value they
 * replaced ("was #171c26, which sits between --fe-bg and --fe-bg-elev"), and a
 * scan that counted those would force the explanation out of the file to keep
 * the test green — punishing exactly the comment that makes the rule
 * defensible.
 */
function styleBlocks(html: string): string {
  return [...html.matchAll(/<style>([\s\S]*?)<\/style>/g)]
    .map((m) => m[1].replace(/\/\*[\s\S]*?\*\//g, ' '))
    .join('\n');
}

/**
 * Colours a chrome stylesheet is allowed to state literally, and why. Anything
 * NOT on this list has to come from a `--fe-*` token, because a literal cannot
 * be reached by a theme — which is the whole failure this guards against.
 *
 * ⚠ Adding a line here is a decision, not a formality. "It is only one
 * colour" is how the second palette got in.
 */
const ALLOWED: { value: string; why: string }[] = [
  {
    value: '#ffffff',
    why:
      'the plate a SERVER LOGO is drawn on (.avatar--brand, .boot__mark--brand). ' +
      'Brand artwork is authored for white pages; a dark-on-dark mark on the ' +
      'palette ground renders as an empty square.',
  },
  {
    value: 'rgba(17, 24, 39, .55)',
    why:
      "the dialog backdrop — the SAME literal the product's own user-settings " +
      'dialog uses (web/src/components/UserSettingsModal.vue, .fx-us-dialog::backdrop). ' +
      'A scrim sits outside the palette on purpose: it has to darken every ' +
      'theme by the same amount, and one derived from --fe-text would vanish ' +
      'on the dark variants.',
  },
];

test('the chrome states no colour of its own', () => {
  for (const page of ['app.html', 'index.html']) {
    const css = styleBlocks(read(page));
    // Hex, rgb()/rgba(), hsl()/hsla() — every way to write a colour that a
    // theme cannot reach.
    const literals = [
      ...css.matchAll(/#[0-9a-fA-F]{3,8}\b/g),
      ...css.matchAll(/\b(?:rgba?|hsla?)\([^)]*\)/g),
    ].map((m) => m[0]);

    const unexplained = literals.filter((v) => !ALLOWED.some((a) => a.value === v.toLowerCase()));
    assert.deepEqual(
      unexplained,
      [],
      `${page} states ${unexplained.length} colour(s) the product's palette cannot reach: ` +
        `${[...new Set(unexplained)].join(', ')}. Use a --fe-* token, or add the value to ` +
        `ALLOWED in this file with the reason it cannot be one.`,
    );
  }
});

test('…and it sizes its controls from the product\u2019s three heights', () => {
  const css = styleBlocks(read('app.html'));
  // A settings surface full of buttons whose heights were typed by hand is
  // what 37px was. Every height in the chrome is a token or a token fallback.
  const heights = [...css.matchAll(/(?:^|[;{\s])height:\s*([^;]+);/g)].map((m) => m[1].trim());
  const literal = heights.filter(
    (h) =>
      !h.includes('--fe-h-') &&
      // Sizes that are genuinely not "a control": avatars, dots, the mark, the
      // scrollbar, the indeterminate bar, the switch and its knob, hairlines,
      // 100%/auto/inherit.
      !/^(100%|auto|inherit|1px|3px|10px|12px|16px|18px|20px|26px|28px|32px|40px|56px|180px|min\()/.test(h),
  );
  assert.deepEqual(literal, [], `hand-typed control heights in app.html: ${literal.join(', ')}`);
});

/**
 * The brand mark — the one thing in these files that is NOT in a <style>
 * block, and therefore invisible to the scan above.
 *
 * Both pages build the logo as an inline SVG string in JavaScript, so the
 * desktop app carries two copies of a mark whose source of truth is
 * `web/src/components/LogoMark.vue`. That is how a rebrand ships halfway:
 * gorunum:v1 moved the logo from an indigo→violet gradient (#6366f1 → #4338ca)
 * to flat product blue, the web app was updated, and these two copies would
 * have stayed violet with nothing failing.
 *
 * The rule is the one LogoMark.vue follows: paint from `--fe-primary`, with the
 * literal only as a floor. `fill` has no inherited value, so a context with no
 * tokens would paint the mark BLACK — which is why the fallback has to be
 * there and why it has to be the right blue.
 */
const RETIRED_BRAND = ['#6366f1', '#4338ca', '#4f46e5'];

test('both copies of the brand mark paint from the palette', () => {
  for (const page of ['app.html', 'index.html']) {
    const src = read(page);
    for (const dead of RETIRED_BRAND) {
      assert.equal(
        src.toLowerCase().includes(dead),
        false,
        `${page} still carries the retired brand colour ${dead}. The mark is flat ` +
          `product blue now — fill="var(--fe-primary, #2f6ceb)", the same as ` +
          `web/src/components/LogoMark.vue.`,
      );
    }
    assert.match(
      src,
      /fill="var\(--fe-primary, #2f6ceb\)"/,
      `${page} does not paint its logo from --fe-primary. A mark with a hardcoded ` +
        `fill cannot follow the palette, and cannot follow the next rebrand either.`,
    );
    // A gradient is what the flat mark replaced; its machinery should be gone
    // with it rather than left behind pointing at nothing.
    assert.equal(
      /linearGradient/.test(src),
      false,
      `${page} still builds a gradient for the mark.`,
    );
  }
});

/**
 * The product mark in the top bar — and the two ways it goes blank silently.
 *
 * gorunum:v3-shell put a brand corner at the far left of the explorer's top
 * bar. A host that mounts `<filex-explorer>` CANNOT fill it with a slot — Vue
 * projects light DOM into a custom element only through a native `<slot>`
 * inside a shadow root, and this element is `shadowRoot: false` on purpose
 * (its whole look is one global stylesheet). Measured from this window before
 * `config.brand` existed: the trailing cluster's slot counted 0 children no
 * matter what was placed in the element. So the only way to fill it is
 * `config.brand`, and if that line is ever dropped the corner is simply empty
 * — no error, no warning, in the app we ship.
 *
 * The second way is subtler and cost a measurement to find: the mark is an
 * `<img src>`, and an SVG loaded as an IMAGE is its own document. It cannot see
 * this page's `--fe-primary`, and a data: URI is fragment-delimited by `#`, so
 * a `var()` fill or a raw-hex URI either paints the wrong blue or nothing.
 */
test('the top bar gets a mark it can actually draw', () => {
  const app = read('app.html');
  assert.match(
    app,
    /brand:\s*\{\s*name:\s*'filex',\s*markUrl:\s*brandMarkUrl\(\)\s*\}/,
    'app.html no longer passes `config.brand` to the explorer. A host cannot fill ' +
      'the #brand slot in a web component, so without this the window opens with a ' +
      'blank top-left corner and nothing reports it.',
  );
  // base64, because the colour in the SVG is a `#` literal.
  assert.match(app, /data:image\/svg\+xml;base64,/);
  // xmlns, because a standalone SVG document without it does not render.
  assert.match(app, /<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg" /);
  // …and the fill is resolved, not left as a var() the image cannot see.
  assert.match(app, /getPropertyValue\('--fe-primary'\)/);
});

/**
 * The one surface the product's design can never reach — and the one that can
 * stop the app dead.
 *
 * `dialog.showErrorBox` is SYNCHRONOUS and modal on the main process: it blocks
 * whatever the renderer is awaiting until a human clicks OK, and it draws an OS
 * box that no palette, token or theme touches. `main.ts` has said so in a
 * comment since the drag-out work — and four call sites did it anyway.
 *
 * Measured 2026-09-12: `keep-e2e` drives the "a root inside the current one is
 * refused" path, which raised one of them. The suite stopped at check 21 and
 * left an "Error" window on the operator's desktop that nothing in the run
 * could dismiss, on every attempt.
 *
 * `tellUser()` is the replacement the file already had: it logs, it honours the
 * same `FILEX_NO_BROWSER` hook as `pickDirectory()` and `askChoice()`, and it
 * does not freeze the process.
 */
test('no native dialog freezes the main process', () => {
  const main = fs.readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'main.ts'),
    'utf8',
  );
  // The call, not the word: the comments explaining the rule must stay.
  const calls = [...main.matchAll(/dialog\.showErrorBox\s*\(/g)];
  assert.equal(
    calls.length,
    0,
    `main.ts calls dialog.showErrorBox ${calls.length}×. Use tellUser('error', …) — ` +
      `it logs, it is skipped under FILEX_NO_BROWSER so an automated run cannot ` +
      `be wedged by it, and it does not block the main process.`,
  );
});

/**
 * Rule two — the one the owner set for this whole wave: no two controls doing
 * the same job.
 *
 * The theme mode, the palette and the row density are the PERSON's
 * preferences. Exactly one control owns each, and it is not in this app's
 * chrome: they live in the file list's "⋯" menu (Theme, Compact view), which
 * is `packages/core`'s and is on screen in this window — measured through the
 * running app, not assumed. The shell READS `filex.thememode` so the chrome can
 * follow the file list; the moment it starts WRITING one of these keys there
 * are two answers to one question again.
 */
test('the shell follows the product\u2019s preferences and never writes them', () => {
  const app = read('app.html');
  for (const key of ['filex.thememode', 'filex.palette', 'filex.density', 'filex.timezone']) {
    // `setItem('filex.thememode', …)` in any spacing, on either quote.
    const writes = new RegExp(`setItem\\(\\s*['"\`]${key.replace('.', '\\.')}['"\`]`);
    assert.equal(
      writes.test(app),
      false,
      `app.html writes ${key}. That preference already has exactly one control, in the ` +
        `file list's "⋯" menu; a second writer here is a second answer to one question.`,
    );
  }
  // …and it does read the mode, which is the half that has to keep working.
  assert.match(app, /getItem\(['"]filex\.thememode['"]\)/);
});
