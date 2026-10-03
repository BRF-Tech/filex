// #74 - the share page and the sign-in page follow the operator's theme.
//
// The owner's rule (2026-10-01): a theme may define those two pages itself;
// if it does, they wear what it says, and if it does not, they take the
// theme's own tones. The stock theme carries today's pages as its own
// definition, so the default look does not move.
//
// What was wrong: the share page's ground and card were hexes of their own
// (`.fe.fe-ppage`, variables.css) that no theme reached - an operator theme
// painted the page's text and buttons and left the stock greys under them -
// and the operator's validator refused any token for those pages, so a theme
// could not say otherwise.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  DERIVED_PUBLIC_PAGE,
  PAGE_TOKENS,
  PUBLIC_PAGE_TOKENS,
  THEMES,
  DEFAULT_THEME_ID,
  generateThemeCss,
  publicPageTokens,
  type ThemeDef,
} from '@brftech/filex-core/src/lib/themes';
import {
  PAGE_TOKEN_FIELDS,
  STOCK_PAGES_DARK,
  STOCK_PAGES_LIGHT,
  draftFromStored,
  draftToTokens,
  newDraft,
} from '@/lib/themeTokens';

const ROOT = path.resolve(__dirname, '../../..');
const VARIABLES = path.join(ROOT, 'packages/core/src/styles/variables.css');
const THEMES_GO = path.join(ROOT, 'backend/internal/api/handlers/themes.go');

const stock = THEMES.find((t) => t.id === DEFAULT_THEME_ID)!;

/** The declarations of the FIRST rule whose selector list is exactly `sel`. */
function block(css: string, sel: string): string {
  const at = css.indexOf(`${sel}{`);
  if (at < 0) return '';
  const open = at + sel.length + 1;
  return css.slice(open, css.indexOf('}', open));
}

/** A complete palette with the page tokens it is given. */
function theme(id: string, light: Record<string, string> = {}, dark: Record<string, string> = {}): ThemeDef {
  const night = THEMES.find((t) => t.id === 'night')!;
  return { id, name: id, light: { ...night.light, ...light }, dark: { ...night.dark, ...dark } };
}

describe('the stock theme defines today’s pages', () => {
  it('its share page is the one variables.css draws, light and dark', () => {
    const css = readFileSync(VARIABLES, 'utf8');
    const light = css.slice(css.indexOf('.fe.fe-ppage {'), css.indexOf('.fe.fe-ppage.fe--theme-dark'));
    const dark = css.slice(css.indexOf('.fe.fe-ppage.fe--theme-dark'), css.indexOf('@media (prefers-color-scheme: dark) {\n  .fe.fe-ppage'));
    for (const token of PUBLIC_PAGE_TOKENS) {
      expect(light, token).toMatch(new RegExp(`${token}:\\s*${stock.light[token]};`));
      expect(dark, token).toMatch(new RegExp(`${token}:\\s*${stock.dark[token]};`));
    }
  });

  it('its sign-in page is the palette’s sunken ground and surface, as it always was', () => {
    expect(stock.light['--fe-login-ground']).toBe(stock.light['--fe-bg-elev']);
    expect(stock.light['--fe-login-card']).toBe(stock.light['--fe-bg']);
    expect(stock.dark['--fe-login-ground']).toBe(stock.dark['--fe-bg-elev']);
    expect(stock.dark['--fe-login-card']).toBe(stock.dark['--fe-bg']);
    const css = readFileSync(VARIABLES, 'utf8');
    expect(css).toMatch(/--fe-login-ground: var\(--fe-bg-elev\);/);
    expect(css).toMatch(/--fe-login-card: var\(--fe-bg\);/);
  });

  it('the theme editor starts “colours of their own” from the same values', () => {
    for (const { key } of PAGE_TOKEN_FIELDS) {
      expect(STOCK_PAGES_LIGHT[key], key).toBe(stock.light[key]);
      expect(STOCK_PAGES_DARK[key], key).toBe(stock.dark[key]);
    }
  });
});

