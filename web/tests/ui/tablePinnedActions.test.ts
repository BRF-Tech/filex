/**
 * ONE TABLE IN THE PRODUCT, and it is the explorer's.
 *
 * The owner, 2026-09-21, verbatim:
 *
 *   "Artık explore tablomuz bizim her yerde kullanacağımız tablo yapısıdır;
 *    bir yere tablo gerekiyorsa bu tabloyu koymak zorundayız. Bunu kural
 *    olarak yazalım, çok önemli bir kural."
 *   "Admin tabloları hâlâ explore tablolarıyla AYNI KODDA DEĞİL. LÜTFEN AYNI
 *    KODA ALALIM. Örnek veriyorum: tablo sütunları düzenlenebilir değil,
 *    büyütme küçültme yok, sıralama yok."
 *
 * The history this file carries, because it is the reason for every check:
 *
 *   2026-09-19 — "admin panelinde bütün tablolarımızda işlemler bölgesi sağda
 *   sabit kalsın" was answered by teaching each hand-rolled `<table>` two
 *   classes, and a scan kept them in step.
 *   2026-09-20 — "biri anya biri konya" was answered by `ui/Table.vue`: ONE
 *   admin table… that IMITATED the explorer's list (the same frozen edges,
 *   the same Actions menu) and got only the parts somebody copied. Resizing,
 *   sorting, the column menu and remembering all lived in the explorer's code
 *   and never reached it. This file then asserted `ui/Table.vue`'s classes —
 *   i.e. it GUARDED THE IMITATION.
 *   2026-09-21 — the table is `packages/core/src/components/DataTable.vue`:
 *   the explorer's list view with the files taken out of it. The explorer's
 *   own listing renders through it, and so does every other table. The
 *   imitation is deleted.
 *
 * What is measured here, in order:
 *   1. DataTable's own contract — the capabilities every table gets: the lead
 *      frozen left and ONE Actions control right, sorting (and the honest
 *      refusal to sort one page of a paged list), resizing, the column menu,
 *      and remembering the arrangement per table.
 *   2. The explorer's listing IS DataTable — no second copy beside it.
 *   3. THE SOURCE SCAN over BOTH trees (web/src AND packages/core/src — the
 *      blind spot of 2026-09-20 was scanning only one): no `<table>`, no
 *      table markup or table roles outside DataTable, no imitation left
 *      behind, and every DataTable carries a table id of its own.
 *   4. The stylesheet: the `fe-list` rules are the table's, the `.tbl` table
 *      rules are gone, and nothing carries a colour a palette cannot move.
 *
 * The geometry (a sticky cell inside the viewport at 700px, still clickable)
 * is measured in a real browser: web/cypress/e2e/41-users-crud.cy.ts.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';

import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import ListView from '@brftech/filex-core/src/components/ListView.vue';
import {
  __flushViewPrefs,
  __resetViewPrefs,
  attachViewPrefsStore,
} from '@brftech/filex-core/src/lib/viewPrefs';

type Row = { id: number; name: string; size: number };

const ROWS: Row[] = [
  { id: 1, name: 'beta', size: 20 },
  { id: 2, name: 'Alpha', size: 300 },
  { id: 3, name: 'gamma', size: 1 },
];

const COLUMNS = [
  { id: 'name', label: 'Name', sortable: true, width: 200 },
  { id: 'size', label: 'Size', sortable: true, width: 100, align: 'right' as const },
];

const settle = () => new Promise<void>((r) => setTimeout(r, 0));

const mounted: { unmount(): void }[] = [];
function draw(extra: Record<string, unknown> = {}) {
  const w = mount(DataTable, {
    props: { columns: COLUMNS, rows: ROWS, rowKey: 'id', tableId: 'test.table', ...extra },
  });
  mounted.push(w);
  return w;
}

/** A row's (or the header's) own cells, in order — `:scope` is not reliable
 *  in the test DOM, so the children are read directly. */
function cellsOf(el: Element): Element[] {
  return Array.from(el.children).filter((c) => c.classList.contains('fe-list__col'));
}

function names(w: ReturnType<typeof draw>): string[] {
  return w.findAll('.fe-list__row').map((r) => r.find('.fe-list__col--lead').text());
}

