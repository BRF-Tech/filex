// What a `text` field on a PDF accepts, and the face it is drawn in.
//
// ⚠⚠ The shaping runs on EVERY keystroke, so the tests are about what the box
// holds after one — not about a verdict at submit time. An invalid signature
// page that only says so on the way out is a page that gets signed twice.
import { describe, expect, it } from 'vitest';

import {
  DEFAULT_SIGN_FONT,
  PDF_RULE_KINDS,
  SIGN_FONTS,
  applyRule,
  fillValues,
  isSignFontKey,
  normalizeFields,
  normalizeRule,
  ruleError,
  ruleInputMode,
  ruleMaxLength,
  signFont,
  withFont,
} from '@brftech/filex-core';

describe('a rule off the wire', () => {
  it('keeps only what it can draw', () => {
    expect(normalizeRule({ kind: 'number' })).toEqual({ kind: 'number' });
    expect(normalizeRule({ kind: 'nonsense', min: 3 })).toEqual({ kind: 'any', min: 3 });
    expect(normalizeRule({ kind: 'any', min: '4', max: 8 })).toEqual({ kind: 'any', min: 4, max: 8 });
    // ⚠ "anything, with no bounds" is not a rule: carrying it back to the
    // plugin would be noise that looks like a constraint.
    expect(normalizeRule({ kind: 'any' })).toBeNull();
    expect(normalizeRule({ kind: 'any', min: 0, max: -2 })).toBeNull();
    expect(normalizeRule(null)).toBeNull();
  });
});

describe('typing under a rule', () => {
  it('numbers only, with one sign and one separator', () => {
    const r = { kind: 'number' as const };
    expect(applyRule('12a3', r)).toBe('123');
    expect(applyRule('-12.5', r)).toBe('-12.5');
    // A Turkish keyboard's comma is kept as typed rather than rewritten.
    expect(applyRule('12,5', r)).toBe('12,5');
    expect(applyRule('1.2.3', r)).toBe('1.23');
  });

  // ⚠⚠ v3 §3.3 — a text field has no `date` rule any more. `date` is a
  // FIELD TYPE, and two ways to ask for a date is how you get two answers:
  // the field type sent `YYYY-MM-DD` and the text rule sent `DD/MM/YYYY`, so
  // the stamping plugin had to guess which of its own boxes it was reading.
  it('there is no date rule: an old manifest degrades to a plain text box', () => {
    expect(PDF_RULE_KINDS).toEqual(['any', 'number', 'email']);
    // An older plugin's `kind: "date"` is READ (not refused) as "anything",
    // so the box keeps taking what the person types.
    expect(normalizeRule({ kind: 'date' })).toBeNull();
    expect(normalizeRule({ kind: 'date', max: 10 })).toEqual({ kind: 'any', max: 10 });
    const degraded = normalizeRule({ kind: 'date', max: 10 })!;
    expect(applyRule('01/02/2026', degraded)).toBe('01/02/2026');
    expect(ruleError('31/02/2026', degraded)).toBe('');
    expect(ruleInputMode(degraded)).toBe('text');
  });

  it('an e-mail loses its spaces, and a length ceiling truncates', () => {
    expect(applyRule(' a b@c.d ', { kind: 'email' })).toBe('ab@c.d');
    expect(applyRule('abcdef', { kind: 'any', max: 3 })).toBe('abc');
  });

  it('no rule shapes nothing', () => {
    expect(applyRule('as typed', null)).toBe('as typed');
    expect(ruleMaxLength(null)).toBeUndefined();
    expect(ruleInputMode(null)).toBe('text');
    expect(ruleInputMode({ kind: 'number' })).toBe('numeric');
    expect(ruleInputMode({ kind: 'email' })).toBe('email');
  });
});

describe('the verdict', () => {
  it('an empty box is never WRONG — that is what `required` is for', () => {
    expect(ruleError('', { kind: 'number' })).toBe('');
    expect(ruleError('  ', { kind: 'email', min: 5 })).toBe('');
    expect(ruleError(undefined, { kind: 'any', min: 2 })).toBe('');
  });

  it('names what is wrong', () => {
    expect(ruleError('abc', { kind: 'number' })).toBe('number');
    expect(ruleError('12,5', { kind: 'number' })).toBe('');
    expect(ruleError('nope', { kind: 'email' })).toBe('email');
    expect(ruleError('a@b.co', { kind: 'email' })).toBe('');
    expect(ruleError('ab', { kind: 'any', min: 3 })).toBe('min');
    expect(ruleError('abcd', { kind: 'any', max: 3 })).toBe('max');
  });

});

describe('the five faces', () => {
  it('are a closed set, handwriting first', () => {
    expect(SIGN_FONTS.map((f) => f.key)).toEqual([
      'caveat',
      'dancing-script',
      'homemade-apple',
      'inter',
      'source-serif',
    ]);
    expect(SIGN_FONTS.filter((f) => f.kind === 'hand')).toHaveLength(3);
    expect(isSignFontKey('caveat')).toBe(true);
    expect(isSignFontKey('comic-sans')).toBe(false);
  });

  it('an unknown key resolves to the default rather than to nothing', () => {
    expect(signFont('comic-sans').key).toBe(DEFAULT_SIGN_FONT);
    expect(signFont(undefined).key).toBe(DEFAULT_SIGN_FONT);
    // ⚠ A canvas cannot read a CSS custom property, so every face carries a
    // literal stack as well as its class.
    for (const f of SIGN_FONTS) {
      expect(f.stack, `${f.key} has a canvas stack`).not.toBe('');
      expect(f.stack).not.toContain('var(');
    }
  });
});

describe('what a filled field sends back', () => {
  it('carries the face and the rule with the value', () => {
    // ⚠ The plugin stamps the PDF from this array alone and never sees the
    // screen: a value that arrives as a bare string is stamped in whatever
    // the plugin defaults to, which is how a signed document comes back in a
    // face the signer never chose.
    const fields = normalizeFields([
      { id: 'text-1', type: 'text', page: 1, x: 0.1, y: 0.1, w: 0.2, h: 0.03, label: 'Vergi no', rule: { kind: 'number' }, font: 'source-serif' },
      { id: 'text-2', type: 'text', page: 1, x: 0.1, y: 0.2, w: 0.2, h: 0.03 },
      { id: 'chk-1', type: 'checkbox', page: 1, x: 0.5, y: 0.5, w: 0.03, h: 0.02 },
    ]);
    expect(fields[0].rule).toEqual({ kind: 'number' });
    expect(fields[0].font).toBe('source-serif');
    // v3 §3.1 — the field's NAME survives the wire too, so the fill form and
    // the audit trail can call it what the placer called it.
    expect(fields[0].label).toBe('Vergi no');

    const filled = fields.map((f) =>
      f.id === 'text-1' ? { ...f, value: '4830987654' } : f.id === 'text-2' ? { ...f, value: 'plain' } : f,
    );
    expect(fillValues(filled, undefined)).toEqual({
      fields: [
        { id: 'text-1', value: '4830987654', label: 'Vergi no', font: 'source-serif', rule: { kind: 'number' } },
        { id: 'text-2', value: 'plain' },
      ],
    });
  });

  it('a face the wire made up is dropped rather than passed on', () => {
    const [f] = normalizeFields([
      { id: 'text-1', type: 'text', page: 1, x: 0, y: 0, w: 0.2, h: 0.03, font: 'comic-sans' },
    ]);
    expect(f.font).toBeUndefined();
    expect(withFont([f], 'text-1', 'caveat')[0].font).toBe('caveat');
  });
});
