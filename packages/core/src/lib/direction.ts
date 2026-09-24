/**
 * direction — which way a language is written, and where that answer goes.
 *
 * ⚠⚠ ONE LIST. Whether a language is written right to left is decided on the
 * SERVER, by `wire.IsRTL` (backend/pkg/pluginkit/wire/langpack.go — the
 * primary-language list plus the script subtags), and it reaches the browser
 * as the `rtl` flag on each row of the offered-language list
 * (`/api/public/branding` → `ui_locales`, folded in by `lib/uiLocales`).
 * This file only READS that flag. It must never grow a table of its own:
 * `Intl.Locale#getTextInfo()` looks like a shortcut and is one — to a second
 * list (the browser's CLDR copy) that Firefox does not ship at all and that
 * would disagree with the install review about the same pack.
 *
 * The two catalogues the product ships (English, Turkish) are left to right,
 * and a code nobody offers is left to right: a language whose words are not
 * on screen does not get to turn the screen around.
 *
 * ⚠⚠ THE RULE, for every surface: `dir` follows the language whose words the
 * surface is showing.
 *
 *   - A whole page (the admin panel, the drive, the public share/PIN/drop
 *     pages, the signing wizard) carries it on `<html>`, DERIVED from
 *     `<html lang>` by `syncDocumentDir` — every place that already sets the
 *     page's language (web/src/i18n, PublicLinkPage) sets its direction with
 *     it, without having to know that direction exists.
 *   - An embedded explorer carries it on its own root, from ITS locale — not
 *     from the host page. An Arabic explorer inside an English page reads
 *     right to left; an English explorer inside an Arabic page reads left to
 *     right. Laying English out mirrored because the page around it is Arabic
 *     (or the reverse) scrambles exactly the words the component exists to
 *     show, and the host already chose the component's language by setting
 *     `locale`.
 *   - Anything Teleported out of that root (menus, popovers, the tour, the
 *     quick-look hint) carries `dir` again, because `<body>` is not inside the
 *     explorer and would hand it the HOST's direction. `useLocale().dir` is the
 *     value to bind.
 *
 * ⚠⚠ Direction is about the INTERFACE. Document space — a PDF page, an image,
 * a drawing, a crop box — has its own coordinates and never mirrors (see
 * lib/pdfFieldsGeom). Nothing here may be applied to those.
 */
import { computed, watch } from 'vue';
import { availableLocales, localesVersion, normalizeLocaleCode } from './uiLocales';

export type TextDirection = 'ltr' | 'rtl';

/**
 * The offered codes the server flagged right to left.
 *
 * ⚠ Read through a widened row type: the flag is added to
 * `PublicLocaleOption` by the language-pack platform, and a row that does not
 * carry it (an older server, the two built-ins) is simply left to right.
 * `localesVersion` is read so the set is re-derived the moment the list — and
 * with it a pack's flag — arrives or changes.
 */
const rtlCodes = computed(() => {
  void localesVersion.value;
  const out = new Set<string>();
  for (const row of availableLocales() as Array<{ code: string; rtl?: boolean }>) {
    if (row.rtl === true) out.add(row.code);
  }
  return out;
});

/** `'rtl'` when the server says this language is written right to left. */
export function localeDir(code: string | null | undefined): TextDirection {
  const c = normalizeLocaleCode(code ?? '');
  return c && rtlCodes.value.has(c) ? 'rtl' : 'ltr';
}

/**
 * Keep `root`'s `dir` derived from its `lang`, for as long as the page lives.
 *
 * ⚠ An observer on `lang`, not a call from each place that sets the language:
 * there are several (the admin panel's language decision, the public link
 * page, and whatever sets it next), and a direction that each of them had to
 * remember to set is a direction one of them forgets. The observer's callback
 * runs as a microtask — before the next paint — so the words and the layout
 * turn together.
 *
 * ⚠ Also re-applied when the language LIST changes: a pack's language can be
 * on `<html lang>` before the list saying it is right to left has arrived
 * (the admin panel holds a stored choice while the list is in flight).
 *
 * Only for a host that owns the whole page. An embed never calls this — the
 * host page's direction is the host's business (see the rule above).
 *
 * @returns a stop function (tests, hot reload).
 */
