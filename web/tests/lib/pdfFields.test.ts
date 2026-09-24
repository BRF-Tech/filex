// pdfFields — the rules of the `pdf-fields` node without a canvas: what a
// field is on the wire, whose it is in fill mode, what goes back in each
// mode, and how the editor names, places and copies boxes.
import { describe, expect, it } from 'vitest';

import {
  allowedTypes,
  applyFillValues,
  copyToPage,
  defineField,
  fillValues,
  isFieldOf,
  isPlaced,
  missingRequired,
  newFieldId,
  normalizeFields,
  normalizeFormats,
  normalizeSigners,
  placeField,
  signerColor,
  todayIso,
  unplacedFields,
  withValue,
  SIGNER_PALETTE,
  type PdfField,
} from '@brftech/filex-core/src/lib/pdfFields';

const fields: PdfField[] = [
  { id: 'sig-1', type: 'signature', page: 1, x: 0.1, y: 0.8, w: 0.3, h: 0.06, assignee: 'a', required: true },
  { id: 'sig-2', type: 'signature', page: 1, x: 0.5, y: 0.8, w: 0.3, h: 0.06, assignee: 'b' },
  { id: 'chk-1', type: 'checkbox', page: 1, x: 0.1, y: 0.2, w: 0.03, h: 0.02, assignee: 'a' },
  { id: 'text-1', type: 'text', page: 2, x: 0.1, y: 0.3, w: 0.3, h: 0.03 },
];

describe('pdfFields', () => {
  it('normalises the wire array: drops junk, keeps one of each id, clamps the box', () => {
    const out = normalizeFields([
      { id: 'a', type: 'signature', page: 0, x: 0.95, y: 0.5, w: 0.2, h: 0.05, assignee: 'x', required: true, value: 'QUJD' },
      { id: 'a', type: 'text', page: 1, x: 0, y: 0, w: 0.1, h: 0.1 },
      { id: 'b', type: 'stamp', page: 1, x: 0, y: 0, w: 0.1, h: 0.1 },
      null,
      { type: 'text', page: 1 },
    ]);
    expect(out).toEqual([{ id: 'a', type: 'signature', page: 1, x: 0.8, y: 0.5, w: 0.2, h: 0.05, assignee: 'x', required: true, value: 'QUJD' }]);
    expect(normalizeFields(undefined)).toEqual([]);
  });

  it('the palette is the five types, narrowed by `types` in the catalogue order', () => {
    expect(allowedTypes(undefined)).toEqual(['signature', 'initials', 'date', 'text', 'checkbox']);
    expect(allowedTypes(['text', 'signature', 'nope'])).toEqual(['signature', 'text']);
    expect(allowedTypes(['nope'])).toEqual(['signature', 'initials', 'date', 'text', 'checkbox']);
  });

  it('fill mode: a signer may touch their own fields and the unassigned ones', () => {
    expect(isFieldOf({ assignee: 'a' }, 'a')).toBe(true);
    expect(isFieldOf({ assignee: 'b' }, 'a')).toBe(false);
    expect(isFieldOf({}, 'a')).toBe(true);
    expect(isFieldOf({ assignee: 'a' }, undefined)).toBe(false);
  });

  it('fill mode sends only the signer’s own filled fields as {fields: [{id, value}]}', () => {
    const filled = withValue(withValue(withValue(fields, 'chk-1', true), 'sig-2', 'QUJD'), 'text-1', 'hello');
    expect(fillValues(filled, 'a')).toEqual({ fields: [{ id: 'chk-1', value: true }, { id: 'text-1', value: 'hello' }] });
    expect(fillValues(filled, 'b')).toEqual({ fields: [{ id: 'sig-2', value: 'QUJD' }, { id: 'text-1', value: 'hello' }] });
    // An unticked checkbox and an emptied text are "no value", not a value.
    const cleared = withValue(withValue(filled, 'chk-1', false), 'text-1', '');
    expect(fillValues(cleared, 'a')).toEqual({ fields: [] });
  });

  it('a previous fill answer is laid back over the fields', () => {
    const out = applyFillValues(fields, { fields: [{ id: 'sig-1', value: 'QUJD' }, { id: 'ghost', value: 1 }] });
    expect(out.find((f) => f.id === 'sig-1')?.value).toBe('QUJD');
    expect(out.find((f) => f.id === 'sig-2')?.value).toBeUndefined();
    expect(applyFillValues(fields, null)).toBe(fields);
  });

  it('required fields of the signer that are still empty', () => {
    expect(missingRequired(fields, 'a').map((f) => f.id)).toEqual(['sig-1']);
    expect(missingRequired(withValue(fields, 'sig-1', 'QUJD'), 'a')).toEqual([]);
    expect(missingRequired(fields, 'b')).toEqual([]);
  });

  it('signers: a colour of their own, else by position in the palette', () => {
    const signers = normalizeSigners([{ id: 'a', label: { en: 'Ann' } }, { id: 'b', label: 'Bob', color: '#123456' }, { bad: true }]);
    expect(signers.map((s) => s.id)).toEqual(['a', 'b']);
    expect(signerColor(signers, 'a')).toBe(SIGNER_PALETTE[0]);
    expect(signerColor(signers, 'b')).toBe('#123456');
    expect(signerColor(signers, undefined)).toBe(SIGNER_PALETTE[0]);
  });

  it('the editor names new fields after their type and never reuses an id', () => {
    expect(newFieldId('signature', fields)).toBe('sig-3');
    expect(newFieldId('date', fields)).toBe('date-1');
    const placed = placeField(fields, 'date', 2, { x: 0.95, y: 0.2 }, undefined, 'a');
    expect(placed.id).toBe('date-1');
    expect(placed.page).toBe(2);
    expect(placed.assignee).toBe('a');
    // Kept inside the page.
    expect(placed.x + placed.w).toBeLessThanOrEqual(1);
  });

  it('copy to page duplicates the box without its value', () => {
    const src = withValue(fields, 'sig-1', 'QUJD');
    const out = copyToPage(src, 'sig-1', 3);
    expect(out).toHaveLength(5);
    const copy = out[4];
    expect(copy).toMatchObject({ id: 'sig-3', type: 'signature', page: 3, x: 0.1, y: 0.8, assignee: 'a', required: true });
    expect(copy.value).toBeUndefined();
    expect(copyToPage(fields, 'nope', 2)).toBe(fields);
  });

  it('today is the local calendar day as YYYY-MM-DD', () => {
    expect(todayIso(new Date(2026, 8, 19, 23, 59))).toBe('2026-09-19');
    expect(todayIso(new Date(2026, 0, 5))).toBe('2026-01-05');
  });
});