beforeEach(() => {
  __resetViewPrefs();
});
afterEach(() => {
  // Unmount, never wipe <body>: a wiped body under a live component makes
  // its next update throw, and the column menu is teleported there.
  while (mounted.length) mounted.pop()!.unmount();
});

describe('DataTable — the frozen edges', () => {
  it('draws the lead first and marks it the lead, in the header and in every row', () => {
    const w = draw();
    const head = cellsOf(w.find('.fe-list__head').element);
    expect(head[0].classList.contains('fe-list__col--lead')).toBe(true);
    expect(head[0].getAttribute('data-col')).toBe('name');
    for (const r of w.findAll('.fe-list__row')) {
      expect(cellsOf(r.element)[0].classList.contains('fe-list__col--lead')).toBe(true);
    }
  });

  it('a `lead: true` column is drawn first wherever it was declared', () => {
    const w = draw({ columns: [COLUMNS[1], { ...COLUMNS[0], lead: true }] });
    expect(cellsOf(w.find('.fe-list__head').element)[0].getAttribute('data-col')).toBe('name');
  });

  it('ends every row in the trailing column — ONE Actions control when the row has verbs', () => {
    const w = draw({
      rowActions: (r: Row) => (r.id === 3 ? [] : [{ key: 'edit', label: 'Edit' }]),
    });
    const rows = w.findAll('.fe-list__row');
    for (const r of rows) {
      const cells = cellsOf(r.element);
      expect(cells[cells.length - 1].classList.contains('fe-list__col--menu')).toBe(true);
    }
    // A row with verbs: exactly one control. A row with NONE: no control —
    // a disabled "Actions" over nothing is a promise the row cannot keep.
    const byName = (n: string) => rows.find((r) => r.find('.fe-list__col--lead').text() === n)!;
    expect(byName('beta').findAll('.tbl-rowactions__btn')).toHaveLength(1);
    expect(byName('gamma').findAll('.tbl-rowactions__btn')).toHaveLength(0);
    // The header's trailing cell is the column menu, the same ⋮ the explorer has.
    const head = cellsOf(w.find('.fe-list__head').element);
    expect(head[head.length - 1].querySelector('.fe-list__colmenu-btn')).not.toBeNull();
  });
});

describe('DataTable — sorting, honestly', () => {
  it('a header click sorts the rows, a second click reverses them, and aria-sort says so', async () => {
    const w = draw();
    expect(names(w)).toEqual(['beta', 'Alpha', 'gamma']);
    const btn = w.find('.fe-list__head [data-col="name"] button.fe-list__sort');
    await btn.trigger('click');
    // The viewer's collation, case-insensitive.
    expect(names(w)).toEqual(['Alpha', 'beta', 'gamma']);
    expect(w.find('.fe-list__head [data-col="name"]').attributes('aria-sort')).toBe('ascending');
    await btn.trigger('click');
    expect(names(w)).toEqual(['gamma', 'beta', 'Alpha']);
    expect(w.find('.fe-list__head [data-col="name"]').attributes('aria-sort')).toBe('descending');
  });

  it('numbers sort as numbers', async () => {
    const w = draw();
    await w.find('.fe-list__head [data-col="size"] button.fe-list__sort').trigger('click');
    expect(names(w)).toEqual(['gamma', 'beta', 'Alpha']);
  });

  it('CLOSES its headers over one page of a paged list, and says why', async () => {
    // ⚠⚠ Sorting 25 rows of 300 and calling it sorted is the lie this refuses
    // (Shares used to do exactly that; Users drew an arrow and moved nothing).
    const w = draw({ page: 1, pageSize: 3, total: 9 });
    const btn = w.find('.fe-list__head [data-col="name"] button.fe-list__sort');
    expect(btn.attributes('disabled')).toBeDefined();
    expect(btn.attributes('title')).toMatch(/pages/i);
    // A scripted click must not get through the affordance either.
    (btn.element as HTMLButtonElement).disabled = false;
    await btn.trigger('click');
    expect(names(w)).toEqual(['beta', 'Alpha', 'gamma']);
    expect(w.emitted('sort')).toBeUndefined();
  });

  it('a CONTROLLED sort is the caller’s: the table reports the click and moves nothing', async () => {
    const w = draw({ sort: { key: 'size', dir: 'asc' } });
    expect(names(w)).toEqual(['beta', 'Alpha', 'gamma']);
    expect(w.find('.fe-list__head [data-col="size"]').attributes('aria-sort')).toBe('ascending');
    await w.find('.fe-list__head [data-col="name"] button.fe-list__sort').trigger('click');
    expect(w.emitted('sort')?.[0]).toEqual([{ key: 'name', dir: 'asc' }]);
    expect(names(w)).toEqual(['beta', 'Alpha', 'gamma']);
  });
});

