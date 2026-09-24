// "One step asks one thing" (v3 §2, rule 3) — one primary button plus Back.
//
// ⚠ Enforced by the RENDERER, not by each plugin: a wizard step with four
// buttons and no indication of which one is the way forward is the complaint
// this rule comes from. A plugin that marks three actions `primary` gets one
// primary and two ordinary buttons — none of them is hidden, because silently
// dropping a button would break a wizard in a way no test of ours would see.
import { describe, expect, it } from 'vitest';

import { hasSteps, isBackAction, stepFooter } from '@brftech/filex-core';
import type { SurfaceAction, SurfaceNode } from '@brftech/filex-core/src/types/Plugins';

const actions: SurfaceAction[] = [
  { id: 'back', label: { en: 'Back' } },
  { id: 'save_draft', label: { en: 'Save draft' } },
  { id: 'send', label: { en: 'Send' }, primary: true },
  { id: 'send_now', label: { en: 'Send now' }, primary: true },
];

describe('is this a step of a walk?', () => {
  it('asks the nodes, including nested rows', () => {
    expect(hasSteps([{ type: 'text' }])).toBe(false);
    expect(hasSteps([{ id: 's', type: 'steps', props: { items: [] } }])).toBe(true);
    const nested: SurfaceNode[] = [{ type: 'row', children: [{ id: 's', type: 'steps', props: { items: [] } }] }];
    expect(hasSteps(nested)).toBe(true);
  });
});

describe('which button is Back', () => {
  it('reads the action id, ignoring case and separators', () => {
    for (const id of ['back', 'Back', 'prev', 'previous', 'step_back', 'go-back']) {
      expect(isBackAction({ id }), id).toBe(true);
    }
    expect(isBackAction({ id: 'cancel' })).toBe(false);
    // ⚠ A step whose backward action is called something else simply gets an
    // ordinary button — which is what it would have had anyway.
    expect(isBackAction({ id: 'geri' })).toBe(false);
  });
});

describe('the arrangement', () => {
  it('on a step: Back, the rest, then exactly ONE primary', () => {
    const f = stepFooter(actions, true);
    expect(f.back?.id).toBe('back');
    expect(f.primary?.id).toBe('send');
    // The plugin's SECOND primary is kept, as an ordinary button.
    expect(f.others.map((a) => a.id)).toEqual(['save_draft', 'send_now']);
  });

  it('on a plain dialog: the plugin’s own arrangement is left alone', () => {
    // ⚠ Two equally weighted actions is a legitimate screen. The rule is
    // about WALKS, and applying it everywhere would be a renderer overruling
    // a design it knows nothing about.
    const f = stepFooter(actions, false);
    expect(f.back).toBeNull();
    expect(f.primary).toBeNull();
    expect(f.others.map((a) => a.id)).toEqual(['back', 'save_draft', 'send', 'send_now']);
  });

  it('survives a step with no primary and no Back', () => {
    const f = stepFooter([{ id: 'ok', label: { en: 'OK' } }], true);
    expect(f.back).toBeNull();
    expect(f.primary).toBeNull();
    expect(f.others.map((a) => a.id)).toEqual(['ok']);
  });

  it('drops actions with no id rather than drawing a button nothing can press', () => {
    expect(stepFooter([{ id: '', label: { en: '' } }], true).others).toEqual([]);
    expect(stepFooter(undefined, true).others).toEqual([]);
  });
});
