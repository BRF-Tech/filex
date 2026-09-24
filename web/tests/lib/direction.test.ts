/**
 * RIGHT TO LEFT — the one module every direction question goes through
 * (packages/core/src/lib/direction.ts).
 *
 * ⚠⚠ Whether a language is right to left is the SERVER's answer
 * (`wire.IsRTL`, carried as `rtl` on each row of the offered-language list).
 * The list is mocked here so this file does not depend on how a language pack
 * reaches the browser — only on the one field direction.ts reads.
 *
 * The geometry helpers are checked two ways: in LTR against the arithmetic
 * the menus used before RTL existed (so the English and Turkish screens
 * cannot move), and in RTL against the LTR answer seen in a mirror.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@brftech/filex-core/src/lib/uiLocales', async (orig) => {
  const real = (await orig()) as Record<string, unknown>;
  return {
    ...real,
    availableLocales: () => [
      { code: 'en', label: 'English', source: 'builtin' },
      { code: 'tr', label: 'Türkçe', source: 'builtin' },
      { code: 'ar', label: 'العربية', source: 'plugin', plugin: 'lang-ar', rtl: true },
      { code: 'fa', label: 'فارسی', source: 'plugin', plugin: 'lang-fa', rtl: true },
      { code: 'es', label: 'Español', source: 'plugin', plugin: 'lang-es', rtl: false },
    ],
  };
});

import {
  clampAlongInline,
  dirOfElement,
  inlineEndX,
  inlineKeyStep,
  inlineStartX,
  isolateLtrRuns,
  isolateValue,
  localeDir,
  openAlongInline,
  syncDocumentDir,
} from '@brftech/filex-core/src/lib/direction';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';

// The observer's callback is queued by the DOM; one macrotask later it has run.
const tick = () => new Promise<void>((r) => setTimeout(r, 0));
const LRI = String.fromCharCode(0x2066);
const FSI = String.fromCharCode(0x2068);
const PDI = String.fromCharCode(0x2069);

describe('localeDir — the server’s flag, never a list of our own', () => {
  it('a language the server marked right to left is rtl', () => {
    expect(localeDir('ar')).toBe('rtl');
    expect(localeDir('fa')).toBe('rtl');
    expect(localeDir('AR')).toBe('rtl');
  });

  it('the two built-ins, a left-to-right pack and anything unknown are ltr', () => {
    expect(localeDir('en')).toBe('ltr');
    expect(localeDir('tr')).toBe('ltr');
    expect(localeDir('es')).toBe('ltr');
    // ⚠ Hebrew IS right to left — but no pack offers it here, and a language
    // whose words are not on screen does not get to turn the screen around.
    expect(localeDir('he')).toBe('ltr');
    expect(localeDir('')).toBe('ltr');
    expect(localeDir(undefined)).toBe('ltr');
  });
});

describe('syncDocumentDir — `dir` is derived from `lang`', () => {
  let stop: (() => void) | undefined;
  afterEach(() => {
    stop?.();
    document.documentElement.removeAttribute('dir');
    document.documentElement.lang = 'en';
  });

  it('follows every change of <html lang>, before the next paint', async () => {
    const html = document.documentElement;
    html.lang = 'en';
    stop = syncDocumentDir(html);
    expect(html.getAttribute('dir')).toBe('ltr');
    html.lang = 'ar';
    await tick();
    expect(html.getAttribute('dir')).toBe('rtl');
    html.lang = 'tr';
    await tick();
    expect(html.getAttribute('dir')).toBe('ltr');
  });
});

describe('dirOfElement — the direction a box is DRAWN in', () => {
  it('is the nearest explicit ltr/rtl, skipping dir="auto"', () => {
    document.body.innerHTML =
      '<div dir="rtl"><div id="a"><bdi dir="auto"><span id="b"></span></bdi></div>' +
      '<div dir="ltr"><span id="c"></span></div></div><span id="d"></span>';
    expect(dirOfElement(document.getElementById('a'))).toBe('rtl');
    expect(dirOfElement(document.getElementById('b'))).toBe('rtl');
    expect(dirOfElement(document.getElementById('c'))).toBe('ltr');
    expect(dirOfElement(document.getElementById('d'))).toBe('ltr');
    expect(dirOfElement(null)).toBe('ltr');
    document.body.innerHTML = '';
  });
});

describe('arrow keys move things the way the arrow points', () => {
  it('→ is forward in LTR, ← is forward in RTL; other keys are 0', () => {
    expect(inlineKeyStep('ArrowRight', 'ltr')).toBe(1);
    expect(inlineKeyStep('ArrowLeft', 'ltr')).toBe(-1);
    expect(inlineKeyStep('ArrowRight', 'rtl')).toBe(-1);
    expect(inlineKeyStep('ArrowLeft', 'rtl')).toBe(1);
    expect(inlineKeyStep('ArrowUp', 'rtl')).toBe(0);
    expect(inlineKeyStep('Enter', 'ltr')).toBe(0);
  });

  it('a box’s start and end edges swap sides', () => {
    const r = { left: 10, right: 50 };
    expect(inlineStartX(r, 'ltr')).toBe(10);
    expect(inlineEndX(r, 'ltr')).toBe(50);
    expect(inlineStartX(r, 'rtl')).toBe(50);
    expect(inlineEndX(r, 'rtl')).toBe(10);
  });
});

/** The context menu's placement exactly as it was written before RTL. */
function oldOpen(anchor: number, w: number, vw: number, m = 8): number {
  let x = anchor;
  if (anchor + w > vw - m) {
    const flipped = anchor - w;
    x = flipped >= m ? flipped : Math.max(m, vw - w - m);
  }
  return x;
}
/** The column menu's / filter popover's clamp exactly as it was. */
function oldClamp(anchor: number, w: number, vw: number, m = 8): number {
  return anchor + w > vw - m ? Math.max(m, vw - m - w) : anchor;
}