describe('DataTable — resizing, the column menu, and remembering', () => {
  it('every resizable column has a focusable handle; the arrow keys resize', async () => {
    const w = draw();
    const handle = w.find('.fe-list__head [data-col="size"] .fe-list__resize');
    expect(handle.attributes('role')).toBe('separator');
    expect(handle.attributes('tabindex')).toBe('0');
    const before = w.find('.fe-list__head [data-col="size"]').attributes('style');
    await handle.trigger('keydown', { key: 'ArrowRight' });
    await nextTick();
    const after = w.find('.fe-list__head [data-col="size"]').attributes('style');
    expect(before).toContain('100px');
    expect(after).toContain('116px');
  });

  it('the column menu hides a column — and the lead is not the person’s to hide', async () => {
    const w = draw();
    await w.find('.fe-list__colmenu-btn').trigger('click');
    await nextTick();
    const rows = Array.from(document.querySelectorAll('.fe-colmenu [role="checkbox"]'));
    expect(rows.map((r) => r.textContent?.trim())).toEqual(['Size']);
    (rows[0] as HTMLElement).click();
    await nextTick();
    expect(w.find('.fe-list__head [data-col="size"]').exists()).toBe(false);
    expect(w.find('.fe-list__row [data-col="size"]').exists()).toBe(false);
  });

  it('the arrangement is saved on the account under the table’s own id', async () => {
    const saved: Record<string, unknown>[] = [];
    attachViewPrefsStore({ load: async () => ({}), save: (d) => saved.push(d as Record<string, unknown>) });
    await settle();
    const w = draw();
    await w.find('.fe-list__head [data-col="size"] .fe-list__resize').trigger('keydown', { key: 'ArrowRight' });
    await w.find('.fe-list__head [data-col="name"] button.fe-list__sort').trigger('click');
    __flushViewPrefs();
    const doc = saved[saved.length - 1] as { t?: Record<string, { c?: { w?: Record<string, number> }; s?: unknown }> };
    expect(doc.t?.['test.table']?.c?.w?.size).toBe(116);
    expect(doc.t?.['test.table']?.s).toEqual({ k: 'name', d: 'asc' });
  });
});

describe('the explorer’s listing IS the table', () => {
  it('ListView renders through DataTable — there is no second copy beside it', () => {
    const w = mount(ListView, {
      props: {
        files: [{ path: 's://a.txt', basename: 'a.txt', type: 'file', size: 1, extension: 'txt' }],
        selected: new Set<string>(),
        locale: 'en',
      },
    });
    expect(w.findComponent(DataTable).exists()).toBe(true);
    // The explorer's own classes are still drawn — by DataTable.
    expect(w.find('.fe-list__col--name.fe-list__col--lead').exists()).toBe(true);
  });
});

/* ------------------------------------------------------------------ */
/* The source scan — the rule that closes the whole class of drift      */
/* ------------------------------------------------------------------ */

const SRC = path.resolve(__dirname, '../../src');
/** ⚠⚠ The half the scan could not see until 2026-09-20: five of the panel's
 *  tables are core components. Both trees, always. */
const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');
const TABLE_FILE = 'components/DataTable.vue';

function vueFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) vueFiles(full, out);
    else if (entry.endsWith('.vue')) out.push(full);
  }
  return out;
}

function rel(root: string, f: string): string {
  return path.relative(root, f).replace(/\\/g, '/');
}

/** A file's template and script with the comments and the <style> taken
 *  out — a file that EXPLAINS it used to be a `<table>` is not one. */
function code(f: string): string {
  return readFileSync(f, 'utf8')
    .replace(/<style[\s\S]*?<\/style>/g, '')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '');
}

