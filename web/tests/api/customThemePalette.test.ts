// tema:v1 — operator-defined themes, on the browser side.
//
// Three separate things are load-bearing here and none of them is visual:
//
//  1. THE DERIVATIONS REPRODUCE THE STOCK PALETTE. The editor asks for twelve
//     colours and works the rest out. If a derivation drifts, nothing errors —
//     somebody's brand just grows a shade that is slightly wrong, in one
//     variant, on one surface. Feeding the derivations the stock twelve and
//     comparing against variables.css is the cheapest way to notice.
//  2. THE TWO TOKEN LISTS AGREE ACROSS LANGUAGES. The editor's authored list
//     (TypeScript) and the backend's allowlist (Go) are the same set written
//     twice. A token in one and not the other fails silently in opposite
//     directions: refused on save, or never set at all.
//  3. A DELETED THEME FALLS BACK. The whole deletion design is that nothing
//     rewrites anybody's stored choice — every read resolves against the list
//     of themes that exist. This is the client half of that.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, beforeEach } from 'vitest';

import {
  DEFAULT_THEME_ID,
  THEMES,
  allThemes,
  setCustomThemes,
  setTheme,
  themeById,
  themeName,
  useThemeState,
  applyThemeToEl,
} from '@brftech/filex-core';

import {
  AUTHORED_KEYS,
  STOCK_LIGHT,
  STOCK_DARK,
  DEFAULT_FONT,
  contrastWarnings,
  deriveTokens,
  draftFromStored,
  draftToTokens,
  newDraft,
  slugify,
} from '@/lib/themeTokens';

const THEMES_GO = path.resolve(__dirname, '../../../backend/internal/api/handlers/themes.go');

function themeDoc(key: string, name: string, primary: string) {
  const draft = newDraft();
  draft.key = key;
  draft.name = name;
  draft.light['--fe-primary'] = primary;
  draft.dark['--fe-primary'] = primary;
  const tokens = draftToTokens(draft);
  return { id: `custom:${key}`, name, light: tokens.light, dark: tokens.dark };
}

// ─────────────────── 1. the derivations ───────────────────