export function syncDocumentDir(
  root: HTMLElement | null = typeof document !== 'undefined' ? document.documentElement : null,
): () => void {
  if (!root) return () => {};
  const apply = () => {
    const want = localeDir(root.getAttribute('lang'));
    if (root.getAttribute('dir') !== want) root.setAttribute('dir', want);
  };
  apply();
  const mo = typeof MutationObserver !== 'undefined' ? new MutationObserver(apply) : null;
  mo?.observe(root, { attributes: true, attributeFilter: ['lang'] });
  const stop = watch(localesVersion, apply);
  return () => {
    mo?.disconnect();
    stop();
  };
}

/* ── interaction geometry ─────────────────────────────────────────────── */
/*
 * ⚠⚠ Pointer events and `getBoundingClientRect()` are PHYSICAL: x grows to the
 * right whatever the page's direction. Every gesture that means "toward the
 * end of the line" — widening a column by its trailing edge, moving a column
 * later, opening a menu beside its anchor — has to translate between the two.
 * These helpers are that translation, in one place; a component that does its
 * own `dir === 'rtl' ? … : …` arithmetic is a component that will get one of
 * the four cases wrong.
 *
 * ⚠⚠ Never for DOCUMENT space (a PDF page, an image, a crop): a page's x is
 * the page's own and is not mirrored (lib/pdfFieldsGeom).
 */

/**
 * The direction an element is actually laid out in: its nearest explicit
 * `dir="ltr|rtl"`. `dir="auto"` (user text isolated with `<bdi>`-like
 * behaviour) is skipped — it says nothing about the layout around it.
 */
export function dirOfElement(el: Element | null | undefined): TextDirection {
  const host = el?.closest?.('[dir="rtl"], [dir="ltr"]');
  return host?.getAttribute('dir') === 'rtl' ? 'rtl' : 'ltr';
}

/** +1 for the physical x axis in LTR, -1 in RTL: "toward the inline end". */
export function inlineSign(dir: TextDirection): 1 | -1 {
  return dir === 'rtl' ? -1 : 1;
}

/**
 * A horizontal arrow key as a step along the line: +1 = toward the inline END
 * (→ in LTR, ← in RTL), -1 = toward the start, 0 = not a horizontal arrow.
 * The key a person presses moves things the way the arrow on it points.
 */
export function inlineKeyStep(key: string, dir: TextDirection): -1 | 0 | 1 {
  const physical = key === 'ArrowRight' ? 1 : key === 'ArrowLeft' ? -1 : 0;
  if (physical === 0) return 0;
  return (physical * inlineSign(dir)) as -1 | 1;
}

/** The x of a box's inline START edge — its left in LTR, its right in RTL. */
export function inlineStartX(r: { left: number; right: number }, dir: TextDirection): number {
  return dir === 'rtl' ? r.right : r.left;
}

/** The x of a box's inline END edge — its right in LTR, its left in RTL. */
export function inlineEndX(r: { left: number; right: number }, dir: TextDirection): number {
  return dir === 'rtl' ? r.left : r.right;
}

/**
 * The physical `left` of a floating box opened at `anchor` (a viewport x):
 * it opens toward the inline END — down-right of the pointer in LTR,
 * down-LEFT in RTL — flips to the other side of the anchor when that would
 * overflow the viewport, and clamps when neither side fits.
 *
 * ⚠ Written ONCE, as the LTR rule, and mirrored by mapping x → viewport − x:
 * the RTL behaviour is the LTR behaviour seen in a mirror by construction,
 * and the LTR answer is byte-for-byte what the menus did before RTL existed.
 */
export function openAlongInline(
  anchor: number,
  size: number,
  viewport: number,
  dir: TextDirection,
  margin = 8,
): number {
  const a = dir === 'rtl' ? viewport - anchor : anchor;
  let left = a;
  if (a + size > viewport - margin) {
    const flipped = a - size;
    left = flipped >= margin ? flipped : Math.max(margin, viewport - size - margin);
  }
  return dir === 'rtl' ? viewport - left - size : left;
}

/**
 * Like `openAlongInline`, without the flip: the box starts at `anchor` and is
 * only pushed back inside the viewport (popovers that hang from a chip and
 * must stay beside it). Same mirror construction.
 */
