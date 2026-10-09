// THE table, narrower than its words: a header and a pill cut short.
//
// The 0.55 release shots at 390px (store-screen-storage-tab-390,
// store-screen-storage-tab-dark-tr-390, vault-strip-held-390-dark):
//
//   1. "Mağaza denetimi" drew as "Mağaza d" and "Store checks" as "Store che",
//      cut in the middle of a letter with no ellipsis. The header's sort button
//      is a flex row (label + arrow) and `text-overflow` only cuts the text of
//      a BLOCK box, so the ellipsis base.css asked for never drew. The label
//      now has a box of its own, and the whole name is the header's title.
//   2. "Bekliyor" / "Waiting" ran out through the border of its own pill in
//      the requests table, its dot squeezed to nothing. A pill in a cell now
//      shortens inside its border (`tbl-pill` / `tbl-pill__text`), and the
//      table gives the whole text as its title while it is cut.
//   3. The explorer's "Modified" read "Mo": not a narrow column but one running
//      on under the frozen ⋮ of a table nobody had scrolled yet, against a
//      cell with no edge. The ⋮ now draws its edge while the table continues
//      beneath it (`is-more-end`).
//
// Each check below is red on the code before fix/055-narrow-table. What the
// painted result looks like is measured in a browser (pnpm shots: store
// screen Storage tab and requests, 390px, light/dark, en/tr; the vault strip).
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { h, nextTick } from 'vue';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import { syncCutTitle } from '@brftech/filex-core/src/lib/cutTitle';
import { __resetViewPrefs } from '@brftech/filex-core/src/lib/viewPrefs';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import Badge from '@/components/ui/Badge.vue';

const CSS_FILE = path.resolve(__dirname, '../../../packages/core/src/styles/base.css');

type Row = { id: number; app: string; checks: string; status: string };
const ROWS: Row[] = [
  { id: 1, app: 'iCloud Drive', checks: '67 checks passed', status: 'Bekliyor' },
  { id: 2, app: 'S3 Archive', checks: '61 checks passed', status: 'Onaylandı' },
];