describe('theme token derivation', () => {
  it('lands on the stock palette when fed the stock twelve', () => {
    const light = deriveTokens(STOCK_LIGHT, { dark: false, radius: '8', font: DEFAULT_FONT });
    const dark = deriveTokens(STOCK_DARK, { dark: true, radius: '8', font: DEFAULT_FONT });

    // ⚠ Close, not identical — and the tolerance is the assertion, not a
    // loophole. The shipped palette was hand-tuned and a couple of its shades
    // carry a tint one mix fraction cannot express, so the derivations land
    // within 6/255 per channel of variables.css. Six is invisible; a
    // derivation that had actually drifted would be tens of steps away.
    const near = (got: string, stock: string, what: string) => {
      const px = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
      const [g, s] = [px(got), px(stock)];
      const delta = Math.max(...[0, 1, 2].map((i) => Math.abs(g[i] - s[i])));
      expect(delta, `${what}: derived ${got}, variables.css says ${stock}`).toBeLessThanOrEqual(6);
    };

    near(light['--fe-bg-hover'], '#f3f4f6', 'light row hover');
    near(light['--fe-bg-selected'], '#eef3ff', 'light selected row');
    near(light['--fe-border-soft'], '#eef0f4', 'light quiet separator');
    near(light['--fe-border-strong'], '#d1d5db', 'light loud separator');

    near(dark['--fe-bg-hover'], '#1f232a', 'dark row hover');
    near(dark['--fe-bg-selected'], '#1c2740', 'dark selected row');
    near(dark['--fe-border-soft'], '#262a32', 'dark quiet separator');
    near(dark['--fe-border-strong'], '#3d444f', 'dark loud separator');

    // The metrics ARE exact — they are arithmetic, not colour.
    expect(light['--fe-radius-sm']).toBe('6px');
    expect(light['--fe-radius-md']).toBe('10px');
    expect(light['--fe-radius-lg']).toBe('12px');
    // …and so is the success signal, which follows the authored colour.
    expect(light['--fe-keep-ok']).toBe(STOCK_LIGHT['--fe-ok']);
  });

  it('picks the readable ink for a primary button instead of always white', () => {
    // ⚠ This is the derivation with a real bug behind it. variables.css carries
    // a comment saying white on the dark palette's light-blue button measures
    // 3.16:1 against a 4.5 bar, which is why the stock dark value is the page
    // ground and not #ffffff. An operator choosing a pale accent hits exactly
    // that, and has no way to know.
    const light = deriveTokens(STOCK_LIGHT, { dark: false, radius: '8', font: DEFAULT_FONT });
    const dark = deriveTokens(STOCK_DARK, { dark: true, radius: '8', font: DEFAULT_FONT });
    expect(light['--fe-text-on-primary']).toBe('#ffffff');
    expect(dark['--fe-text-on-primary']).toBe(STOCK_DARK['--fe-bg']);

    // A pale accent in light mode flips it the other way.
    const pale = deriveTokens(
      { ...STOCK_LIGHT, '--fe-primary': '#ffe066' },
      { dark: false, radius: '8', font: DEFAULT_FONT },
    );
    expect(pale['--fe-text-on-primary']).toBe(STOCK_LIGHT['--fe-bg']);
  });

  it('clamps a nonsense radius rather than storing something the server refuses', () => {
    const huge = deriveTokens(STOCK_LIGHT, { dark: false, radius: '999', font: DEFAULT_FONT });
    expect(huge['--fe-radius']).toBe('24px');
    const junk = deriveTokens(STOCK_LIGHT, { dark: false, radius: 'abc', font: DEFAULT_FONT });
    expect(junk['--fe-radius']).toBe('8px');
    // The backend's length pattern is `0` or up to two digits plus px.
    expect(huge['--fe-radius']).toMatch(/^(?:0|\d{1,2}(?:\.\d)?px)$/);
  });

  it('warns about an unreadable pairing without refusing it', () => {
    expect(contrastWarnings(deriveTokens(STOCK_LIGHT, { dark: false, radius: '8', font: DEFAULT_FONT })))
      .toEqual([]);

    const bad = deriveTokens(
      { ...STOCK_LIGHT, '--fe-text': '#cccccc' },
      { dark: false, radius: '8', font: DEFAULT_FONT },
    );
    const warnings = contrastWarnings(bad);
    expect(warnings.map((w) => w.labelKey)).toContain('appearance.contrast.text');
  });

  it('slugifies a Turkish name into an ASCII key the server accepts', () => {
    expect(slugify('Acme Bulut')).toBe('acme-bulut');
    // ⚠ İ, ş, ğ, ü, ö, ç have no business in a palette id, but they have every
    // business in the name somebody types. Dropping them would turn
    // "Şirket Güneşi" into "irket-gnei".
    expect(slugify('Şirket Güneşi')).toBe('sirket-gunesi');
    expect(slugify('ÇÖĞÜŞİ')).toBe('cogusi');
    expect(slugify('  ---  ')).toBe('');
  });

  it('round-trips a stored theme back into an editable draft', () => {
    const draft = newDraft();
    draft.key = 'acme';
    draft.name = 'Acme Bulut';
    draft.radius = '14';
    draft.font = 'Brand Sans, sans-serif';
    draft.light['--fe-primary'] = '#aa0055';
    const tokens = draftToTokens(draft);

    const back = draftFromStored({ key: 'acme', name: 'Acme Bulut', ...tokens });
    expect(back.radius).toBe('14');
    expect(back.font).toBe('Brand Sans, sans-serif');
    expect(back.light['--fe-primary']).toBe('#aa0055');
    expect(Object.keys(back.light).sort()).toEqual([...AUTHORED_KEYS].sort());
  });
});

// ─────────────────── 2. the two lists ───────────────────

describe('the editor and the server agree on the token set', () => {
  it('the authored list matches themeColorTokens in handlers/themes.go', () => {
    const go = readFileSync(THEMES_GO, 'utf8');
    const block = go.match(/var themeColorTokens = \[\]string\{([\s\S]*?)\n\}/);
    expect(block, 'themeColorTokens not found — did the Go declaration move?').toBeTruthy();

    const goKeys = Array.from(block![1].matchAll(/"([^"]+)"/g)).map((m) => m[1]);
    expect(goKeys.length).toBeGreaterThan(0);
    expect([...goKeys].sort()).toEqual([...AUTHORED_KEYS].sort());
  });

  it('every token the editor stores is on the server allowlist', () => {
    const go = readFileSync(THEMES_GO, 'utf8');
    const allowed = new Set(Array.from(go.matchAll(/"(--fe-[a-z-]+)"/g)).map((m) => m[1]));
    const stored = draftToTokens(newDraft());
    for (const key of [...Object.keys(stored.light), ...Object.keys(stored.dark)]) {
      expect(allowed.has(key), `${key} is stored by the editor but not allowed by the server`).toBe(
        true,
      );
    }
  });

  it('the built-in ids the server knows are the ids the client ships', () => {
    const go = readFileSync(THEMES_GO, 'utf8');
    const block = go.match(/var builtinThemeIDs = \[\]string\{([\s\S]*?)\n\}/);
    expect(block).toBeTruthy();
    const ids = Array.from(block![1].matchAll(/"([^"]+)"/g)).map((m) => m[1]);
    // BuiltinDefaultThemeID is spelled as a constant, not a literal.
    const goIds = new Set([...ids, DEFAULT_THEME_ID]);
    for (const t of THEMES) {
      expect(goIds.has(t.id), `${t.id} is a shipped palette the server would refuse as a default`)
        .toBe(true);
    }
  });
});