describe('a theme that defines its pages gets them', () => {
  const own = theme(
    'custom:acme',
    { '--fe-ppage-ground-1': '#f1e9e4', '--fe-ppage-ground-2': '#e4d6cd', '--fe-ppage-card': '#fffdfb', '--fe-login-card': '#fffefc' },
    { '--fe-ppage-card': '#2a2220' },
  );
  const css = generateThemeCss(own);

  it('the share page’s own values, under the page’s own selectors (they outrank `:root,.fe`)', () => {
    const light = block(css, '.fe.fe-ppage');
    expect(light).toContain('--fe-ppage-ground-1:#f1e9e4;');
    expect(light).toContain('--fe-ppage-ground-2:#e4d6cd;');
    expect(light).toContain('--fe-ppage-card:#fffdfb;');
    const dark = block(
      css,
      ".fe.fe-ppage.fe--theme-dark,:root[data-theme='dark'] .fe.fe-ppage,:root.dark .fe.fe-ppage,.dark .fe.fe-ppage",
    );
    expect(dark).toContain('--fe-ppage-card:#2a2220;');
    // What the dark variant leaves out is worked out, not left stock.
    expect(dark).toContain(`--fe-ppage-ground-1:${DERIVED_PUBLIC_PAGE.dark['--fe-ppage-ground-1']};`);
  });

  it('the page tokens are not written where they would lose (`:root,.fe` is 0,1,0 against the page’s 0,2,0)', () => {
    expect(block(css, ':root,.fe')).not.toContain('--fe-ppage');
  });

  it('the sign-in page’s own value rides with the palette', () => {
    expect(block(css, ':root,.fe')).toContain('--fe-login-card:#fffefc;');
  });
});

describe('a theme that does not define its pages gets them in its own tones', () => {
  const night = THEMES.find((t) => t.id === 'night')!;
  const css = generateThemeCss(night);

  it('the share page is mixed from the theme’s own surface and ink, not the stock greys', () => {
    const light = block(css, '.fe.fe-ppage');
    for (const token of PUBLIC_PAGE_TOKENS) {
      expect(light, token).toContain(`${token}:${DERIVED_PUBLIC_PAGE.light[token]};`);
      expect(light, token).not.toContain(stock.light[token]);
    }
    expect(light).toMatch(/var\(--fe-bg\)/);
    expect(css).toContain(
      `@media (prefers-color-scheme: dark){.fe.fe-ppage:not(.fe--theme-light){${Object.entries(publicPageTokens(night, 'dark'))
        .map(([k, v]) => `${k}:${v};`)
        .join('')}}}`,
    );
  });

  it('the sign-in page needs nothing written: its defaults ARE the palette’s tokens', () => {
    expect(css).not.toContain('--fe-login-');
  });

  it('a value the theme gives as blank counts as not given', () => {
    const blank = theme('custom:blank', { '--fe-ppage-card': '  ' });
    expect(publicPageTokens(blank, 'light')['--fe-ppage-card']).toBe(DERIVED_PUBLIC_PAGE.light['--fe-ppage-card']);
  });
});

describe('the operator’s editor and the server agree', () => {
  it('the editor’s page fields are core’s PAGE_TOKENS', () => {
    expect(PAGE_TOKEN_FIELDS.map((f) => f.key).sort()).toEqual([...PAGE_TOKENS].sort());
  });

  it('…and the server allowlist’s themePageColorTokens', () => {
    const go = readFileSync(THEMES_GO, 'utf8');
    const m = go.match(/var themePageColorTokens = \[\]string\{([\s\S]*?)\n\}/);
    expect(m, 'themePageColorTokens not found in handlers/themes.go').toBeTruthy();
    const goKeys = Array.from(m![1].matchAll(/"([^"]+)"/g)).map((x) => x[1]);
    expect(goKeys.sort()).toEqual([...PAGE_TOKENS].sort());
  });

  it('a theme with pages of its own stores them, and opens again with them', () => {
    const draft = newDraft();
    draft.key = 'acme';
    draft.name = 'Acme';
    draft.pages.own = true;
    draft.pages.light['--fe-ppage-card'] = '#fffdfb';
    draft.pages.dark['--fe-login-ground'] = '#201a18';
    const tokens = draftToTokens(draft);
    expect(tokens.light['--fe-ppage-card']).toBe('#fffdfb');
    expect(tokens.dark['--fe-login-ground']).toBe('#201a18');
    for (const { key } of PAGE_TOKEN_FIELDS) {
      expect(tokens.light[key], key).toBeTruthy();
      expect(tokens.dark[key], key).toBeTruthy();
    }
    const back = draftFromStored({ key: 'acme', name: 'Acme', ...tokens });
    expect(back.pages.own).toBe(true);
    expect(back.pages.light['--fe-ppage-card']).toBe('#fffdfb');
    expect(back.pages.dark['--fe-login-ground']).toBe('#201a18');
  });

  it('a theme that follows its palette stores no page token at all', () => {
    const tokens = draftToTokens(newDraft());
    for (const key of PAGE_TOKENS) {
      expect(tokens.light[key], key).toBeUndefined();
      expect(tokens.dark[key], key).toBeUndefined();
    }
    const back = draftFromStored({ key: 'x', name: 'X', ...tokens });
    expect(back.pages.own).toBe(false);
  });
});
