// Nothing on screen says an em dash or an en dash: a plain hyphen-minus only.
// Nor a character that passes for a hyphen without being one - a
// non-breaking hyphen (U+2011), a typographic hyphen (U+2010), a figure dash
// (U+2012), a horizontal bar (U+2015), a minus sign (U+2212) used as a dash
// or in a number. The one exception is a label that IS the minus sign alone
// (a zoom-out button beside "+"). The list and the rule are
// scripts/i18n-validate.mjs's (DASH_LOOKALIKES, dashProblem): one definition
// for filex's own texts and for every language pack.
//
// ⚠ Why (owner's ruling, 2026-09-30): the long dashes read as a machine's
// hand in the interface ("the AI dash"), and about 1,100 of them had piled up
// in the en/tr catalogues, most written by agents. Every one became "-"; this
// keeps them from coming back. Ranges are "a-z", "3-60", an empty cell is "-".
//
// Two halves:
//   1. every catalogue a person or a translator reads: the explorer's, the
//      admin panel's, the server's `server.*` texts and the notes for
//      translators - read line by line, comments included;
//   2. every word written straight into code that can reach a screen: Vue
//      templates, attributes, bindings and scripts, TypeScript in core, the
//      admin app and the desktop app, the desktop's HTML pages, the web shell
//      and service worker, and every Go string outside the tests (the drop
//      page, the public share page, error texts the API hands to the
//      interface, CLI output). A parser reads each file, so COMMENTS stay out
//      of it: a note beside the code is nobody's screen.
//
// WHEN IT FIRES: write "-". Code that has to RECOGNISE a long dash (reading a
// label an older client sent, say) builds it from its code point -
// `String.fromCharCode(0x2014)` in TypeScript, `string(rune(0x2014))` in Go -
// which says on the line that it is a character being matched, not text
// being shown. Only when that is impossible does an entry go in ALLOW below,
// with its reason; an entry that no longer matches anything fails the test.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { NOTIFICATION_PHRASES } from '@brftech/filex-core/src/lib/notificationText';
// A plain .mjs: the one definition of what is not a plain hyphen.
import { dashProblem } from '../../../scripts/i18n-validate.mjs';
import {
  cssStrings,
  goStrings,
  htmlStrings,
  jsonStrings,
  sourceFiles,
  tsStrings,
  vueStrings,
  type Shown,
} from '../helpers/shownStrings';

const ROOT = path.resolve(__dirname, '../../..');

const CATALOGUES = [
  'packages/core/src/locales/en.ts',
  'packages/core/src/locales/tr.ts',
  'web/src/locales/en.json',
  'web/src/locales/tr.json',
  'backend/internal/srvtext/locales/en.json',
  'backend/internal/srvtext/locales/tr.json',
  'backend/internal/srvtext/locales/context.json',
];

// Built from their code points, so no editor or tool can quietly turn the
// check itself into the character it looks for (or into a plain hyphen).
const EM_DASH = String.fromCharCode(0x2014);
const EN_DASH = String.fromCharCode(0x2013);
const NB_HYPHEN = String.fromCharCode(0x2011);
const MINUS = String.fromCharCode(0x2212);
const BS = String.fromCharCode(92);
// The same characters spelled as an escape (Go's \u2014, \U00002014) or an
// HTML entity (&mdash; &ndash; &minus; &#8212; &#x2011;): still on screen.
// TypeScript escapes need no pattern - the parser hands over the cooked text.
const SPELLED = new RegExp(
  `${BS}${BS}u(201[0-5]|2212)|${BS}${BS}U0000(201[0-5]|2212)|&(mdash|ndash|minus|#82(0[89]|1[0-3])|#8722|#x(201[0-5]|2212));`,
  'i',
);
// A label that is the minus sign alone is a symbol: dashProblem passes the
// character, and the entity gets the same pass here.
const isLong = (s: string) => dashProblem(s) !== null || (SPELLED.test(s) && s.trim().toLowerCase() !== '&minus;');
const FIX = 'write "-" (a plain hyphen) instead of a long dash or a character that passes for a hyphen';

function longDashes(file: string): string[] {
  const out: string[] = [];
  const lines = fs.readFileSync(path.join(ROOT, file), 'utf8').split('\n');
  lines.forEach((line, i) => {
    if (isLong(line)) out.push(`${file}:${i + 1}: ${line.trim()}`);
  });
  return out;
}