/** The `<template #cell-<id>>…</template>` blocks of one file. */
function cellSlots(src: string): { name: string; body: string }[] {
  const out: { name: string; body: string }[] = [];
  const open = /<template\s+#cell-([A-Za-z0-9_.-]+)(?:="[^"]*")?\s*>/g;
  for (let m = open.exec(src); m; m = open.exec(src)) {
    const from = m.index + m[0].length;
    const scan = /<template\b|<\/template>/g;
    scan.lastIndex = from;
    let depth = 1;
    let end = -1;
    for (let t = scan.exec(src); t; t = scan.exec(src)) {
      depth += t[0] === '</template>' ? -1 : 1;
      if (depth === 0) {
        end = t.index;
        break;
      }
    }
    if (end >= 0) out.push({ name: m[1], body: src.slice(from, end) });
  }
  return out;
}

const VOID_TAGS = new Set(['br', 'hr', 'img', 'input', 'source', 'wbr']);

/** The slot's ROOT nodes — the boxes the cell lays out beside one another —
 *  as their opening tags. */
function rootNodes(body: string): string[] {
  const out: string[] = [];
  const tag = /<(\/?)([A-Za-z][\w.-]*)((?:"[^"]*"|'[^']*'|[^>"'])*?)(\/?)>/g;
  let depth = 0;
  for (let m = tag.exec(body); m; m = tag.exec(body)) {
    const [, closing, name, attrs, self] = m;
    if (closing) {
      depth -= 1;
      continue;
    }
    if (depth === 0) out.push(`<${name}${attrs}>`);
    if (!self && !VOID_TAGS.has(name.toLowerCase())) depth += 1;
  }
  return out;
}

/** A root node that means "start a new line": a top/bottom margin. Inline
 *  spacing (`ms-`/`me-`) is what a flex row is FOR and is not matched. */
const STACKS = /class="(?:[^"]*\s)?(?:mt|mb)-[\w.[\]/-]+/;

/** A root that is a PILL — the panel's `Badge`, the one way a page draws a
 *  label with its own background and border. Squeezed below its own width it
 *  overflows under whatever is beside it (see the rule that uses it). */
const PILL = /^<Badge[\s>/]/;

/** Roots with `v-else` / `v-else-if` folded into the root before them: only
 *  one of a branch is ever drawn, so a branch is ONE box. */
function branches(roots: string[]): string[] {
  const out: string[] = [];
  for (const r of roots) {
    if (out.length && /\sv-else(-if)?[=\s>]/.test(r)) continue;
    out.push(r);
  }
  return out;
}

const USE_INSTEAD =
  'Use the product’s ONE table: `DataTable` (packages/core/src/components/DataTable.vue — ' +
  '`import { DataTable } from "@brftech/filex-core"` in web/src). It is the explorer’s own ' +
  'table, so resizing, sorting, the column menu, the frozen lead and Actions column and a ' +
  'remembered arrangement come with it. A table of your own gets none of them, and only the ' +
  'owner notices. The rule and why: docs/CONTRIBUTING.md → "UI rules" → "One table".';

