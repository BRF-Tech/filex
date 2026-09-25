// #57 — an OPEN app screen follows a change of light/dark.
//
// Measured 2026-09-25 (operator theme, Arabic, Playwright): the explorer
// turned with the operating system and with the settings switch, and an app's
// page open beside it did not. AppPage.vue and useAppHomeRoute handed the
// page `computed(() => effectiveTheme())` — a computed over localStorage and
// matchMedia, neither of which Vue can track, so it was evaluated ONCE and the
// page kept the mode it was opened in (`fe--theme-dark` pinned on a light
// window). `liveTheme` is the answer: a ref that `paint()` — the one writer of
// `<html class="dark">` — keeps true.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { applyStoredTheme, liveTheme, setStoredTheme } from '@/lib/theme';

const WEB_SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../src');

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : /\.(vue|ts)$/.test(name) ? [p] : [];
  });
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}), text: async () => '' })));
});
afterEach(() => {
  vi.unstubAllGlobals();
  document.documentElement.classList.remove('dark');
});

describe('liveTheme — the mode the page is painted in, as it changes', () => {
  it('follows every paint: the settings switch, and back', () => {
    setStoredTheme('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(liveTheme.value).toBe('dark');
    setStoredTheme('light');
    expect(liveTheme.value).toBe('light');
  });

  it('follows the operating system in auto (what the OS listener repaints)', () => {
    setStoredTheme('auto');
    const mm = vi.spyOn(window, 'matchMedia').mockImplementation(
      (q: string) => ({ matches: q.includes('dark'), media: q, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList,
    );
    applyStoredTheme();
    expect(liveTheme.value).toBe('dark');
    mm.mockImplementation(
      (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList,
    );
    applyStoredTheme();
    expect(liveTheme.value).toBe('light');
  });

  it('no view freezes the mode in a computed over effectiveTheme()', () => {
    const code = (f: string) =>
      readFileSync(f, 'utf8')
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .replace(/(^|\s)\/\/.*$/gm, '$1');
    const frozen = walk(WEB_SRC)
      .filter((f) => /computed\(\s*\(\)\s*=>\s*effectiveTheme\(\)\s*\)/.test(code(f)))
      .map((f) => path.relative(WEB_SRC, f).split(path.sep).join('/'));
    expect(frozen, 'effectiveTheme() is not reactive; hand a page `liveTheme` instead').toEqual([]);
  });
});
