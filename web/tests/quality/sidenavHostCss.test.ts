// The explorer renders into its host's DOM (no shadow root), so a host page's
// global `button { … }` rule reaches every button the explorer draws. Where a
// core rule leaves a property unsaid, the host's value wins.
//
// ⚠ #53 (2026-09-25): the desktop app styles its own dialogs with
// `button { justify-content: center }`, and `.fe-sidenav__item` said nothing
// about justify-content, so every sidebar entry — Home, Trash, each storage —
// sat centred in its row, on macOS and Windows alike. Measured in a browser
// with the desktop's rule verbatim: icon 57-59px in before, 12px (the padding)
// after.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// Comments out: one of them quotes the host's `button { … }` rule, braces and all.
const css = readFileSync(path.resolve(__dirname, '../../../packages/core/src/styles/base.css'), 'utf8').replace(
  /\/\*[\s\S]*?\*\//g,
  '',
);

function firstBlock(selector: string): string {
  const at = css.indexOf(`${selector} {`);
  if (at < 0) throw new Error(`base.css has no "${selector} {" rule`);
  return css.slice(at, css.indexOf('}', at));
}

describe('sidebar entries hold their own alignment', () => {
  it('.fe-sidenav__item says justify-content itself', () => {
    expect(firstBlock('.fe-sidenav__item')).toMatch(/justify-content:\s*flex-start/);
  });
});