describe('the product has exactly one table', () => {
  const files = [
    ...vueFiles(SRC).map((f) => ({ f, tree: 'web/src', r: rel(SRC, f) })),
    ...vueFiles(CORE_SRC).map((f) => ({ f, tree: 'packages/core/src', r: rel(CORE_SRC, f) })),
  ];

  it('finds the sources at all, in BOTH trees', () => {
    // ⚠ An empty list makes every check below pass by saying nothing
    // (filex lesson #93) — so each tree has a floor of its own.
    expect(files.filter((x) => x.tree === 'web/src').length).toBeGreaterThan(30);
    expect(files.filter((x) => x.tree === 'packages/core/src').length).toBeGreaterThan(30);
  });

  it('no file in either tree writes a `<table>` — not one, no exemptions', () => {
    const offenders = files.filter((x) => /<table\b/.test(code(x.f))).map((x) => `${x.tree}/${x.r}`);
    expect(offenders, `These files draw a raw <table>. ${USE_INSTEAD}`).toEqual([]);
  });

  it('no file but DataTable draws table markup or table roles (a second table component)', () => {
    /* A second table does not have to say `<table>`: the imitation this rule
       replaces was a <table>, but the next one could as easily be divs with
       table roles, or a copy of the explorer's `fe-list` markup. Any of those
       outside the one file that owns them is a second table. */
    const MARKERS: [RegExp, string][] = [
      [/<(thead|tbody|tfoot|tr|td|th)\b/, 'table elements'],
      [/role="(grid|table|treegrid|rowgroup|columnheader|rowheader|gridcell)"/, 'table roles'],
      [/\bfe-list__(head|row|col)\b/, 'the table’s own markup (fe-list__head/row/col)'],
    ];
    const offenders: string[] = [];
    for (const x of files) {
      if (x.tree === 'packages/core/src' && x.r === TABLE_FILE) continue;
      const src = code(x.f);
      const tpl = src.slice(src.indexOf('<template'));
      for (const [re, what] of MARKERS) {
        if (re.test(tpl)) offenders.push(`${x.tree}/${x.r} (${what})`);
      }
    }
    expect(offenders, `A second table component. ${USE_INSTEAD}`).toEqual([]);
  });

  it('the imitation is gone and nothing reaches for it', () => {
    for (const gone of [
      path.join(SRC, 'components/ui/Table.vue'),
      path.join(SRC, 'components/ui/TableScroll.vue'),
      path.join(SRC, 'components/ui/RowActions.vue'),
      path.join(CORE_SRC, 'composables/useTableScroll.ts'),
      path.join(SRC, 'styles/table.css'),
    ]) {
      expect(existsSync(gone), `${gone} is back — that is a second table`).toBe(false);
    }
    const offenders = files
      .filter((x) => /ui\/Table\.vue|TableScroll|useTableScroll/.test(code(x.f)))
      .map((x) => `${x.tree}/${x.r}`);
    expect(offenders, USE_INSTEAD).toEqual([]);
  });

  it('every DataTable has a table id of its own — where its arrangement is remembered', () => {
    /* A table with no id still works, but forgets every column a person
       sizes; two with the SAME literal id would overwrite each other's
       arrangement. A bound `:table-id` is accepted (SurfaceList builds one per
       app and node; CsvViewer deliberately passes none — its columns are
       whatever each file has, and says so beside the binding). */
    const missing: string[] = [];
    const ids = new Map<string, string>();
    const dupes: string[] = [];
    for (const x of files) {
      const src = code(x.f);
      for (const m of src.matchAll(/<DataTable\b([\s\S]*?)\/?>/g)) {
        const attrs = m[1];
        const lit = /\stable-id="([^"]+)"/.exec(attrs);
        /* A bound id, or the explorer's own store (its listing is remembered
           per FOLDER through `:column-store`, lib/viewPrefs). */
        const bound = /\s:(table-id|column-store)="/.test(attrs);
        if (!lit && !bound) missing.push(`${x.tree}/${x.r}`);
        if (lit) {
          const prev = ids.get(lit[1]);
          if (prev && prev !== `${x.tree}/${x.r}`) dupes.push(`${lit[1]}: ${prev} + ${x.tree}/${x.r}`);
          ids.set(lit[1], `${x.tree}/${x.r}`);
        }
      }
    }
    expect(ids.size, 'the scan found no DataTable at all — it is not reading what it thinks').toBeGreaterThan(25);
    expect(missing, 'a DataTable without `table-id`').toEqual([]);
    expect(dupes, 'two tables share a table id').toEqual([]);
  });

  it('a cell slot that puts a second LINE in a cell wraps it in ONE element', () => {
    /* ⚠⚠ A DataTable cell is a flex ROW (`.fe-list__row .fe-list__col {
       display: flex }`), so a `mt-1` on a second root node of a `#cell-*`
       slot is a margin on a flex ITEM: it does not start a new line, it
       nudges the box DOWN over the one beside it.

       Measured in v0.43.0 QA on the Apps table's Label cell, which put a
       `<span>` label, a `<Badge class="ms-1">` and `<AppPluginLanguages
       class="mt-1">` side by side in a 180px cell: the badge was drawn over
       the label and the coverage line over the badge, at 958px and at
       1440px, in English as well as under a long German label. The cell
       below it (`#cell-state`) wraps its two lines in one `<div>` and is
       right.

       `.tbl-sub` as a DIRECT child is the one shape the stylesheet handles
       (`.fe-list__cell:has(> .tbl-sub)` turns the cell into a column); a
       margin class is not, and never was. Mutually exclusive roots
       (`v-if` / `v-else-if` / `v-else`) are ONE root — only one is drawn. */
    const offenders: string[] = [];
    for (const x of files) {
      for (const slot of cellSlots(code(x.f))) {
        const roots = branches(rootNodes(slot.body));
        if (roots.length < 2) continue;
        const stacked = roots.filter((r) => STACKS.test(r));
        if (stacked.length) offenders.push(`${x.tree}/${x.r} #cell-${slot.name}: ${stacked.join(' + ')}`);
      }
    }
    expect(
      offenders,
      'A cell lays its children out in a ROW. Wrap the lines in one <div> — ' +
        'docs/CONTRIBUTING.md → "UI rules" → "One table".',
    ).toEqual([]);
  });

  it('…and that scan sees a broken cell (a detector that finds nothing proves nothing)', () => {
    const stacks = (src: string) =>
      cellSlots(src).some((s) => {
        const roots = branches(rootNodes(s.body));
        return roots.length > 1 && roots.some((r) => STACKS.test(r));
      });
    expect(
      stacks(`<template #cell-label="{ row }">
          <span class="font-medium">{{ row.label }}</span>
          <Badge class="ms-1">pack</Badge>
          <AppPluginLanguages class="mt-1" :languages="row.languages" />
        </template>`),
      'the overlap this rule exists for',
    ).toBe(true);
    expect(
      stacks(`<template #cell-label="{ row }">
          <div>
            <span class="font-medium">{{ row.label }}</span>
            <AppPluginLanguages class="mt-1" :languages="row.languages" />
          </div>
        </template>`),
      'one wrapper is the fix',
    ).toBe(false);
    expect(
      stacks(`<template #cell-state="{ row }">
          <Badge v-if="row.ok">ok</Badge>
          <span v-else class="mt-1">—</span>
        </template>`),
      'v-if / v-else are never drawn together',
    ).toBe(false);
  });

  it('a Badge shares its cell with nothing — it is wrapped with whatever it sits beside', () => {
    /* ⚠⚠ WHY THE RULE ABOVE MISSED ONE. It reads a second line off a CLASS —
       a `mt-`/`mb-` on a second root — because that is how the Apps list's
       Label cell announced its. The admin Notifications page's Webhook cell
       announced nothing: a `<Badge>` ("Not sent") and a `<span>` with the
       reason, two roots and no margin. Its second line came from the reason
       WRAPPING, which no class says, and the scan saw two roots, no margin,
       and passed (v0.43.0 pack agent, es/ar and a narrow English column).

       The mechanism is not the margin. Every root of a cell is a flex item
       that may shrink below its content (`:where(.fe-list__cell) > *
       { min-width: 0 }`, so one long value cannot push a cell past its
       track). Beside text that wraps, a pill is squeezed narrower than its
       own label, and the label spills under the text next to it — the
       overlap, with no margin anywhere. So a Badge is never a co-drawn root:
       inside ONE wrapper it is an inline box and the text flows after it
       and under it.

       Two short Badges side by side are the same trap at a narrow width, and
       the same one-line fix. `v-if` / `v-else` alternatives are one root. */
    const offenders: string[] = [];
    for (const x of files) {
      for (const slot of cellSlots(code(x.f))) {
        const roots = branches(rootNodes(slot.body));
        if (roots.length < 2) continue;
        const pills = roots.filter((r) => PILL.test(r));
        if (pills.length) offenders.push(`${x.tree}/${x.r} #cell-${slot.name}: ${roots.join(' + ')}`);
      }
    }
    expect(
      offenders,
      'A Badge beside another root in a cell is squeezed below its label and overlaps its ' +
        'neighbour. Wrap the cell’s content in ONE element — docs/CONTRIBUTING.md → "UI rules" → "One table".',
    ).toEqual([]);
  });

  it('…and that scan sees the Webhook cell as it was (a detector that finds nothing proves nothing)', () => {
    const pillBeside = (src: string) =>
      cellSlots(src).some((s) => {
        const roots = branches(rootNodes(s.body));
        return roots.length > 1 && roots.some((r) => PILL.test(r));
      });
    expect(
      pillBeside(`<template #cell-webhook="{ row }">
          <Badge :tone="row.webhook_status === 'sent' ? 'emerald' : 'zinc'">{{ webhookLabel(row.webhook_status) }}</Badge>
          <span v-if="webhookReason(row)" class="text-zinc-500">{{ ' — ' + webhookReason(row) }}</span>
        </template>`),
      'the overlap on the admin Notifications page — a badge and a reason, no margin anywhere',
    ).toBe(true);
    expect(
      pillBeside(`<template #cell-webhook="{ row }">
          <div>
            <Badge tone="zinc">{{ webhookLabel(row.webhook_status) }}</Badge>
            <span class="text-zinc-500">{{ ' — ' + webhookReason(row) }}</span>
          </div>
        </template>`),
      'one wrapper is the fix',
    ).toBe(false);
    expect(
      pillBeside(`<template #cell-flags="{ row }">
          <Badge v-if="row.migrations" variant="warning">migrations</Badge>
          <Badge v-if="row.security" variant="danger">security</Badge>
        </template>`),
      'two pills side by side are the same squeeze',
    ).toBe(true);
    expect(
      pillBeside(`<template #cell-state="{ row }">
          <Badge v-if="row.ok" tone="emerald">ok</Badge>
          <span v-else class="text-zinc-500">—</span>
        </template>`),
      'v-if / v-else are never drawn together',
    ).toBe(false);
    expect(
      pillBeside(`<template #cell-severity="{ row }">
          <Badge :tone="severityTone(row.severity)">{{ severityLabel(row.severity) }}</Badge>
        </template>`),
      'a Badge alone in its cell is fine',
    ).toBe(false);
  });

  it('a row ends in ONE control: RowActions is drawn by DataTable, nowhere else', () => {
    /* The owner, 2026-09-20: "adminde aksiyonlar karma karışık; … en dibe
       sabitli tek buton olmalı". Pages hand the table their verbs
       (`:row-actions`), and the table draws the one control. A page that
       draws its own RowActions — or loose buttons in an actions column — is
       where the scatter came from. */
    const offenders = files
      .filter((x) => !(x.tree === 'packages/core/src' && x.r === TABLE_FILE))
      .filter((x) => /<RowActions\b/.test(code(x.f)) || /#cell-actions\b/.test(code(x.f)))
      .map((x) => `${x.tree}/${x.r}`);
    expect(offenders, 'give the table `:row-actions` instead').toEqual([]);
  });
});

