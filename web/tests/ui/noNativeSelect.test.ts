/**
 * NO NATIVE SELECT IN THE PRODUCT.
 *
 * The owner, 2026-10-04, on the Apps store's theme picker:
 *
 *   "hiç bir yerde öyle basit bir dropdown kullanma lütfen"
 *   ("don't use a plain dropdown like that anywhere, please")
 *
 * A native select element opens the OPERATING SYSTEM's list: it ignores the
 * palette, opens white over a dark panel, is a different control on every
 * platform and a sheet nobody designed on a phone. filex had 18 of them in
 * the explorer (packages/core) and one in the admin panel (web ui/Select,
 * used 40 times) when this gate was written (#160). The choice is ours now,
 * in two shapes, both in core:
 *
 *   - `ChoiceButtons` — two to four answers, every one readable without a
 *     click (`segmented` when it sits inline beside other fields);
 *   - `ChoiceSelect`  — a longer list, or one the data sizes. The admin
 *     panel's `ui/Select` is a labelled frame around this same component.
 *
 * What is measured here:
 *   1. THE SOURCE SCAN over BOTH trees (packages/core/src AND web/src): no
 *      select element in a template, in markup a script builds, or through
 *      `createElement` / `h()`. Comments are not code: a sentence ABOUT the
 *      element is not one (the comment stripper is the duplication scan's,
 *      scripts/dup-scan.mjs).
 *   2. The detector itself, on lines that must and must not trip it, so a
 *      regex edit cannot quietly turn the gate off.
 *   3. ui/Select IS core's ChoiceSelect — no second list beside it.
 *
 * HOW TO ANSWER IT WHEN IT FIRES
 * ------------------------------
 * Use `ChoiceSelect` (core) — or `ui/Select` in web/src, which wraps it with
 * the panel's label, hint and error — or `ChoiceButtons` for two to four
 * answers. There is no exemption list: an exemption list is where the next
 * native dropdown hides.
 */
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { stripComments } from '../../../scripts/dup-scan.mjs';
import Select from '@/components/ui/Select.vue';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');

/** Where the interface lives. */
const ROOTS = ['packages/core/src', 'web/src'];
const SKIP_DIR = /(^|\/)(node_modules|dist|locales|__tests__)(\/|$)/;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    const rel = path.relative(REPO, p).split(path.sep).join('/');
    if (SKIP_DIR.test(rel)) continue;
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(vue|ts|tsx|js|mjs|html)$/.test(name) && !/(\.d\.ts|\.test\.ts|\.spec\.ts)$/.test(name)) out.push(rel);
  }
  return out;
}

const FILES = ROOTS.flatMap((r) => walk(path.join(REPO, r)));

/**
 * The ways a native select element gets onto a page. Case-SENSITIVE on
 * purpose: in a Vue template `<Select` is a component (web ui/Select), and
 * only the lowercase tag is the browser's element.
 */
