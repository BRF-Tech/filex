/**
 * THE PANEL FOLLOWS THE PALETTE — on every route, not just the ones with a
 * file explorer on them.
 *
 * ⚠⚠ The bug, and why it was invisible: a palette is a map of `--fe-*` values
 * that core's `lib/themes` publishes as a singleton `<style
 * data-filex-theme>` mirroring `variables.css`' cascade (`:root,.fe { … }`).
 * That element was written by `FileExplorer.vue`'s own `onMounted`, so it
 * existed on /explore and nowhere else. Users, Storages, Settings, Webhooks,
 * the dashboard — every page without an explorer stayed on the stock blue
 * whatever the person had picked, with no error and nothing to notice beyond
 * the panel ignoring their theme (owner, 2026-09-20: "admin panel seçili renk
 * paletinden etkilenmiyor, etkilenmeli").
 *
 * It is a one-line kind of bug and a whole-app kind of symptom, which is
 * exactly the kind that comes back. Hence a gate on the wiring itself.
 */
import { beforeEach, describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { DEFAULT_THEME_ID, THEME_LS_KEY, setTheme, useThemeState } from '@brftech/filex-core';
import { applyPalette } from '@/lib/palette';

const styleEl = () => document.head.querySelector('style[data-filex-theme]');

/** Let the `watch` inside applyPalette run. */
const settle = () => new Promise<void>((r) => setTimeout(r, 0));

/* ⚠ The <style> element is NOT torn down between cases: `applyPalette` is a
   once-per-document singleton (calling it twice must not install a second
   watcher), so a test that removed the element would find the next call a
   no-op and measure nothing. Each case sets the palette it needs instead. */
beforeEach(() => {
  applyPalette();
});

describe('applyPalette', () => {
  it('publishes the stored palette for the whole document, not just for `.fe`', async () => {
    setTheme('amber');
    await settle();
    const css = styleEl()?.textContent ?? '';
    expect(css, 'no palette stylesheet was written').not.toBe('');
    // ⚠ `:root` is the whole point: `.fe` alone would cover the explorer and
    // leave the admin chrome — the cards, the nav, the tables — on the stock
    // tokens, which is the bug.
    expect(css).toContain(':root');
    expect(css).toContain('--fe-primary');
  });

  it('follows a palette picked later, without a reload', async () => {
    setTheme('amber');
    await settle();
    const first = styleEl()?.textContent ?? '';

    setTheme('forest');
    await settle();
    const second = styleEl()?.textContent ?? '';

    expect(second).not.toBe(first);
    expect(second).toContain('forest');
  });

  it('the stock palette publishes nothing, so a host’s own `--fe-*` overrides still win', async () => {
    setTheme('amber');
    await settle();
    expect(styleEl()?.textContent).not.toBe('');

    setTheme(DEFAULT_THEME_ID);
    await settle();
    expect(styleEl()?.textContent).toBe('');
  });

  it('reads the palette from the key the settings modal writes', () => {
    // ⚠ `filex.palette`, NOT `filex.theme` — the two were one key once, with
    // two writers and silent fallbacks, so picking a palette reset the
    // light/dark mode and vice versa. A test rather than a comment, because
    // nothing throws when it is wrong.
    expect(THEME_LS_KEY).toBe('filex.palette');
    setTheme('night');
    expect(localStorage.getItem(THEME_LS_KEY)).toBe('night');
    expect(useThemeState().themeId.value).toBe('night');
  });
});

describe('the wiring is in main.ts', () => {
  it('the app applies the palette before it mounts', () => {
    // A component could do this and it did — that was the bug. The check is
    // on the entry point, because "before the mount" is the half that keeps
    // the first frame from painting stock blue and then repainting.
    const main = readFileSync(path.resolve(__dirname, '../../src/main.ts'), 'utf8');
    expect(main, 'main.ts no longer applies the palette').toContain('applyPalette()');
    const applyAt = main.indexOf('applyPalette()');
    const mountAt = main.indexOf('app.mount(');
    expect(applyAt).toBeGreaterThan(-1);
    expect(mountAt).toBeGreaterThan(-1);
    expect(applyAt, 'the palette is applied after the mount — the panel will flash').toBeLessThan(
      mountAt,
    );
  });
});
