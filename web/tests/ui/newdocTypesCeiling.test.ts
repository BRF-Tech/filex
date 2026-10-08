// New document's type list is bounded by the window, not by a number.
//
// ⚠ 0.52.0 and 0.53.0, apps/app-new-document-1440.png: the list carried
// `max-height: min(46vh, 420px)`. The 420px arm was written for a ~370px grid,
// to keep a scrollbar off a normal desktop dialog; the grid then grew (ODF
// tiles, an Apps group) and the arm became a ceiling of its own, so the list
// scrolled in a window of any height - picking the app's tile scrolled the
// office tiles half out of the box, and the scene's 1400px window could not
// help. The ceiling exists for short windows (Name and Location must stay in
// view on a phone), so it is a share of the window and nothing else.
//
// A jsdom test cannot measure a scroll box; e2e/shots/apps.mjs measures the
// real list before its picture and fails on one that scrolls.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CSS = readFileSync(path.resolve(__dirname, '../../../packages/core/src/styles/base.css'), 'utf8').replace(/\r\n/g, '\n');

describe('New document: the type list', () => {
  it('scrolls only when the window is too short for it - no fixed pixel ceiling', () => {
    const rules = [...CSS.matchAll(/(^|\n)\.fe-newdoc__types\s*\{([^}]*)\}/g)].map((m) => m[2]);
    expect(rules.length, 'core base.css has no `.fe-newdoc__types` rule').toBeGreaterThan(0);
    const heights = rules.flatMap((r) => [...r.matchAll(/max-height:\s*([^;]+);/g)].map((m) => m[1].trim()));
    expect(heights.length, 'the type list lost its ceiling - on a phone Name and Location fall below the fold').toBeGreaterThan(0);
    for (const h of heights) {
      expect(h, 'the ceiling is a share of the window').toMatch(/vh/);
      expect(h, 'a pixel arm caps the list in a window of any height').not.toMatch(/\dpx/);
    }
  });
});
