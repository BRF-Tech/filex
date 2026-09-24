// `show_when` / `required_when` — a form may not present a contradiction.
//
// ⚠⚠ The rule the v3 contract states (§2.1) is not "a field can be hidden";
// it is that a hidden field's VALUE never reaches the plugin. "Signed
// document goes: a new version of this file" sitting above a file-name box
// asking for a name it will not use is the screen this exists to make
// impossible — and the name must not arrive in the job either, or the plugin
// has to decide which of two contradictory answers it was given.
import { describe, expect, it } from 'vitest';

import {
  conditionMet,
  fieldRequired,
  hasAnswer,
  hiddenKeys,
  stripHiddenValues,
  surfaceMissingRequired,
  visibleFields,
} from '@brftech/filex-core';
import type { PluginField, SurfaceNode } from '@brftech/filex-core/src/types/Plugins';

const fields: PluginField[] = [
  { key: 'output', type: 'select', options: [{ value: 'version' }, { value: 'sibling' }] },
  { key: 'name', type: 'string', show_when: { key: 'output', equals: ['sibling'] }, required_when: { key: 'output', equals: ['sibling'] } },
  // A chain: this one depends on a field that can itself disappear.
  { key: 'suffix', type: 'string', show_when: { key: 'name', equals: ['report'] } },
  { key: 'notify', type: 'bool' },
];

const nodes: SurfaceNode[] = [{ id: 'f', type: 'form', props: { fields } }];

describe('a condition', () => {
  it('holds when the other field carries one of the values', () => {
    expect(conditionMet({ key: 'output', equals: ['sibling'] }, { output: 'sibling' })).toBe(true);
    expect(conditionMet({ key: 'output', equals: ['sibling'] }, { output: 'version' })).toBe(false);
    // A multi-select holds several: "one of these" means any of them.
    expect(conditionMet({ key: 'kinds', equals: ['pdf'] }, { kinds: ['docx', 'pdf'] })).toBe(true);
    // A boolean compares as its own word, so `equals: ["true"]` works.
    expect(conditionMet({ key: 'on', equals: ['true'] }, { on: true })).toBe(true);
  });

  it('with no values at all means "that field has been answered"', () => {
    expect(conditionMet({ key: 'name' }, { name: 'x' })).toBe(true);
    expect(conditionMet({ key: 'name' }, { name: '  ' })).toBe(false);
    expect(conditionMet({ key: 'name' }, {})).toBe(false);
  });

  it('is absent → it holds; that is what every field without one means', () => {
    expect(conditionMet(undefined, {})).toBe(true);
    expect(conditionMet(null, {})).toBe(true);
  });

  it('counts `false` and `0` as answers', () => {
    // ⚠ A person who answered "no" HAS answered. Treating falsiness as
    // absence is how a required toggle can never be satisfied with "off".
    expect(hasAnswer(false)).toBe(true);
    expect(hasAnswer(0)).toBe(true);
    expect(hasAnswer('')).toBe(false);
    expect(hasAnswer([])).toBe(false);
    expect(hasAnswer(undefined)).toBe(false);
  });
});

describe('which fields exist right now', () => {
  it('hides the dependent field until its condition holds', () => {
    expect(visibleFields(fields, { output: 'version' }).map((f) => f.key)).toEqual(['output', 'notify']);
    expect(visibleFields(fields, { output: 'sibling' }).map((f) => f.key)).toEqual(['output', 'name', 'notify']);
  });

  it('CASCADES: a field shown by a hidden field is hidden too', () => {
    // ⚠ `suffix` depends on `name`, and `name` is not on the step while the
    // output is a version. Resolving in one pass would leave `suffix` on
    // screen asking about an answer nobody can see or give.
    const shown = visibleFields(fields, { output: 'version', name: 'report' });
    expect(shown.map((f) => f.key)).toEqual(['output', 'notify']);
    expect(visibleFields(fields, { output: 'sibling', name: 'report' }).map((f) => f.key)).toEqual([
      'output',
      'name',
      'suffix',
      'notify',
    ]);
  });

  it('makes `required_when` a real requirement, only while it holds', () => {
    const name = fields[1];
    expect(fieldRequired(name, { output: 'version' })).toBe(false);
    expect(fieldRequired(name, { output: 'sibling' })).toBe(true);
    // A plain `required` is unconditional.
    expect(fieldRequired({ key: 'x', required: true }, {})).toBe(true);
  });
});

describe('what goes on the wire', () => {
  it('DROPS a hidden field’s value — it is not blanked, it is not there', () => {
    const values = { output: 'version', name: 'invoice.pdf', notify: true };
    expect(hiddenKeys(nodes, values)).toEqual(['name', 'suffix']);
    const sent = stripHiddenValues(nodes, values);
    expect(sent).toEqual({ output: 'version', notify: true });
    // ⚠ `{name: ""}` would be an ANSWER ("call it nothing"), which is exactly
    // the surprise the contract says must not reach the job.
    expect('name' in sent).toBe(false);
  });

  it('keeps everything while the condition holds', () => {
    const values = { output: 'sibling', name: 'invoice.pdf' };
    expect(stripHiddenValues(nodes, values)).toEqual(values);
  });

  it('blocks the submit on a visible, required, empty field — and only then', () => {
    expect(surfaceMissingRequired(nodes, { output: 'version', name: '' })).toEqual([]);
    expect(surfaceMissingRequired(nodes, { output: 'sibling', name: '' }).map((f) => f.key)).toEqual(['name']);
    expect(surfaceMissingRequired(nodes, { output: 'sibling', name: 'x' })).toEqual([]);
  });
});