// ─────────────────── 3. the palette list ───────────────────

describe('the palette list gains the instance themes', () => {
  beforeEach(() => {
    setCustomThemes([]);
    localStorage.clear();
    setTheme(DEFAULT_THEME_ID);
  });

  it('lists the built-ins, then the operator themes, and never interleaves them', () => {
    expect(allThemes()).toHaveLength(THEMES.length);

    setCustomThemes([themeDoc('acme', 'Acme Bulut', '#aa0055')]);

    const list = allThemes();
    expect(list).toHaveLength(THEMES.length + 1);
    expect(list.slice(0, THEMES.length).map((t) => t.id)).toEqual(THEMES.map((t) => t.id));
    expect(list[list.length - 1].id).toBe('custom:acme');
  });

  it('shows an operator theme under its own name, not a catalogue key', () => {
    setCustomThemes([themeDoc('acme', 'Acme Bulut', '#aa0055')]);
    const t = (key: string) => `TRANSLATED(${key})`;

    expect(themeName(themeById('custom:acme')!, t)).toBe('Acme Bulut');
    // A built-in still goes through the catalogue.
    expect(themeName(themeById('night')!, t)).toBe('TRANSLATED(theme.name.night)');
  });

  it('paints an operator theme onto an element like any built-in', () => {
    setCustomThemes([themeDoc('acme', 'Acme Bulut', '#aa0055')]);
    const el = document.createElement('div');

    applyThemeToEl(el, 'custom:acme', false);
    expect(el.style.getPropertyValue('--fe-primary')).toBe('#aa0055');

    // ⚠ Switching away must CLEAR it. The clear list is computed from the
    // themes that are installed, not from the built-ins alone — get that wrong
    // and a token only an operator theme declares survives the switch.
    applyThemeToEl(el, DEFAULT_THEME_ID, false);
    expect(el.style.getPropertyValue('--fe-primary')).toBe('');
  });

  it('keeps a custom id across a reload, before the server list has arrived', () => {
    // ⚠ The id is stored in a browser and re-read at module load, long before
    // /api/appearance answers. A boot check that only accepted ids it could
    // already resolve would reset every operator theme on every page load.
    setCustomThemes([themeDoc('acme', 'Acme Bulut', '#aa0055')]);
    setTheme('custom:acme');
    expect(localStorage.getItem('filex.palette')).toBe('custom:acme');

    // Simulate a fresh page: the list is empty again until the fetch lands.
    setCustomThemes([themeDoc('acme', 'Acme Bulut', '#aa0055')]);
    expect(useThemeState().themeId.value).toBe('custom:acme');
  });

  // ⚠⚠ THE DELETION CONTRACT.
  it('puts somebody using a deleted theme back on the default', () => {
    setCustomThemes([themeDoc('acme', 'Acme Bulut', '#aa0055')]);
    setTheme('custom:acme');
    expect(useThemeState().themeId.value).toBe('custom:acme');

    // The operator deletes it; the next payload simply does not contain it.
    setCustomThemes([]);

    expect(useThemeState().themeId.value).toBe(DEFAULT_THEME_ID);
    expect(themeById('custom:acme')).toBeUndefined();
    expect(allThemes().map((t) => t.id)).not.toContain('custom:acme');
    // And the stale key is not left behind to come back on the next reload.
    expect(localStorage.getItem('filex.palette')).toBe(DEFAULT_THEME_ID);
  });

  it('leaves a person on a theme that still exists', () => {
    setCustomThemes([themeDoc('acme', 'Acme', '#aa0055'), themeDoc('other', 'Other', '#00aa55')]);
    setTheme('custom:acme');
    setCustomThemes([themeDoc('acme', 'Acme', '#aa0055')]);
    expect(useThemeState().themeId.value).toBe('custom:acme');
  });
});