const NATIVE_SELECT = [
  /<select(?=[\s>/]|$)/,
  /createElement(?:NS)?\(\s*(?:[^,()]*,\s*)?['"`]select['"`]/,
  /\bh\(\s*['"`]select['"`]/,
];

/** The lines of `code` (comments already blanked) that draw a native select. */
function nativeSelectLines(code: string): number[] {
  const hits: number[] = [];
  code.split('\n').forEach((line, i) => {
    if (NATIVE_SELECT.some((re) => re.test(line))) hits.push(i + 1);
  });
  return hits;
}

function codeOf(file: string): string {
  const src = readFileSync(path.join(REPO, file), 'utf8').replace(/\r\n/g, '\n');
  return stripComments(src, path.extname(file)) as string;
}

describe('no native select — the source scan', () => {
  it('reads the trees it is meant to guard', () => {
    // A path typo would scan nothing and pass forever.
    expect(FILES).toContain('packages/core/src/components/ChoiceSelect.vue');
    expect(FILES).toContain('packages/core/src/modals/PermissionsModal.vue');
    expect(FILES).toContain('web/src/components/ui/Select.vue');
    expect(FILES.filter((f) => f.startsWith('packages/core/src/') && f.endsWith('.vue')).length).toBeGreaterThan(100);
    expect(FILES.filter((f) => f.startsWith('web/src/') && f.endsWith('.vue')).length).toBeGreaterThan(50);
  });

  it('no file in packages/core/src or web/src draws a native select element', () => {
    const found: string[] = [];
    for (const file of FILES) {
      const code = codeOf(file);
      const raw = readFileSync(path.join(REPO, file), 'utf8').replace(/\r\n/g, '\n').split('\n');
      for (const n of nativeSelectLines(code)) found.push(`  ${file}:${n}\n      ${raw[n - 1].trim()}`);
    }
    expect(
      found,
      'A native select element is back. Use core ChoiceSelect (ui/Select in web/src) for a list, or core ChoiceButtons for two to four answers:\n' +
        found.join('\n'),
    ).toEqual([]);
  });
});

/* The detector itself — so a regex edit or a comment-stripper change cannot
   quietly turn the gate off, or turn every mention into a failure. */
describe('no native select — the detector', () => {
  it('recognises the element however it is written', () => {
    const trips = [
      '<select v-model="x" class="fe-input">',
      '        <select',
      '<select>',
      '<select/>',
      "el.innerHTML = '<select name=\"a\"></select>';",
      "const s = document.createElement('select');",
      'const s = document.createElementNS(NS, "select");',
      "return h('select', { value }, kids);",
    ];
    for (const l of trips) expect(nativeSelectLines(l), l).toEqual([1]);
  });

  it('leaves components, other words and mentions in comments alone', () => {
    const clean = [
      '<Select v-model="role" :options="roleOptions" />',
      '<ChoiceSelect :model-value="x" :options="o" />',
      '<selection-box />',
      "const selected = rows.filter((r) => r.selectable);",
      "emit('select', action);",
      "const picked = document.querySelector('[data-select]');",
    ];
    for (const l of clean) expect(nativeSelectLines(l), l).toEqual([]);

    // A sentence ABOUT the element, in each kind of comment the trees hold.
    const vue = [
      '<template>',
      '  <!-- not a <select> here -->',
      '  <div />',
      '</template>',
      '<script setup lang="ts">',
      '// a <select> used to sit here',
      '/* and a <select> in a block */',
      'const x = 1;',
      '</script>',
    ].join('\n');
    expect(nativeSelectLines(stripComments(vue, '.vue') as string)).toEqual([]);
    const ts = ['/**', ' * a <select> mentioned', ' */', "export const a = 'b'; // <select>"].join('\n');
    expect(nativeSelectLines(stripComments(ts, '.ts') as string)).toEqual([]);
    // …and the code next to a comment is still read.
    const mixed = ['<!-- a note -->', '<select v-model="x"></select>'].join('\n');
    expect(nativeSelectLines(stripComments(mixed, '.vue') as string)).toEqual([2]);
  });
});

describe('ui/Select is core ChoiceSelect — one list in the product', () => {
  it('renders the core combobox and no native element', () => {
    const w = mount(Select, {
      props: { modelValue: 'b', options: [{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }], label: 'Pick' },
      attachTo: document.body,
    });
    expect(w.find('select').exists()).toBe(false);
    const combo = w.get('[role="combobox"]');
    expect(combo.classes()).toContain('fe-select__trigger');
    expect(combo.attributes('data-value')).toBe('b');
    // The label names the control (for → id), as it named the native one.
    expect(w.get('label').attributes('for')).toBe(combo.attributes('id'));
  });

  it('imports the core component rather than drawing a list of its own', () => {
    const src = readFileSync(path.join(REPO, 'web/src/components/ui/Select.vue'), 'utf8');
    expect(src).toMatch(/import\s*\{\s*ChoiceSelect\s*\}\s*from\s*'@brftech\/filex-core'/);
    expect(codeOf('web/src/components/ui/Select.vue')).not.toMatch(/role="(listbox|option)"/);
  });
});
