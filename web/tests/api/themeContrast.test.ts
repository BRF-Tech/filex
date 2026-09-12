/**
 * Every palette has to clear the contrast bar its own file claims.
 *
 * `packages/core/src/lib/themes.ts` opens with "All palettes were
 * contrast-checked (WCAG 2.1): text/bg ≥ 7:1, text on elevated/hover/selected
 * surfaces + muted text + text-on-primary ≥ 4.5:1, primary & danger vs bg ≥
 * 3:1 — in BOTH variants." That was a sentence, not a check, and two of the
 * numbers in it were not true when this test was written (2026-09-12):
 *
 *   - the stock dark palette put WHITE on `--fe-primary` #60a5fa: 2.54:1,
 *     against a claimed 4.5. Every primary button in dark mode.
 *   - the `default` theme's preview map still carried #3b82f6 as the primary
 *     after the stock palette was darkened to #2f6fe0 for exactly this
 *     reason, so the gallery card advertised a colour the product had stopped
 *     using.
 *
 * Both are fixed; this file is what keeps them fixed. A new theme that misses
 * the bar fails here rather than shipping and being discovered by somebody
 * who cannot read it.
 *
 * ⚠ Scope: the text/surface tokens only. The `--fe-icon-*` accents are
 * decorative glyph fills (a Drive-yellow folder on white is 1.9:1 and is the
 * right colour anyway), and they are never the only signal — every row that
 * carries one also carries its name in `--fe-text`.
 */
import { describe, expect, it } from 'vitest';
import { THEMES, type ThemeTokenMap } from '@brftech/filex-core/src/lib/themes';

/** Relative luminance, WCAG 2.1 §1.4.3. */
function luminance(hex: string): number {
  const c = hex.replace('#', '');
  const channels = [0, 2, 4]
    .map((i) => parseInt(c.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** [foreground token, background token, minimum ratio] */
const RULES: Array<[string, string, number]> = [
  ['--fe-text', '--fe-bg', 7],
  ['--fe-text-muted', '--fe-bg', 4.5],
  ['--fe-text', '--fe-bg-elev', 4.5],
  ['--fe-text', '--fe-bg-hover', 4.5],
  ['--fe-text', '--fe-bg-selected', 4.5],
  ['--fe-text-on-primary', '--fe-primary', 4.5],
  ['--fe-primary', '--fe-bg', 3],
  ['--fe-danger', '--fe-bg', 3],
];

function check(name: string, map: ThemeTokenMap): string[] {
  const failures: string[] = [];
  for (const [fg, bg, min] of RULES) {
    const f = map[fg];
    const b = map[bg];
    if (!f || !b) {
      failures.push(`${name}: ${fg} or ${bg} is missing from the palette`);
      continue;
    }
    const ratio = contrast(f, b);
    if (ratio < min) {
      failures.push(`${name}: ${fg} on ${bg} = ${ratio.toFixed(2)}:1, needs ${min}:1`);
    }
  }
  return failures;
}

describe('theme palettes', () => {
  it('every theme clears the bar in both variants', () => {
    const failures = THEMES.flatMap((t) => [
      ...check(`${t.id}/light`, t.light),
      ...check(`${t.id}/dark`, t.dark),
    ]);
    expect(failures, 'a palette misses the contrast bar themes.ts documents').toEqual([]);
  });

  it("the default theme's preview map still matches the stock palette", async () => {
    // The `default` entry applies nothing — its maps exist only to draw the
    // gallery card, which is exactly why nothing notices when they drift.
    const { readFileSync } = await import('node:fs');
    const path = await import('node:path');
    const css = readFileSync(
      path.resolve(__dirname, '../../../packages/core/src/styles/variables.css'),
      'utf8',
    );
    const stock = THEMES.find((t) => t.id === 'default')!;

    // `:root` block = the light palette; the class-based dark block follows it.
    const rootBlock = css.slice(css.indexOf(':root {'), css.indexOf('.fe--theme-dark'));
    const darkBlock = css.slice(css.indexOf('.fe--theme-dark'), css.indexOf('@media'));

    const drift: string[] = [];
    for (const [variant, block, map] of [
      ['light', rootBlock, stock.light],
      ['dark', darkBlock, stock.dark],
    ] as const) {
      for (const [token, value] of Object.entries(map)) {
        const m = block.match(new RegExp(`${token}:\\s*(#[0-9a-fA-F]{6})`));
        if (!m) continue; // token the stock palette inherits rather than sets
        if (m[1].toLowerCase() !== value.toLowerCase()) {
          drift.push(`${variant} ${token}: variables.css ${m[1]} vs preview ${value}`);
        }
      }
    }
    expect(drift, 'the gallery advertises a colour the product does not use').toEqual([]);
  });
});
