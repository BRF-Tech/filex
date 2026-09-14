/**
 * Every theme has to declare the same tokens as every other theme.
 *
 * This is the guard for a bug whose whole nature is that nothing notices.
 * `--fe-primary-soft` was declared by `packages/core/src/styles/variables.css`
 * and read by nine rules in base.css — the active tab of a segmented control,
 * a filter chip that is set, an active toolbar icon button, the
 * advanced-search tabs — and it appeared in **no theme map at all**. Picking
 * Forest, Amber or Lilac therefore repainted everything except the one token
 * that marks "this is the selected thing", which stayed stock blue and clashed
 * with the palette the user had just chosen. `--fe-border-soft` (ten rules,
 * the quieter separators inside panels and dialogs) was missing the same way.
 *
 * Nothing errored. `var(--fe-primary-soft)` simply resolved to the stock
 * value that variables.css still had on `:root`, because a theme only
 * overrides the keys it names. The gallery preview card looked right, since it
 * is painted from the same incomplete map. The only way to see it was to pick
 * a theme and look at a selected chip.
 *
 * So: the shared set is what the `default` map declares — and the default map
 * is itself pinned to variables.css by `themeContrast.test.ts`, which closes
 * the loop: a token added to the stock palette has to be added to the default
 * map (that test), and then to every other theme (this one).
 *
 * Extras ON TOP of the shared set are allowed and listed below with reasons —
 * High Contrast's hard shadows, Terminal's monospace face. They are a theme
 * saying something extra, not a theme missing something.
 */
import { describe, expect, it } from 'vitest';
import { THEMES, DEFAULT_THEME_ID, type ThemeTokenMap } from '@brftech/filex-core/src/lib/themes';

/** Tokens a theme may declare beyond the shared set, and why. */
const ALLOWED_EXTRAS: Record<string, string> = {
  '--fe-shadow': 'High Contrast replaces blur with a hard 1px outline',
  '--fe-shadow-sm': 'High Contrast replaces blur with a hard 1px outline',
  '--fe-font': 'Terminal ships a monospace face as part of the palette',
};

const stock = THEMES.find((t) => t.id === DEFAULT_THEME_ID);

function keys(map: ThemeTokenMap): string[] {
  return Object.keys(map).sort();
}

describe('theme token parity', () => {
  it('the default theme is a sane baseline', () => {
    // A guard for the guard: if the registry ever stops exporting the stock
    // theme, every assertion below would pass by comparing against nothing.
    expect(stock, 'the default theme is missing from THEMES').toBeTruthy();
    expect(keys(stock!.light).length).toBeGreaterThan(10);
    expect(keys(stock!.light)).toEqual(keys(stock!.dark));
    expect(THEMES.length).toBeGreaterThan(3);
  });

  it('every theme declares every token the stock palette declares, in BOTH variants', () => {
    const shared = keys(stock!.light);
    const missing: string[] = [];
    for (const theme of THEMES) {
      for (const variant of ['light', 'dark'] as const) {
        for (const token of shared) {
          if (!(token in theme[variant])) missing.push(`${theme.id}/${variant} ${token}`);
        }
      }
    }
    expect(
      missing,
      'a theme leaves a token to the stock palette — on that theme the surface ' +
        'painted with it keeps the default blue while everything around it changes',
    ).toEqual([]);
  });

  it('a theme declares nothing beyond the shared set except the documented extras', () => {
    const shared = new Set(keys(stock!.light));
    const strays: string[] = [];
    for (const theme of THEMES) {
      for (const variant of ['light', 'dark'] as const) {
        for (const token of keys(theme[variant])) {
          if (!shared.has(token) && !(token in ALLOWED_EXTRAS)) {
            strays.push(`${theme.id}/${variant} ${token}`);
          }
        }
      }
    }
    // Not pedantry: a token only one theme names is the same defect in
    // reverse — every OTHER theme is then inheriting the stock value for it.
    expect(
      strays,
      'add the token to every theme (and to the stock palette), or to ALLOWED_EXTRAS with the reason',
    ).toEqual([]);
  });

  it('the extras that are allowed are actually used by a theme', () => {
    // An allowlist nobody prunes turns into permission for the next hole.
    const declared = new Set(
      THEMES.flatMap((t) => [...Object.keys(t.light), ...Object.keys(t.dark)]),
    );
    const stale = Object.keys(ALLOWED_EXTRAS).filter((t) => !declared.has(t));
    expect(stale, 'ALLOWED_EXTRAS lists a token no theme declares any more').toEqual([]);
  });

  it('no theme declares a token with an empty value', () => {
    const empties: string[] = [];
    for (const theme of THEMES) {
      for (const variant of ['light', 'dark'] as const) {
        for (const [token, value] of Object.entries(theme[variant])) {
          if (!value || !String(value).trim()) empties.push(`${theme.id}/${variant} ${token}`);
        }
      }
    }
    expect(empties).toEqual([]);
  });
});