// ⚠⚠ A box is DEFINED before it is PLACED (v3 §3.3). Naming one and
// finding a place for it are two jobs, and the wizard asks them one at a
// time; `placed: false` is what carries "this one is still waiting".
describe('defined, then placed', () => {
  it('a box without a place says so, and a box written before this does not have to', () => {
    const waiting = defineField([], 'signature', { label: 'İmza', assignee: 's1', required: true });
    expect(isPlaced(waiting)).toBe(false);
    expect(waiting.label).toBe('İmza');
    expect(waiting.assignee).toBe('s1');
    expect(waiting.required).toBe(true);
    // It already knows how big it wants to be, so placing is one tap.
    expect(waiting.w).toBeGreaterThan(0);
    expect(waiting.h).toBeGreaterThan(0);

    expect(isPlaced(fields[0])).toBe(true);
    expect(unplacedFields([...fields, waiting]).map((f) => f.id)).toEqual([waiting.id]);
  });

  it('the flag survives the wire, and only when it is false', () => {
    const [a, b] = normalizeFields([
      { id: 'a', type: 'signature', page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05, placed: false },
      { id: 'b', type: 'signature', page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.05 },
    ]);
    expect(a.placed).toBe(false);
    expect(b.placed).toBeUndefined();
    expect(isPlaced(b)).toBe(true);
  });

  // ⚠⚠ A date box's layout has to survive the round trip. `PdfField` had no
  // `format` at all, so every trip through the node stripped it and the box
  // came back to the plugin as "no layout" — a form that asked for
  // `12/31/2000` quietly started printing `31.12.2000`.
  it('a date box keeps its `format` through the wire', () => {
    const [d] = normalizeFields([
      { id: 'date-1', type: 'date', page: 1, x: 0.1, y: 0.1, w: 0.16, h: 0.035, format: 'MM/DD/YYYY' },
    ]);
    expect(d.format).toBe('MM/DD/YYYY');
  });

  it('`format` survives being read back out of what normalizeFields produced', () => {
    const once = normalizeFields([
      { id: 'date-1', type: 'date', page: 1, x: 0.1, y: 0.1, w: 0.16, h: 0.035, format: 'YYYY-MM-DD' },
    ]);
    expect(normalizeFields(once)).toEqual(once);
    expect(normalizeFields(once)[0].format).toBe('YYYY-MM-DD');
  });

  it('a layout that is not a word is no layout, and blanks do not count', () => {
    const [a, b, c] = normalizeFields([
      { id: 'date-1', type: 'date', page: 1, x: 0.1, y: 0.1, w: 0.16, h: 0.035, format: 7 },
      { id: 'date-2', type: 'date', page: 1, x: 0.1, y: 0.1, w: 0.16, h: 0.035, format: '   ' },
      { id: 'date-3', type: 'date', page: 1, x: 0.1, y: 0.1, w: 0.16, h: 0.035, format: '  DD.MM.YYYY ' },
    ]);
    expect(a.format).toBeUndefined();
    expect(b.format).toBeUndefined();
    expect(c.format).toBe('DD.MM.YYYY');
  });

  it('the layout catalogue keeps its examples and drops what cannot be drawn', () => {
    expect(
      normalizeFormats([
        { id: 'DD.MM.YYYY', label: { en: 'DD.MM.YYYY', tr: 'GG.AA.YYYY' }, example: '31.12.2000' },
        { id: 'DD.MM.YYYY', label: 'a duplicate' },
        { id: 'YYYY-MM-DD' },
        { label: 'no id' },
        null,
      ]),
    ).toEqual([
      { id: 'DD.MM.YYYY', label: { en: 'DD.MM.YYYY', tr: 'GG.AA.YYYY' }, example: '31.12.2000' },
      { id: 'YYYY-MM-DD', label: 'YYYY-MM-DD' },
    ]);
    expect(normalizeFormats(undefined)).toEqual([]);
  });

  it('every defined box gets an id of its own', () => {
    let list: PdfField[] = [];
    for (let i = 0; i < 3; i++) list = [...list, defineField(list, 'text')];
    expect(new Set(list.map((f) => f.id)).size).toBe(3);
  });
});