describe('menus beside an anchor — LTR unchanged, RTL its mirror', () => {
  const cases: [number, number, number][] = [];
  for (const vw of [390, 1024, 1440]) {
    for (const w of [120, 232, 360, 500]) {
      for (const a of [0, 3, 8, 50, 180, vw / 2, vw - 200, vw - 20, vw]) cases.push([a, w, vw]);
    }
  }

  it('LTR: openAlongInline and clampAlongInline are the old arithmetic, number for number', () => {
    for (const [a, w, vw] of cases) {
      expect(openAlongInline(a, w, vw, 'ltr'), `open ${a},${w},${vw}`).toBe(oldOpen(a, w, vw));
      expect(clampAlongInline(a, w, vw, 'ltr'), `clamp ${a},${w},${vw}`).toBe(oldClamp(a, w, vw));
    }
  });

  it('RTL: the same answer, seen in a mirror (x → viewport − x)', () => {
    for (const [a, w, vw] of cases) {
      const mirroredLeft = vw - oldOpen(vw - a, w, vw) - w;
      expect(openAlongInline(a, w, vw, 'rtl'), `open ${a},${w},${vw}`).toBe(mirroredLeft);
      const mirroredClamp = vw - oldClamp(vw - a, w, vw) - w;
      expect(clampAlongInline(a, w, vw, 'rtl'), `clamp ${a},${w},${vw}`).toBe(mirroredClamp);
    }
  });

  it('RTL, concretely: a right-click in the middle opens DOWN-LEFT of the pointer', () => {
    // Right edge at the pointer: the menu runs the way the line reads.
    expect(openAlongInline(700, 200, 1440, 'rtl')).toBe(500);
    // Near the LEFT edge there is no room that way, so it flips to the right.
    expect(openAlongInline(100, 200, 1440, 'rtl')).toBe(100);
  });
});

describe('machine text inside right-to-left text', () => {
  it('isolates a number pair and a word:value token, and nothing else', () => {
    expect(isolateLtrRuns('الصفحة 3 / 10')).toBe(`الصفحة ${LRI}3 / 10${PDI}`);
    expect(isolateLtrRuns('ابحث بالاسم أو بـ tag:…')).toBe(`ابحث بالاسم أو بـ ${LRI}tag:…${PDI}`);
    expect(isolateLtrRuns('ابحث في الملفات… بالاسم، أو tag:وسم')).toBe(`ابحث في الملفات… بالاسم، أو ${LRI}tag:وسم${PDI}`);
    expect(isolateLtrRuns('1.2 MB / 5 GB')).toBe(`${LRI}1.2 MB / 5 GB${PDI}`);
    expect(isolateLtrRuns('نص عربي فقط')).toBe('نص عربي فقط');
    expect(isolateLtrRuns('10:30')).toBe('10:30');
  });

  it('counts a pair whose numbers were already isolated as values', () => {
    const s = `${isolateValue('3')} / ${isolateValue('10')}`;
    expect(isolateLtrRuns(s)).toBe(`${LRI}${s}${PDI}`);
  });
});

describe('useLocale().t — isolation is RTL only', () => {
  it('in English the string comes back exactly as the catalogue has it', () => {
    const { t, dir } = useLocale(() => 'en');
    expect(dir.value).toBe('ltr');
    expect(t('tour.progress', { n: 3, m: 10 })).toBe('3 / 10');
    expect(t('upload.failed', { name: '2026 report.pdf' })).not.toContain(FSI);
  });

  it('in Arabic a name is first-strong isolated and a pair reads left to right', () => {
    const { t, dir } = useLocale(() => 'ar');
    expect(dir.value).toBe('rtl');
    // No Arabic table is registered here: the English words fall through,
    // which is exactly what makes the marks easy to see.
    expect(t('tour.progress', { n: 3, m: 10 })).toBe(`${LRI}3 / 10${PDI}`);
    expect(t('upload.failed', { name: '2026 report.pdf' })).toContain(`${FSI}2026 report.pdf${PDI}`);
  });
});