describe('translations use a plain hyphen, never a long dash', () => {
  it.each(CATALOGUES)('%s', (file) => {
    expect(longDashes(file), FIX).toEqual([]);
  });

  // The notification phrases are a table inside code (the catalogue exports
  // them as `server.notify.*`), and the file's comments may say what they
  // like, so the VALUES are read, not the lines. file.infected's
  // "{signature} - {path}" was the one left behind the first time.
  it('packages/core/src/lib/notificationText.ts NOTIFICATION_PHRASES', () => {
    const found: string[] = [];
    const walk = (v: unknown, at: string) => {
      if (typeof v === 'string') {
        if (isLong(v)) found.push(`${at}: ${v}`);
      } else if (v && typeof v === 'object') {
        for (const [k, x] of Object.entries(v)) walk(x, `${at}.${k}`);
      }
    };
    walk(NOTIFICATION_PHRASES, 'NOTIFICATION_PHRASES');
    expect(Object.keys(NOTIFICATION_PHRASES).length).toBeGreaterThan(10);
    expect(found, FIX).toEqual([]);
  });
});

// ── the interface code ───────────────────────────────────────────────────

/** A long dash the code has to keep. `file` is repo-relative, `text` a piece of the literal. */
interface Allow {
  file: string;
  text: string;
  why: string;
}
const ALLOW: Allow[] = [];

function reader(name: string): (src: string) => Shown[] {
  if (name.endsWith('.vue')) return vueStrings;
  if (name.endsWith('.go')) return goStrings;
  if (name.endsWith('.css')) return (src) => cssStrings(src);
  if (name.endsWith('.json')) return jsonStrings;
  if (/\.(html|svg)$/.test(name)) return htmlStrings;
  return (src) => tsStrings(src);
}

const used = new Set<Allow>();

/** "file:line: text" for every shown line of `src` that carries a long dash. */
function shownDashes(name: string, src: string): string[] {
  const out: string[] = [];
  for (const s of reader(name)(src)) {
    s.text.split('\n').forEach((l, k) => {
      if (!isLong(l)) return;
      const allowed = ALLOW.find((a) => a.file === name && l.includes(a.text));
      if (allowed) used.add(allowed);
      else out.push(`${name}:${s.line + k}: ${l.trim().slice(0, 140)}`);
    });
  }
  return out;
}

const rel = (f: string) => path.relative(ROOT, f).split(path.sep).join('/');
const isTest = (f: string) => /(_test\.go|\.(test|spec)\.[cm]?[jt]s)$/.test(f);

/** Where the interface lives, and which files of each place are read. */
const AREAS: Array<{ area: string; files: () => string[]; atLeast: number }> = [
  {
    area: 'packages/core/src',
    files: () => sourceFiles(path.join(ROOT, 'packages/core/src'), /\.(vue|[cm]?ts|js|css)$/),
    atLeast: 200,
  },
  {
    area: 'packages/react/src + packages/webcomponent/src',
    files: () => [
      ...sourceFiles(path.join(ROOT, 'packages/react/src'), /\.(vue|[cm]?ts|tsx|js|css)$/),
      ...sourceFiles(path.join(ROOT, 'packages/webcomponent/src'), /\.(vue|[cm]?ts|js|css)$/),
    ],
    atLeast: 2,
  },
  {
    area: 'web/src',
    files: () => sourceFiles(path.join(ROOT, 'web/src'), /\.(vue|[cm]?ts|js|css)$/),
    atLeast: 150,
  },
  {
    area: 'desktop/src',
    files: () => sourceFiles(path.join(ROOT, 'desktop/src'), /\.([cm]?ts|js|css|html)$/),
    atLeast: 20,
  },
  {
    // The installed web app's name comes from its manifest (vite.config.ts);
    // the desktop package's description is what a package manager shows.
    area: 'pages (web shell, app manifest, service worker, desktop windows, desktop package)',
    files: () => [
      path.join(ROOT, 'web/index.html'),
      path.join(ROOT, 'web/vite.config.ts'),
      ...sourceFiles(path.join(ROOT, 'web/public'), /\.(js|svg|html)$/),
      ...sourceFiles(path.join(ROOT, 'desktop/ui'), /\.(html|js|css)$/),
      path.join(ROOT, 'desktop/package.json'),
    ],
    atLeast: 7,
  },
  {
    area: 'backend (Go strings)',
    files: () => sourceFiles(path.join(ROOT, 'backend'), /\.go$/),
    atLeast: 300,
  },
];

