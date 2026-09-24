// THE table in a pane too narrow for it — the arithmetic half.
//
// ⚠⚠ The owner's rule is that a table never sheds a column for want of room:
// "no room demesin, kenara devam eden bir scroll getirsin" — it stays whole
// and scrolls sideways. This file is about what the person is shown FIRST,
// which is the half that rule does not decide.
//
// v0.43.0: an app's list inside the details panel (265px) drew its Identity
// column and nothing else. The lead stopped shrinking at `leadAuto`, a
// DESKTOP default of 240px, so the State and When columns the app had sent
// were pushed out of the pane and under the pinned Actions cell — a signer
// table that read as names, buttons, and a blank gap between them, while the
// documentation beside it promised "Waiting / Invited / Opened it / Signed /
// Refused" on every row.
//
// The painted half of the same fix — that those cells are VISIBLE and not
// under a frozen cell — is measured in a real browser:
// e2e/tests/117-narrow-table-columns.spec.ts and e2e/shots/signing.mjs.
import { describe, expect, it } from 'vitest';

import {
  createColumnStore,
  memoryBacking,
  type LayoutMetrics,
  type TableColumnSpec,
} from '@brftech/filex-core/src/lib/tableColumns';

/** An app's signer list: one lead that names the row, two columns of facts. */
const specs: TableColumnSpec[] = [
  { id: 'who', width: 240, min: 120, max: 900, hideable: false, resizable: true },
  { id: 'state', width: 140, min: 56, max: 900, hideable: true, resizable: true },
  { id: 'when', width: 140, min: 56, max: 900, hideable: true, resizable: true },
];

/** The metrics DataTable passes for a list with a labelled `Actions` control
 *  and no tick column. */
const metrics: LayoutMetrics = { gap: 8, padding: 24, check: 0, menu: 92 };

function layoutAt(available: number, at: TableColumnSpec[] = specs) {
  const store = createColumnStore(() => at, memoryBacking(), { lead: 'who' });
  return store.layout(
    available,
    at.map((c) => c.id),
    metrics,
  );
}

describe('a table in a pane that cannot hold it', () => {
  it('opens wide the way it always did', () => {
    const wide = layoutAt(1000);
    expect(wide.visible).toEqual(['state', 'when']);
    expect(wide.widths.state).toBe(140);
    expect(wide.widths.when).toBe(140);
    // The slack is the lead's, and only the lead's.
    expect(wide.widths.who).toBeGreaterThan(240);
    expect(wide.total).toBeLessThanOrEqual(1000);
  });

  it('lets the LEAD give way once the others have given everything', () => {
    const panel = layoutAt(265);
    // Nothing is shed: the person's columns are all still drawn.
    expect(panel.visible).toEqual(['state', 'when']);
    // The others are at their minimums…
    expect(panel.widths.state).toBe(56);
    expect(panel.widths.when).toBe(56);
    // …and the lead no longer stands at 240 holding the whole pane.
    expect(panel.widths.who).toBe(120);
    // It is still wider than the pane — it scrolls, as the owner's rule says
    // — but what the person sees first is three columns, not one.
    expect(panel.total).toBeGreaterThan(265);
  });

  it('never goes below the lead’s own minimum', () => {
    // A pane narrower than anything sane: the lead stops at its min rather
    // than becoming a column of three characters.
    expect(layoutAt(120).widths.who).toBe(120);
    expect(layoutAt(40).widths.who).toBe(120);
  });

  it('gives way in proportion, not all at once', () => {
    // Between the two ends the lead takes what is left rather than jumping to
    // its minimum: at 500 there is room for more than 120.
    const mid = layoutAt(500);
    expect(mid.widths.who).toBeGreaterThan(120);
    expect(mid.widths.who).toBeLessThanOrEqual(240);
  });

  it('leaves a table somebody has SIZED alone', () => {
    const store = createColumnStore(() => specs, memoryBacking(), { lead: 'who' });
    store.freeze({ who: 240, state: 140, when: 140 });
    const sized = store.layout(265, ['who', 'state', 'when'], metrics);
    // The pane stops having an opinion the instant one width is stored —
    // otherwise a person who widened Identity would find it narrowed again
    // by the next resize.
    expect(sized.auto).toBe(false);
    expect(sized.widths.who).toBe(240);
  });
});
