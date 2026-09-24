/**
 * Right to left: no PHYSICAL direction creeps back into the interface.
 *
 * filex lays itself out right to left for Arabic, Hebrew, Persian, Urdu and
 * every other right-to-left language a language pack adds (v0.43.0). That
 * works because the interface is written in LOGICAL terms — `margin-inline-
 * start`, `inset-inline-end`, `text-align: start`, Tailwind's `ms-`/`pe-`/
 * `start-`/`text-end`/`rounded-s`/`border-e` — which turn by themselves under
 * `dir="rtl"` and are the very same pixels under `dir="ltr"`. One stray
 * `margin-left` is a gap on the wrong side of an Arabic screen that nobody
 * reading the English one will ever see, so this check reads the sources
 * instead.
 *
 * HOW TO ANSWER IT WHEN IT FIRES
 * ------------------------------
 * Write the logical form:
 *
 *   margin-left / padding-right / border-left   → margin-inline-start /
 *                                                  padding-inline-end /
 *                                                  border-inline-start
 *   left: / right:                              → inset-inline-start / -end
 *   text-align: left | right                    → start | end
 *   float / clear: left | right                 → inline-start | inline-end
 *   border-top-left-radius                      → border-start-start-radius
 *   margin: 0 8px 0 4px (sides differ)          → margin-block + margin-inline
 *   ml- mr- pl- pr- left- right- text-left      → ms- me- ps- pe- start- end-
 *   rounded-l border-r …                           text-start rounded-s border-e
 *   translateX(8px) / box-shadow: 2px 0 …       → × var(--filex-dir-x, 1)
 *                                                  (core base.css, top)
 *
 * and for GESTURES (pointer x, arrow keys, menus opened beside an anchor) use
 * the helpers in packages/core/src/lib/direction.ts, never your own
 * `dir === 'rtl' ? … : …`.
 *
 * WHEN PHYSICAL IS GENUINELY RIGHT
 * --------------------------------
 * ⚠⚠ DOCUMENT SPACE never mirrors: a box at the left of a PDF page is at the
 * left of the page in every language. So do things centred by
 * `left: 50%; transform: translateX(-50%)` (symmetric), a drawn tick, a play
 * triangle, and a floating box whose `left` is a viewport coordinate the
 * script computed (its maths is direction-aware — lib/direction).
 *
 *   - In CSS, say so ON THE LINE: `left: 50%; /* rtl-physical: why … *\/`.
 *     The marker must carry a reason.
 *   - In a template binding or a script, add an entry to ALLOW below, with the
 *     reason. An entry that no longer matches anything fails the test, so the
 *     list cannot rot into a blanket exemption.
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');

/** Where the interface lives. Tests, builds and catalogues are not interface. */
const ROOTS = ['packages/core/src', 'web/src', 'backend/internal'];
const SKIP_DIR = /(^|\/)(node_modules|dist|locales|testdata|__tests__)(\/|$)/;

interface Allow {
  file: string;
  /** A substring of the offending line. */
  line: string;
  why: string;
}

/**
 * The places physical is right, each with the reason. Keyed by file and a
 * substring of the line, so an edit that moves a line keeps its entry and an
 * edit that REMOVES it makes the entry stale (and red).
 */
