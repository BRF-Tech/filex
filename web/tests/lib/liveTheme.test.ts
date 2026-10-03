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

import { answerAccountPrefs } from '../helpers/accountPrefs';

// setStoredTheme also saves the choice on the account, 400 ms later. The
// stubbed fetch below is gone by then, so the PUT used to land in whichever
// test was running (the source scans here are slow on a loaded machine) and
// helpers/noNetwork failed THAT test (v0.50.0 pretag, 2026-10-02). Answer it,
// and drop a write still waiting when a test ends.
answerAccountPrefs();

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

  // #74 - another tab's choice used to reach only the three views that kept
  // a `storage` listener of their own (Explore, Connections had none, Editor),
  // and those set their local copy alone: the explorer turned dark inside a
  // shell that stayed light. The listener lives beside paint() now.
  it('follows another tab’s choice - the whole window repaints', () => {
    setStoredTheme('light');
    expect(liveTheme.value).toBe('light');
    localStorage.setItem('filex.theme', 'dark');
    window.dispatchEvent(new StorageEvent('storage', { key: 'filex.theme', newValue: 'dark' }));
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(liveTheme.value).toBe('dark');
    // A different key changes nothing.
    localStorage.setItem('filex.theme', 'light');
    window.dispatchEvent(new StorageEvent('storage', { key: 'filex.palette', newValue: 'night' }));
    expect(liveTheme.value).toBe('dark');
    window.dispatchEvent(new StorageEvent('storage', { key: 'filex.theme', newValue: 'light' }));
    expect(liveTheme.value).toBe('light');
  });

  it('no view keeps a light/dark copy of its own (a MutationObserver on <html>, a storage listener)', () => {
    const code = (f: string) =>
      readFileSync(f, 'utf8')
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .replace(/(^|\s)\/\/.*$/gm, '$1');
    const copies = walk(WEB_SRC)
      .filter((f) => !f.endsWith(path.join('lib', 'theme.ts')))
      .filter((f) => {
        const src = code(f);
        return (
          /classList\.contains\(\s*['"]dark['"]\s*\)/.test(src) && /MutationObserver/.test(src)
        ) || /e\.key\s*===\s*['"]filex\.theme['"]/.test(src);
      })
      .map((f) => path.relative(WEB_SRC, f).split(path.sep).join('/'));
    expect(copies, 'hand the screen `liveTheme` (lib/theme) instead of watching <html> yourself').toEqual([]);
  });

  it('no core surface listens to the OS mode on its own - composables/useSystemDark is the one listener', () => {
    const CORE_SRC = path.resolve(WEB_SRC, '../../packages/core/src');
    const own = walk(CORE_SRC)
      .filter((f) => !f.endsWith(path.join('composables', 'useSystemDark.ts')))
      .filter((f) => {
        const src = readFileSync(f, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/.*$/gm, '$1');
        return /matchMedia\(\s*['"]\(prefers-color-scheme:\s*dark\)['"]\s*\)\s*;?\s*$/m.test(src)
          && /addEventListener\??\.?\(\s*['"]change['"]/.test(src);
      })
      .map((f) => path.relative(CORE_SRC, f).split(path.sep).join('/'));
    expect(own, 'use composables/useSystemDark').toEqual([]);
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