export function clampAlongInline(
  anchor: number,
  size: number,
  viewport: number,
  dir: TextDirection,
  margin = 8,
): number {
  const a = dir === 'rtl' ? viewport - anchor : anchor;
  const left = a + size > viewport - margin ? Math.max(margin, viewport - margin - size) : a;
  return dir === 'rtl' ? viewport - left - size : left;
}

/* ── text that must read left to right inside right-to-left text ───────── */

const LRI = '\u2066';
const RLI = '\u2067';
const FSI = '\u2068';
const PDI = '\u2069';

/**
 * The runs of a right-to-left sentence that are MACHINE text and read left to
 * right whatever surrounds them:
 *
 *   - a number pair — `3 / 10`, `1.2 MB / 5 GB`. Spaced out, the slash is a
 *     neutral between two numbers and the bidi algorithm resolves it to the
 *     paragraph's direction, so an RTL line draws `10 / 3`;
 *   - a `word:value` token — the search syntax `tag:…`, a URL
 *     (`https://…`), a Windows path (`C:\Users\…`), the token syntax a
 *     refusal names (`root:<storage>://<folder>`). Its colon sits between
 *     Latin and Arabic letters and lands on the wrong side: `tag:وسم` draws
 *     as `وسم:tag`, and `root:<storage>://<folder>` loses its closing `>` to
 *     the far left of the run, mirrored into `<`;
 *   - an absolute PATH — `/var/lib/filex/report.pdf`. Its LEADING slash is a
 *     neutral between the Arabic before it and the Latin after it, so it
 *     takes the paragraph's direction and is drawn at the RIGHT-hand END of
 *     the run: the line reads `var/lib/filex/report.pdf/`. Measured in
 *     Chromium at `dir="rtl"` (v0.43.0): two rectangles, the second of them
 *     the six pixels of that slash, against one when the run is isolated.
 *     ⚠ ONE segment is a path too — `/informe.pdf`, a file at the root of
 *     a storage, which a notification body names that way. It was left out
 *     (the rule wanted two), and the admin Notifications page drew it as
 *     `informe.pdf/` in Arabic (v0.43.0 pack agent). A single segment is
 *     isolated only when its slash STARTS something: not after a Latin
 *     letter or digit (`and/or`, `km/h`, `24/09`, `TCP/IP`), not after
 *     another slash, dot or colon, and only when the segment holds a Latin
 *     letter or digit — so a lone `/` between two Arabic words, `و/أو`,
 *     and a spaced `3 / 10` (the rule above answers that one) stay as
 *     they are. A trailing full stop is the sentence's, not the path's.
 *     ⚠ And the segment has to END there — no colon after it. In
 *     `/المستندات/tag:2026/…` the slash before `tag` is preceded by an
 *     Arabic letter and so starts a one-segment path; left unchecked it
 *     took `/tag`, and the `word:value` token after it — the run that
 *     actually needs isolating — was cut in two.
 *
 * Each is wrapped in LEFT-TO-RIGHT ISOLATE … POP DIRECTIONAL ISOLATE
 * (U+2066 … U+2069), which works where markup cannot go — an input's
 * placeholder, a `title`, an `aria-label` — and is invisible everywhere.
 * A number that `isolateValue` already wrapped still counts: the pattern
 * steps over an isolate mark on either side of it.
 *
 * ⚠ Called ONLY for a right-to-left language — never on its own. `foreignText`
 * below is the one entry point that knows which languages those are; in LTR
 * the text comes back untouched, so the English and Turkish strings are
 * byte-for-byte what they were.
 *
 * ⚠⚠ IDEMPOTENT. Text now reaches a screen through more than one of these
 * gates (a catalogue sentence through `useLocale().t`, a server's sentence
 * through `foreignText`, a failure said by lib/errorWords, a notification
 * composed by the reader), and a run that is already isolated must not
 * collect a second pair of marks — nested isolates draw the same but make a
 * string that no longer equals the one a test wrote.
 */
const LTR_RUNS =
  /[\u2066-\u2069]?\d[\d.,]*(?:\s?[A-Za-z%]{1,3})?[\u2066-\u2069]?\s*\/\s*[\u2066-\u2069]?\d[\d.,]*(?:\s?[A-Za-z%]{1,3})?[\u2066-\u2069]?|[A-Za-z][\w+.-]*:[^\s,\u060C)\u2066-\u2069]+|\/(?:[A-Za-z0-9_.~%+-]+\/)+[A-Za-z0-9_.~%+-]*|(?<![A-Za-z0-9_.~%+:\/-])\/(?=[A-Za-z0-9_.~%+-]*[A-Za-z0-9])[A-Za-z0-9_.~%+-]*[A-Za-z0-9_~%+-](?!\.?[A-Za-z0-9_~%+:-])/g;