export const ALLOW: Allow[] = [
  {
    file: 'packages/core/src/components/plugin/nodes/SurfacePdfFields.vue',
    line: ":style=\"{ left: `${f.x * 100}%`",
    why: '⚠⚠ DOCUMENT SPACE: a field sits at its PDF coordinates (fractions of the page, origin top-left); the signer stamps it there. It never mirrors.',
  },
  {
    file: 'packages/core/src/components/plugin/nodes/SurfacePdfFields.vue',
    line: ":style=\"{ left: `${ghost.x * 100}%`",
    why: '⚠⚠ DOCUMENT SPACE: the box being drawn, at the pointer\'s position ON THE PAGE.',
  },
  {
    file: 'packages/core/src/components/ContextMenu.vue',
    line: ":style=\"{ top: y + 'px', left: x + 'px' }\"",
    why: 'A viewport coordinate computed by openAlongInline (lib/direction), which already opens the menu toward the inline end.',
  },
  {
    file: 'packages/core/src/components/DataTable.vue',
    line: ":style=\"{ top: colMenuY + 'px', left: colMenuX + 'px' }\"",
    why: 'A viewport coordinate computed by clampAlongInline (lib/direction).',
  },
  {
    file: 'packages/core/src/components/FilterBar.vue',
    line: ":style=\"{ left: pos.x + 'px', top: pos.y + 'px' }\"",
    why: 'A viewport coordinate computed by clampAlongInline from the chip\'s START edge (lib/direction).',
  },
  {
    file: 'packages/core/src/components/OnboardingTour.vue',
    line: "left: spot.left + 'px'",
    why: 'The spotlight is the target\'s own viewport box (getBoundingClientRect) — physical by definition.',
  },
  {
    file: 'packages/core/src/components/OnboardingTour.vue',
    line: 'left: `${Math.round(left)}px`',
    why: 'The card\'s viewport coordinate; which SIDE of the target is tried first follows the direction (`beside`).',
  },
  {
    file: 'packages/core/src/lib/dragGhost.ts',
    line: "el.style.left = '-1000px'",
    why: 'A FIXED box adds no scrollable overflow, so off the left edge is off screen in either direction; it only exists for setDragImage.',
  },
  {
    file: 'web/src/components/AccountMenu.vue',
    line: ':style="{ top: pos.top, right: pos.right, left: pos.left }"',
    why: 'anchorUnderEndEdge (web/src/lib/anchoredPanel.ts) answers `right` in LTR and `left` in RTL — both are bound, the absent one is unset.',
  },
  {
    file: 'web/src/components/NotificationBell.vue',
    line: ':style="{ top: pos.top, right: pos.right, left: pos.left, width: pos.width }"',
    why: 'anchorUnderEndEdge answers `right` in LTR and `left` in RTL.',
  },
];

/* ── walking the tree ────────────────────────────────────────────────── */

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    const rel = path.relative(REPO, p).split(path.sep).join('/');
    if (SKIP_DIR.test(rel)) continue;
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(css|vue|ts|go)$/.test(name) && !/(_test\.go|\.d\.ts|\.test\.ts|\.spec\.ts)$/.test(name)) out.push(rel);
  }
  return out;
}

const FILES = ROOTS.flatMap((r) => walk(path.join(REPO, r)));

interface Finding {
  file: string;
  line: number;
  text: string;
  what: string;
}

/* ── CSS ─────────────────────────────────────────────────────────────── */