describe('the interface code uses a plain hyphen, never a long dash', () => {
  it('reads what is shown and skips what is a comment', () => {
    const d = EM_DASH;
    const samples: Record<string, string> = {
      'x.vue': [
        '<template>',
        `  <!-- HIDDEN ${d} -->`,
        `  <p title="SHOWN1 ${d}">SHOWN2 ${d} {{ ok ? 'SHOWN3 ${d}' : '' }}</p>`,
        `  <b :aria-label="\`SHOWN4 ${d} \${n}\`">&mdash; SHOWN5</b>`,
        '</template>',
        '<script setup lang="ts">',
        `// HIDDEN ${d}`,
        `const s = 'SHOWN6 ${d}'; /* HIDDEN ${d} */`,
        `const r = /HIDDEN${d}/;`,
        '</script>',
        `<style>/* HIDDEN ${d} */ p::after { content: 'SHOWN7 ${d}'; }</style>`,
      ].join('\n'),
      'x.ts': [
        `/** HIDDEN ${d} */`,
        `const a = "SHOWN1 ${d}";`,
        `const b = \`SHOWN2 ${d} \${a} SHOWN3 ${d}\`; // HIDDEN ${d}`,
        `const c = '${BS}u2014 SHOWN4';`,
        `const r = /HIDDEN[${d}]/;`,
      ].join('\n'),
      'x.go': [
        'package x',
        `// HIDDEN ${d}`,
        `/* HIDDEN ${d} */`,
        `var a = "SHOWN1 ${d}" // HIDDEN ${d}`,
        `var b = \`first line`,
        `SHOWN2 ${d}\``,
        `var c = "SHOWN3 ${BS}u2014"`,
        `var r = '${d}' // HIDDEN: a rune is a character to match`,
        `var e = "not // a comment: SHOWN4 ${d}"`,
      ].join('\n'),
      'x.html': [
        `<!-- HIDDEN ${d} -->`,
        `<title>SHOWN1 ${d}</title>`,
        '<script>',
        `  // HIDDEN ${d}`,
        `  const x = 'SHOWN2 ${d}';`,
        '</script>',
        `<style>/* HIDDEN ${d} */ p::before { content: 'SHOWN3 ${d}' }</style>`,
        '<p>SHOWN4 &ndash;</p>',
      ].join('\n'),
      'x.json': JSON.stringify({ description: `SHOWN1 ${d}`, keywords: ['ok', `SHOWN2 ${EN_DASH}`] }, null, 2),
    };
    const shown: Record<string, number> = { 'x.vue': 7, 'x.ts': 4, 'x.go': 4, 'x.html': 4, 'x.json': 2 };
    for (const [name, src] of Object.entries(samples)) {
      const hits = shownDashes(name, src).join('\n');
      expect(hits, name).not.toContain('HIDDEN');
      for (let i = 1; i <= shown[name]; i++) expect(hits, `${name} SHOWN${i}`).toContain(`SHOWN${i}`);
    }
  });

  it('a character that passes for a hyphen is caught too; the minus sign alone as a label is not', () => {
    const src = [
      '<template>',
      `  <p>SHOWN1 read${NB_HYPHEN}only</p>`,
      `  <p>SHOWN2 priority ${MINUS}1000</p>`,
      `  <p title="SHOWN3 a ${String.fromCharCode(0x2012)} b">SHOWN4 x ${String.fromCharCode(0x2015)} y</p>`,
      '  <p>SHOWN5 &minus;1</p>',
      `  <button type="button" aria-label="Zoom out">${MINUS}</button>`,
      '  <button type="button" aria-label="Zoom out">&minus;</button>',
      `  <b>SHOWN6 ${String.fromCharCode(0x2010)}</b>`,
      '</template>',
      '<script setup lang="ts">',
      `const z = '${MINUS}';`,
      `const w = 'SHOWN7 a ${MINUS} b';`,
      '</script>',
    ].join('\n');
    const hits = shownDashes('y.vue', src);
    const text = hits.join('\n');
    for (let i = 1; i <= 7; i++) expect(text, `y.vue SHOWN${i}`).toContain(`SHOWN${i}`);
    expect(hits, 'the minus sign alone - two buttons, one constant - passes').toHaveLength(7);
    expect(dashProblem(`read${NB_HYPHEN}only`)).toContain('U+2011');
    expect(dashProblem(` ${MINUS} `)).toBeNull();
    expect(dashProblem('read-only, 3-60, -1')).toBeNull();
  });

  // Each area is read once: its own case and the allowance check share it.
  const scans = new Map<string, { files: number; found: string[] }>();
  const scan = ({ area, files }: (typeof AREAS)[number]) => {
    let s = scans.get(area);
    if (!s) {
      const list = files().filter((f) => !isTest(f));
      s = { files: list.length, found: list.flatMap((f) => shownDashes(rel(f), fs.readFileSync(f, 'utf8'))) };
      scans.set(area, s);
    }
    return s;
  };

  it.each(AREAS)('$area', (a) => {
    const { files, found } = scan(a);
    // A scan of nothing passes everything.
    expect(files).toBeGreaterThanOrEqual(a.atLeast);
    expect(found, FIX).toEqual([]);
  });

  it('every allowance still matches something', () => {
    AREAS.forEach(scan);
    expect(ALLOW.filter((a) => !used.has(a)).map((a) => `${a.file}: ${a.text}`)).toEqual([]);
  });
});
