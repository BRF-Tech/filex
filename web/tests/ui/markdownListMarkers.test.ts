// Rendered markdown keeps its bullets and its numbers, whoever hosts it.
//
// ⚠ Found by the v0.41.0 screenshot pass (2026-09-14) and measured in the admin
// SPA's markdown viewer: every <li> computed `list-style-type: none`, so a
// bulleted list rendered as indented loose lines and a numbered list lost its
// numbers — "1. back up, 2. upgrade" became two unordered lines. The viewer's
// rules set an indent and left the marker to the page; the SPA's Tailwind
// preflight (`ol, ul { list-style: none }`) removes it, while the shadow-DOM
// embed, which has no preflight, showed the same file correctly. jsdom applies
// neither stylesheet, so the rule itself is what is pinned: the viewer states
// the marker for both list kinds, on both markdown surfaces.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CSS = readFileSync(path.resolve(__dirname, '../../../packages/core/src/styles/base.css'), 'utf8').replace(/\r\n/g, '\n');

/** The declarations of every rule whose selector list names `selector` exactly. */
function declarationsFor(selector: string): string {
  const out: string[] = [];
  const noComments = CSS.replace(/\/\*[\s\S]*?\*\//g, '');
  for (const m of noComments.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const selectors = m[1].split(',').map((s) => s.trim());
    if (selectors.includes(selector)) out.push(m[2]);
  }
  return out.join(';');
}

describe('markdown list markers', () => {
  for (const surface of ['.fe-preview__md', '.filex-viewer-ipynb__md']) {
    it(`${surface} ul draws a bullet`, () => {
      expect(declarationsFor(`${surface} ul`)).toMatch(/list-style(-type)?:\s*disc/);
    });
    it(`${surface} ol draws a number`, () => {
      expect(declarationsFor(`${surface} ol`)).toMatch(/list-style(-type)?:\s*decimal/);
    });
    it(`${surface} lists are indented enough for the marker to show`, () => {
      expect(declarationsFor(`${surface} ul`) + declarationsFor(`${surface} ol`)).toMatch(/padding-left:\s*2em/);
    });
  }
});