/**
 * Is `s` already ONE isolate, opened at its first character and closed at its
 * last?
 *
 * ⚠ Not "starts with an initiator and ends with a terminator": the number-pair
 * rule above matches a mark on either side of each number, so
 * `FSI 3 PDI / FSI 10 PDI` — two values isolated separately by `isolateValue` — looks like
 * that and is NOT wrapped as a pair. The depth has to stay inside until the
 * end, or the two halves would never be held together and the slash would
 * still resolve to the paragraph.
 */
function wrapped(s: string): boolean {
  if (!(s[0] === LRI || s[0] === FSI || s[0] === RLI) || s[s.length - 1] !== PDI) return false;
  let depth = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === LRI || c === FSI || c === RLI) depth += 1;
    else if (c === PDI) depth -= 1;
    if (depth === 0 && i < s.length - 1) return false;
  }
  return depth === 0;
}

export function isolateLtrRuns(text: string): string {
  return text.replace(LTR_RUNS, (m: string, offset: number, whole: string) => {
    if (wrapped(m)) return m;
    const before = whole[offset - 1];
    const after = whole[offset + m.length];
    if ((before === LRI || before === FSI) && after === PDI) return m;
    return LRI + m + PDI;
  });
}

/**
 * ⚠⚠ TEXT FILEX DID NOT WRITE, made readable in `code`'s direction.
 *
 * A sentence the SERVER composed (`server.*`, reaching the browser as the
 * `message` of a refusal), an installed app's own words, a notification built
 * from what happened — none of it passes through `useLocale().t` or the admin
 * panel's post-translation hook, so none of it was isolated. It is exactly
 * the text that carries machine runs: a path, a URL, a command, a token
 * syntax.
 *
 * Measured in the Arabic admin panel (v0.43.0 pack round, `G:/filex-lang-ar`):
 * `server.token.scope_unknown` names the syntax `root:<storage>://<folder>`,
 * and the closing `>` — a neutral between the Latin run and the Arabic after
 * it — resolved to the paragraph's direction, jumped to the FAR LEFT of the
 * run and mirrored into `<`, so the line read `…<root:<storage>://<folder`.
 * The same sentence drawn from the catalogue was correct, which is what makes
 * this read as a broken pack rather than as a missing call.
 *
 * ⚠ A pack cannot fix it: bidi control characters are forbidden in a
 * translation and the pack's own validator refuses them (`BIDI`). It is the
 * render site's job, and this is the render site's one function.
 */
export function foreignText(code: string | null | undefined, text: string): string {
  return localeDir(code) === 'rtl' ? isolateLtrRuns(text) : text;
}

/**
 * A value interpolated into a sentence — a file or folder name, a person —
 * wrapped in FIRST-STRONG ISOLATE … POP DIRECTIONAL ISOLATE, so its own
 * letters decide its direction and it cannot drag the sentence around it:
 * `2026 report.pdf` inside an Arabic sentence keeps its number in front, and
 * an Arabic name inside a sentence keeps its own order.
 *
 * ⚠ RTL only (`useLocale().t`), and for DISPLAY: the marks are invisible but
 * real characters, so a string that becomes data — a file name, a command to
 * copy — must never come from a right-to-left `t()` with a value in it.
 */
export function isolateValue(v: string): string {
  return FSI + v + PDI;
}

/**
 * The `dir` for a piece of the person's CONTENT drawn inside the interface —
 * a file's first lines on its card. ⚠ Content is not interface: an English
 * README previewed in an Arabic explorer still reads left to right, and an
 * Arabic note in an English one right to left.
 *
 *   code  → `ltr`: machine text, whatever the language around it;
 *   text  → `auto`: the first letter of the text decides (a heading's `#`
 *           stays in front instead of trailing an RTL line);
 *   table → none: a table follows the interface, like every other table.
 */
export function contentDir(kind: string): 'ltr' | 'auto' | undefined {
  if (kind === 'code') return 'ltr';
  if (kind === 'text') return 'auto';
  return undefined;
}
