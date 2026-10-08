// An expiry in a table cell: the date, and how far away it is UNDER it.
//
// ⚠ 0.52.0 and 0.53.0, signing/admin-table-actions-1440.png: Admin → Shares
// printed "Sep 22, 2026, 10:30 AM · in 7 days" on one line in a column the
// explorer's table gave ~175px, and the cell cut it mid-word with no
// ellipsis - "· in 7 day". The date and the distance are now the cell's own
// two children, stacked by core's `.fe-list__cell:has(> .tbl-sub)`, and the
// date clamps with an ellipsis instead of being cut. And past a week
// formatRelative answers with the date itself, so the one-line form printed
// the same date twice.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { defineComponent, h } from 'vue';

import DateWithDistance from '@/components/DateWithDistance.vue';

const NOW = Date.parse('2026-09-15T10:30:00Z');
const DAY = 86_400_000;

/** The component as a table puts it: straight inside a cell. */
function inCell(at: string, locale = 'en') {
  return mount(
    defineComponent({
      render: () => h('div', { class: 'fe-list__col fe-list__cell', role: 'gridcell' }, [h(DateWithDistance, { at, locale })]),
    }),
  );
}

describe('DateWithDistance', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('puts the distance on its own line under the date, both children of the cell', () => {
    // A minute short of a week: what a link minted for seven days shows a
    // moment later (at exactly a week formatRelative already answers a date).
    const w = inCell(new Date(NOW + 7 * DAY - 60_000).toISOString());
    const kids = [...w.element.children] as HTMLElement[];
    expect(kids.map((k) => k.getAttribute('data-testid'))).toEqual(['date-with-distance', 'date-distance']);
    expect(kids[0].classList.contains('tbl-clamp'), 'the date clamps with an ellipsis rather than being cut').toBe(true);
    expect(kids[1].classList.contains('tbl-sub'), 'the distance is the secondary line core stacks under the value').toBe(true);
    expect(kids[0].textContent).toMatch(/2026/);
    expect(kids[0].getAttribute('title'), 'the whole date is there to read when it is clamped').toBe(kids[0].textContent?.trim());
    expect(kids[1].textContent?.trim()).toBe('in 7 days');
    expect(w.text()).not.toContain('·');
  });

  it('says the date once when the distance would only repeat it', () => {
    const w = inCell(new Date(NOW + 30 * DAY).toISOString());
    expect(w.findAll('[data-testid="date-distance"]')).toHaveLength(0);
    const date = w.get('[data-testid="date-with-distance"]').text();
    expect(w.text().split(date)).toHaveLength(2);
  });

  it('speaks the reader’s language', () => {
    const w = inCell(new Date(NOW + 3 * DAY).toISOString(), 'tr');
    expect(w.get('[data-testid="date-distance"]').text()).toBe('3 gün sonra');
  });
});

describe('the pages that print an expiry use it', () => {
  const VIEWS = path.resolve(__dirname, '../../src/views');
  for (const file of ['Shares.vue', 'MyShares.vue']) {
    it(`${file}: the Expires cell is DateWithDistance, not "date · distance" on one line`, () => {
      const src = readFileSync(path.join(VIEWS, file), 'utf8');
      const cell = /<template #cell-expires_at="\{ row \}">([\s\S]*?)\n {6}<\/template>/.exec(src);
      expect(cell, `${file} has no #cell-expires_at slot`).not.toBeNull();
      expect(cell![1]).toContain('<DateWithDistance');
      expect(cell![1]).not.toContain('formatRelative(');
    });
  }
});