/** Split a CSS value on top-level whitespace, keeping `calc( … )` whole. */
function splitTop(v: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let cur = '';
  for (const ch of v.trim()) {
    if (ch === '(') depth++;
    if (ch === ')') depth--;
    if (/\s/.test(ch) && depth === 0) {
      if (cur) out.push(cur);
      cur = '';
    } else cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}

/** Split a CSS value on top-level commas (a shadow list), keeping `color-mix(a, b)` whole. */
function splitTopCommas(v: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let cur = '';
  for (const ch of v) {
    if (ch === '(') depth++;
    if (ch === ')') depth--;
    if (ch === ',' && depth === 0) {
      out.push(cur);
      cur = '';
    } else cur += ch;
  }
  out.push(cur);
  return out;
}

const PHYS_PROP =
  /(?:^|[{;\s])((?:margin|padding|scroll-margin|scroll-padding)-(?:left|right)|border-(?:left|right)(?:-(?:width|style|color))?|border-(?:top|bottom)-(?:left|right)-radius|left|right)\s*:/;
const PHYS_VALUE = /(?:^|[{;\s])(text-align|float|clear)\s*:\s*(left|right)\b/;
const SHORTHAND =
  /(?:^|[{;\s])(margin|padding|inset|border-width|border-style|border-color|scroll-margin|scroll-padding|border-radius)\s*:\s*([^;{}]+)/;
const TRANSLATE = /translate(?:X|3d)?\(\s*([^,)]+)/g;
const SHADOW = /(?:^|[{;\s])box-shadow\s*:\s*([^;{}]+)/;

/** What is physical in one line of CSS (comments already blanked). */
function cssFindings(code: string): string[] {
  const what: string[] = [];
  const p = PHYS_PROP.exec(code);
  if (p) what.push(`\`${p[1]}\``);
  const v = PHYS_VALUE.exec(code);
  if (v) what.push(`\`${v[1]}: ${v[2]}\``);
  const s = SHORTHAND.exec(code);
  if (s) {
    const vals = splitTop(s[2].replace(/!important/, ''));
    if (s[1] === 'border-radius') {
      if (!s[2].includes('/')) {
        const [tl, tr, br, bl] =
          vals.length === 2 ? [vals[0], vals[1], vals[0], vals[1]]
          : vals.length === 3 ? [vals[0], vals[1], vals[2], vals[1]]
          : vals.length === 4 ? vals
          : [vals[0], vals[0], vals[0], vals[0]];
        if (tl !== tr || bl !== br) what.push('`border-radius` with sides that differ');
      }
    } else if (vals.length === 4 && vals[1] !== vals[3]) {
      what.push(`\`${s[1]}\` with a left and a right that differ`);
    }
  }
  for (const m of code.matchAll(TRANSLATE)) {
    const x = m[1].trim();
    if (!/^(0|0px|-50%)$/.test(x) && !x.includes('--filex-dir-x')) what.push(`a horizontal \`translate(${x})\``);
  }
  const sh = SHADOW.exec(code);
  if (sh && !sh[1].includes('--filex-dir-x')) {
    for (const one of splitTopCommas(sh[1])) {
      const first = splitTop(one).find((t) => /^-?[\d.]/.test(t));
      if (first && !/^-?0(px|rem|em)?$/.test(first) && !/inset/.test(one.split(first)[0])) {
        what.push(`a \`box-shadow\` with a horizontal offset (${first})`);
        break;
      }
    }
  }
  return what;
}

/** Blank CSS comments (keeping line breaks), returning the code of each line. */
function cssLines(css: string): string[] {
  let inComment = false;
  return css.split('\n').map((line) => {
    let out = '';
    for (let i = 0; i < line.length; i++) {
      if (!inComment && line.startsWith('/*', i)) {
        inComment = true;
        i++;
        out += '  ';
      } else if (inComment && line.startsWith('*/', i)) {
        inComment = false;
        i++;
        out += '  ';
      } else out += inComment ? ' ' : line[i];
    }
    return out;
  });
}

function scanCss(file: string, css: string, firstLine: number, into: Finding[]) {
  const raw = css.split('\n');
  cssLines(css).forEach((code, i) => {
    const what = cssFindings(code);
    if (!what.length) return;
    const marker = /rtl-physical:\s*(\S.*)?$/.exec(raw[i]);
    if (marker && marker[1] && marker[1].replace(/\*\/.*$/, '').trim().length >= 8) return;
    into.push({ file, line: firstLine + i, text: raw[i].trim(), what: what.join(', ') });
  });
}

/* ── templates and scripts ───────────────────────────────────────────── */

const TW_PHYSICAL =
  /(?:^|[\s"'`{(,])(?:[a-z0-9_[\]&.-]+:)*!?-?((?:ml|mr|pl|pr|scroll-ml|scroll-mr|scroll-pl|scroll-pr)-[\w./[\]%-]+|(?:left|right)-(?:\d[\w./]*|px|auto|full|\[[^\]]+\])|text-(?:left|right)|rounded-(?:l|r|tl|tr|bl|br)(?:-[\w[\]./-]+)?|border-(?:l|r)(?:-[\w[\]./-]+)?|float-(?:left|right)|clear-(?:left|right)|space-x-[\w./[\]-]+|divide-x(?:-[\w./[\]-]+)?)(?=$|[\s"'`}),])/;
/** `-translate-x-full`, `translate-x-4` without an `rtl:` partner on the line. */
const TW_TRANSLATE = /(?:^|[\s"'`])-?translate-x-(?!0\b)[\w./[\]-]+/;
const TW_ORIGIN = /(?:^|[\s"'`])origin-(?:top-|bottom-)?(?:left|right)\b/;
/** A style binding or assignment that sets a physical side. */
const STYLE_PHYSICAL = [
  /\.style\.(left|right|marginLeft|marginRight|paddingLeft|paddingRight|borderLeft|borderRight)\s*=/,
  /cssText\s*=.*\b(left|right)\s*:/,
  /:style="[^"]*\b(left|right|marginLeft|marginRight|paddingLeft|paddingRight)\s*:/,
  /\sstyle="(?:[^"]*[;\s])?((?:margin|padding)-(?:left|right)|border-(?:left|right)|left|right)\s*:/,
  /\sstyle="[^"]*\b(text-align:\s*(?:left|right))\b/,
  /\b(left|right|marginLeft|marginRight|paddingLeft|paddingRight)\s*:\s*(`[^`]*(px|%)`|[^,}]*\+\s*'(px|%)')/,
];

function isCommentLine(l: string): boolean {
  return /^\s*(\/\/|\*|\/\*|<!--)/.test(l);
}

function scanMarkup(file: string, text: string, into: Finding[], skipRanges: [number, number][]) {
  text.split('\n').forEach((raw, i) => {
    const n = i + 1;
    if (skipRanges.some(([a, b]) => n >= a && n <= b)) return;
    if (isCommentLine(raw)) return;
    const what: string[] = [];
    const tw = TW_PHYSICAL.exec(raw);
    if (tw) what.push(`Tailwind \`${tw[1]}\``);
    if (TW_TRANSLATE.test(raw) && !/\brtl:-?translate-x-/.test(raw)) {
      what.push('Tailwind `translate-x-*` with no `rtl:` partner');
    }
    if (TW_ORIGIN.test(raw) && !/\brtl:origin-/.test(raw)) what.push('Tailwind `origin-*-left|right` with no `rtl:` partner');
    for (const re of STYLE_PHYSICAL) {
      const m = re.exec(raw);
      if (m) {
        what.push(`a physical style \`${m[1]}\``);
        break;
      }
    }
    if (!what.length) return;
    if (ALLOW.some((a) => a.file === file && raw.includes(a.line))) return;
    into.push({ file, line: n, text: raw.trim(), what: what.join(', ') });
  });
}

/** Go: only the CSS the server-rendered pages carry (inside raw strings). */
function scanGo(file: string, text: string, into: Finding[]) {
  text.split('\n').forEach((raw, i) => {
    if (/^\s*\/\//.test(raw)) return;
    if (!/[{;]\s*[a-z-]+\s*:[^=]/.test(raw)) return; // a CSS declaration, not Go
    const what = cssFindings(raw);
    if (!what.length) return;
    if (/rtl-physical:\s*\S{8,}/.test(raw)) return;
    into.push({ file, line: i + 1, text: raw.trim(), what: what.join(', ') });
  });
}

function findings(): Finding[] {
  const out: Finding[] = [];
  for (const file of FILES) {
    const text = readFileSync(path.join(REPO, file), 'utf8').replace(/\r\n/g, '\n');
    if (file.endsWith('.css')) scanCss(file, text, 1, out);
    else if (file.endsWith('.go')) scanGo(file, text, out);
    else if (file.endsWith('.vue')) {
      const styles: [number, number][] = [];
      for (const m of text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) {
        const startLine = text.slice(0, m.index! + m[0].indexOf('>') + 1).split('\n').length;
        const endLine = startLine + m[1].split('\n').length - 1;
        styles.push([startLine, endLine]);
        scanCss(file, m[1], startLine, out);
      }
      scanMarkup(file, text, out, styles);
    } else scanMarkup(file, text, out, []);
  }
  return out;
}

describe('right to left — the interface is written in logical directions', () => {
  it('reads the tree it is meant to guard', () => {
    // A path typo would scan nothing and pass forever.
    expect(FILES).toContain('packages/core/src/styles/base.css');
    expect(FILES).toContain('web/src/styles/main.css');
    expect(FILES.filter((f) => f.endsWith('.vue')).length).toBeGreaterThan(100);
  });

  it('has no physical left/right outside the allow-listed places', () => {
    const found = findings();
    const report = found.map((f) => `  ${f.file}:${f.line}  ${f.what}\n      ${f.text}`).join('\n');
    expect(
      found,
      `A physical direction is back. Write the logical form (see the head of this file for the table), or — if this is document space or a computed viewport coordinate — mark the CSS line \`/* rtl-physical: <why> */\` or add an ALLOW entry with the reason:\n${report}`,
    ).toEqual([]);
  });

  it('every ALLOW entry still names a line that exists (no stale exemptions)', () => {
    const stale = ALLOW.filter((a) => {
      try {
        return !readFileSync(path.join(REPO, a.file), 'utf8').includes(a.line);
      } catch {
        return true;
      }
    });
    expect(stale, 'These ALLOW entries match nothing any more — delete them').toEqual([]);
  });

  it('a CSS exemption says why', () => {
    const bare: string[] = [];
    for (const file of FILES.filter((f) => /\.(css|vue|go)$/.test(f))) {
      readFileSync(path.join(REPO, file), 'utf8')
        .split('\n')
        .forEach((l, i) => {
          const m = /rtl-physical:\s*(.*)$/.exec(l);
          if (m && m[1].replace(/\*\/.*$/, '').trim().length < 8) bare.push(`${file}:${i + 1}`);
        });
    }
    expect(bare, '`rtl-physical:` needs a reason after it').toEqual([]);
  });

  /* The detector itself, on lines that must and must not trip it — so a
     regex edit cannot quietly turn the guard off. */
  it('recognises the physical forms, and leaves the logical ones alone', () => {
    const trips = [
      '  margin-left: 4px;',
      '  padding-right: 0;',
      '  border-left: 1px solid;',
      '  border-right-color: red;',
      '  left: 0;',
      '  right: calc(1px + 2px);',
      '  text-align: left;',
      '  float: right;',
      '  border-top-left-radius: 4px;',
      '  margin: 0 8px 0 4px;',
      '  padding: 1px 2px 3px 4px;',
      '  border-radius: 4px 4px 0 2px;',
      '  transform: translateX(8px);',
      '  box-shadow: -1px 0 0 0 red;',
    ];
    for (const l of trips) expect(cssFindings(l), l).not.toEqual([]);
    const passes = [
      '  margin-inline-start: 4px;',
      '  inset-inline-end: 0;',
      '  text-align: start;',
      '  margin: 0 8px;',
      '  padding: 1px 2px 3px 2px;',
      '  border-radius: 4px 4px 0 0;',
      '  transform: translateX(-50%);',
      '  transform: translateX(calc(8px * var(--filex-dir-x, 1)));',
      '  box-shadow: 0 1px 2px red;',
      '  box-shadow: calc(-1px * var(--filex-dir-x, 1)) 0 0 0 red;',
      '  top: 0;',
    ];
    for (const l of passes) expect(cssFindings(l), l).toEqual([]);

    const tw = (s: string) => TW_PHYSICAL.test(s);
    for (const l of ['class="ml-2"', 'class="flex pr-4"', "'text-right'", 'class="sm:left-0"', 'class="border-l-4"', 'class="rounded-tr-lg"', 'class="-mr-1"']) {
      expect(tw(l), l).toBe(true);
    }
    for (const l of ['class="ms-2"', 'class="flex pe-4"', "'text-end'", 'class="sm:start-0"', 'class="border-s-4"', 'class="rounded-se-lg"', 'class="mt-2"', 'class="px-3"']) {
      expect(tw(l), l).toBe(false);
    }
  });
});

/* ── what must NOT turn, and the rules that make things turn ──────────── */

function cssRule(file: string, selector: string): string {
  const css = readFileSync(path.join(REPO, file), 'utf8').replace(/\r\n/g, '\n').replace(/\/\*[\s\S]*?\*\//g, '');
  const out: string[] = [];
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    // Top-level commas only: `:is(pre, code)` is ONE selector.
    const sels = splitTopCommas(m[1]).map((s) => s.replace(/\s+/g, ' ').trim());
    if (sels.includes(selector)) out.push(m[2]);
  }
  return out.join(';');
}

describe('document space never mirrors', () => {
  it('a PDF field sits at its page coordinates: physical `left` in %, not a logical inset', () => {
    // ⚠⚠ If this binding ever becomes `insetInlineStart`, a box placed at the
    // left of a page is drawn at its RIGHT in Arabic — and the signer still
    // stamps it on the left. (The ALLOW entries above go stale too.)
    const src = readFileSync(path.join(REPO, 'packages/core/src/components/plugin/nodes/SurfacePdfFields.vue'), 'utf8');
    expect(src).toContain(":style=\"{ left: `${f.x * 100}%`, top: `${f.y * 100}%`");
    expect(src).not.toMatch(/insetInlineStart|inset-inline/);
  });

  it('the box’s resize handle and required mark stay on the PAGE’s right, not the line’s end', () => {
    expect(cssRule('packages/core/src/styles/base.css', '.fe-spdf__handle')).toMatch(/(^|[;\s])right:\s*-5px/);
    expect(cssRule('packages/core/src/styles/base.css', '.fe-spdf__handle')).not.toMatch(/inset-inline/);
    expect(cssRule('packages/core/src/styles/base.css', '.fe-spdf__field.is-required::after')).toMatch(/(^|[;\s])right:\s*3px/);
  });
});

describe('the rules that turn things', () => {
  const BASE = 'packages/core/src/styles/base.css';

  it('every `dir` sets the sign transforms read, and machine text stays left to right', () => {
    expect(cssRule(BASE, "[dir='rtl']")).toMatch(/--filex-dir-x:\s*-1/);
    expect(cssRule(BASE, "[dir='ltr']")).toMatch(/--filex-dir-x:\s*1/);
    expect(cssRule(BASE, "[dir='rtl'] :is(pre, code, kbd, samp, .font-mono, .tbl-mono, .fe-share__url, .fe-inspector__share-url)")).toMatch(/direction:\s*ltr/);
  });

  it('directional icons are mirrored by one rule per icon set', () => {
    expect(cssRule(BASE, ':is(.fe-aicon--dir, .fe-viewer__chev > svg, .fe-destpick__rowinto):dir(rtl)')).toMatch(/scaleX\(-1\)/);
    const main = readFileSync(path.join(REPO, 'web/src/styles/main.css'), 'utf8');
    expect(main).toMatch(/\.lucide-arrow-left-icon[\s\S]*?\):dir\(rtl\)\s*\{\s*transform:\s*scaleX\(-1\)/);
  });
});