const mounted: VueWrapper[] = [];
function draw(extra: Record<string, unknown> = {}, slots: Record<string, unknown> = {}) {
  const w = mount(DataTable, {
    props: {
      rows: ROWS,
      columns: [
        { id: 'app', label: 'App', sortable: true, width: 240 },
        { id: 'checks', label: 'Store checks', sortable: true, width: 220 },
        { id: 'status', label: 'Status', width: 140 },
      ],
      rowKey: 'id',
      tableId: 'test.narrow-cut',
      locale: 'en',
      ...extra,
    },
    slots: slots as never,
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

beforeEach(() => {
  __resetViewPrefs();
  localStorage.clear();
});
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  __resetViewPrefs();
});

/** Pretend `el`'s content is `content` px wide in a `box` px box. */
function measure(el: Element, content: number, box: number) {
  Object.defineProperty(el, 'scrollWidth', { configurable: true, get: () => content });
  Object.defineProperty(el, 'clientWidth', { configurable: true, get: () => box });
}

/** Every rule whose selector is exactly `selector`, comments stripped, their
 *  bodies joined ('' when there is none). */
function ruleBody(css: string, selector: string): string {
  const bare = css.replace(/\/\*[\s\S]*?\*\//g, '');
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`(?:^|\\})\\s*${esc}\\s*\\{([^}]*)\\}`, 'gm');
  return Array.from(bare.matchAll(re), (m) => m[1]).join('\n');
}

describe('a header narrower than its name', () => {
  it('a sortable header keeps its words in a box of their own, so they can end in an ellipsis', () => {
    const w = draw();
    const btn = w.get('.fe-list__head [data-col="checks"] button.fe-list__sort');
    const label = btn.find('.fe-list__sort-label');
    expect(label.exists(), 'the label is loose text inside a flex button - no ellipsis can cut it').toBe(true);
    expect(label.text()).toBe('Store checks');
    // The lead's header too: it is drawn by a template of its own.
    expect(w.find('.fe-list__head [data-col="app"] button.fe-list__sort .fe-list__sort-label').exists()).toBe(true);
  });

  it('…and its title names the column in full, in the viewer’s language', () => {
    const w = draw();
    const btn = w.get('.fe-list__head [data-col="checks"] button.fe-list__sort');
    expect(btn.attributes('title')).toBe(en['cols.sort_by'].replace('{col}', 'Store checks'));

    const trw = draw({
      tableId: 'test.narrow-cut.tr',
      locale: 'tr',
      columns: [
        { id: 'app', label: 'Eklenti', sortable: true },
        { id: 'checks', label: 'Mağaza denetimi', sortable: true },
      ],
    });
    expect(trw.get('.fe-list__head [data-col="checks"] button.fe-list__sort').attributes('title')).toBe(
      'Mağaza denetimi sütununa göre sırala',
    );
    expect(tr['cols.sort_by']).toBe('{col} sütununa göre sırala');
  });

  it('a header that does not sort carries its whole name as its title', () => {
    const w = draw();
    const label = w.get('.fe-list__head [data-col="status"] .fe-list__head-label');
    expect(label.attributes('title')).toBe('Status');
  });

  it('a CLOSED header still says why, word for word (and its name is still its text)', () => {
    const w = draw({ pages: 3, page: 1 });
    const btn = w.get('.fe-list__head [data-col="checks"] button.fe-list__sort');
    expect(btn.attributes('disabled')).toBeDefined();
    expect(btn.attributes('title')).toBe(en['table.sort_paged']);
    expect(btn.get('.fe-list__sort-label').text()).toBe('Store checks');
  });

  it('the stylesheet cuts that box with an ellipsis, and keeps the arrow whole', () => {
    const css = readFileSync(CSS_FILE, 'utf8');
    const label = ruleBody(css, '.fe-list__sort-label');
    expect(label, 'no rule for the header label box').not.toBe('');
    expect(label).toMatch(/text-overflow:\s*ellipsis/);
    expect(label).toMatch(/overflow:\s*hidden/);
    expect(label).toMatch(/white-space:\s*nowrap/);
    expect(label).toMatch(/min-width:\s*0/);
    expect(ruleBody(css, '.fe-list__sort-arrow')).toMatch(/flex:\s*0 0 auto/);
  });
});

describe('a pill narrower than its words', () => {
  it('the panel’s Badge is a table pill: its words in a box, its dot kept whole', () => {
    const w = mount(Badge, { props: { tone: 'amber', dot: true }, slots: { default: () => 'Bekliyor' } });
    expect(w.classes()).toContain('tbl-pill');
    const text = w.find('.tbl-pill__text');
    expect(text.exists(), 'the words are loose text in a flex pill - no ellipsis can cut them').toBe(true);
    expect(text.text()).toBe('Bekliyor');
    // ⚠ The dot was a flex item with no floor: squeezed to 0px in the shot.
    expect(w.get('.tbl-pill__dot').classes()).toContain('shrink-0');
    w.unmount();
  });

  it('…an icon before the words stays a flex item of its own, and white space alone gets no box', () => {
    const w = mount(Badge, {
      props: { tone: 'zinc' },
      slots: { default: () => [h('svg', { class: 'lock-icon' }), ' Locked'] },
    });
    expect(w.find('.tbl-pill > svg.lock-icon').exists()).toBe(true);
    expect(w.find('.tbl-pill__text svg').exists()).toBe(false);
    expect(w.get('.tbl-pill__text').text()).toBe('Locked');
    w.unmount();

    const gap = mount(Badge, { slots: { default: () => [h('i', { class: 'a' }), ' ', h('i', { class: 'b' })] } });
    expect(gap.findAll('.tbl-pill__text')).toHaveLength(0);
    expect(gap.findAll('i')).toHaveLength(2);
    gap.unmount();
  });

  it('the stylesheet lets a pill in a cell shorten INSIDE its border, the words ending in an ellipsis', () => {
    const css = readFileSync(CSS_FILE, 'utf8');
    const pill = ruleBody(css, ':where(.fe-list__cell) .tbl-pill');
    expect(pill, 'no rule for a pill in a cell').not.toBe('');
    expect(pill).toMatch(/max-width:\s*100%/);
    expect(pill).toMatch(/min-width:\s*0/);
    expect(pill).toMatch(/overflow:\s*hidden/);
    const words = ruleBody(css, ':where(.fe-list__cell) .tbl-pill > .tbl-pill__text');
    expect(words).toMatch(/text-overflow:\s*ellipsis/);
    expect(words).toMatch(/min-width:\s*0/);
    expect(ruleBody(css, ':where(.fe-list__cell) .tbl-pill > *')).toMatch(/flex-shrink:\s*0/);
    // ⚠ Not the other answer: a cell that grows to its pill leaves its header.
    expect(pill).not.toMatch(/max-content/);
  });

  it('a pill cut short in a table gives its whole text as its title - and takes it back once it fits', async () => {
    const w = draw({}, {
      'cell-status': ({ row }: { row: Row }) => h(Badge, { tone: 'amber', dot: true }, () => row.status),
    });
    const pill = w.findAll('.fe-list__body .tbl-pill')[0].element;
    const words = pill.querySelector('.tbl-pill__text')!;
    expect(pill.getAttribute('title')).toBeNull();

    measure(words, 61, 30);
    words.dispatchEvent(new Event('pointerover', { bubbles: true }));
    await nextTick();
    expect(pill.getAttribute('title')).toBe('Bekliyor');

    // The person widened the column: the words fit, the borrowed title goes.
    measure(words, 61, 61);
    words.dispatchEvent(new Event('pointerover', { bubbles: true }));
    await nextTick();
    expect(pill.getAttribute('title')).toBeNull();
  });

  it('…and never touches a title the page gave the pill itself', () => {
    const root = document.createElement('div');
    root.innerHTML =
      '<span class="tbl-pill" title="Locked by e-Signature"><span class="tbl-pill__text">Locked</span></span>';
    document.body.appendChild(root);
    const words = root.querySelector('.tbl-pill__text')!;
    measure(words, 80, 20);
    syncCutTitle(words, root);
    expect(root.querySelector('.tbl-pill')!.getAttribute('title')).toBe('Locked by e-Signature');
    root.remove();
  });
});

describe('a column that runs on under the frozen ⋮', () => {
  it('draws the ⋮’s edge before anything is scrolled (`is-more-end`), and drops it at the end', async () => {
    const w = draw();
    const list = w.get('.fe-list').element as HTMLElement;
    let left = 0;
    measure(list, 900, 390);
    Object.defineProperty(list, 'scrollLeft', { configurable: true, get: () => left });

    list.dispatchEvent(new Event('scroll'));
    await nextTick();
    expect(w.get('.fe-list').classes()).toContain('is-more-end');

    left = 510;
    list.dispatchEvent(new Event('scroll'));
    await nextTick();
    expect(w.get('.fe-list').classes()).not.toContain('is-more-end');
  });

  it('the stylesheet draws that edge on the ⋮', () => {
    const css = readFileSync(CSS_FILE, 'utf8');
    expect(ruleBody(css, '.fe-list.is-pin-menu.is-more-end .fe-list__col--menu')).toMatch(/box-shadow:/);
  });
});