describe('the table’s stylesheet', () => {
  const CSS = readFileSync(path.join(CORE_SRC, 'styles/base.css'), 'utf8');

  it('the frozen edges are the table’s own rules, and the left one is GATED', () => {
    // A sticky lead wider than its pane covers the pane, so it pins only
    // while `is-pin-lead` says there is room (DataTable computes it).
    expect(CSS).toMatch(/\.fe-list\.is-pin-lead \.fe-list__col--lead\s*\{/);
    expect(CSS).toMatch(/\.fe-list__head \.fe-list__col--menu,\s*\n\.fe-list__row \.fe-list__col--menu\s*\{/);
    expect(CSS).toContain('.fe-table--framed');
  });

  it('the imitation’s table rules are gone', () => {
    const body = CSS.replace(/\/\*[\s\S]*?\*\//g, '');
    for (const dead of ['.tbl {', '.tbl thead', '.tbl tbody', '.tbl-scroll', '.tbl-lead', '.tbl-actions', '.tbl-state']) {
      expect(body, `${dead} is back in base.css`).not.toContain(dead);
    }
  });

  it('the one table’s additions carry no colour a palette cannot move', () => {
    const start = CSS.indexOf('THE ONE TABLE — what DataTable adds');
    const end = CSS.indexOf('The date headings travel', start);
    expect(start, 'the DataTable block moved — fix this slice').toBeGreaterThan(0);
    const block = CSS.slice(start, end).replace(/\/\*[\s\S]*?\*\//g, '');
    expect(block.length).toBeGreaterThan(1500);
    expect(block, 'a raw hex colour').not.toMatch(/#[0-9a-f]{3,8}\b/i);
    expect(block, 'a Tailwind colour').not.toMatch(/\b(zinc|slate|gray)-\d{2,3}\b/);
  });
});

describe('the admin views keep to the palette', () => {
  const files = vueFiles(SRC);
  it('no admin view hard-codes a `divide-zinc` row list where the palette should decide', () => {
    /* "admin panel seçili renk paletinden etkilenmiyor, etkilenmeli": seven
       views drew rows with `divide-zinc-200 dark:divide-zinc-800`, two hexes
       no palette can reach. */
    const offenders = files
      .filter((f) => /\bdivide-(zinc|slate|gray)-\d{2,3}\b/.test(code(f)))
      .map((f) => rel(SRC, f));
    expect(offenders).toEqual([]);
  });
});
