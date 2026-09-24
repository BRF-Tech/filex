// The v3 surface catalogue, as the RENDERER enforces it (docs/APP-PLUGINS-API
// §2) — not as a plugin promises to behave.
//
// ⚠⚠ Every rule here is enforced on the way IN, from the declaration, so no
// plugin can opt out of it by drawing its own control:
//   · a `select` is a row of choice buttons; there is no `<select>` in a
//     surface, anywhere;
//   · `advanced` is ignored — a surface has no collapsed section;
//   · `show_when` hides a field AND drops its value;
//   · `required_when` blocks the submit once it holds;
//   · a step has one primary button plus Back.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import SurfaceRenderer from '@brftech/filex-core/src/components/plugin/SurfaceRenderer.vue';
import SurfaceConversation from '@brftech/filex-core/src/components/plugin/SurfaceConversation.vue';
import SurfaceFooterButtons from '@brftech/filex-core/src/components/plugin/SurfaceFooterButtons.vue';
import { usePluginSurface } from '@brftech/filex-core/src/composables/usePluginSurface';
import type { PluginSurface, SurfaceNode } from '@brftech/filex-core/src/types/Plugins';

function draw(nodes: SurfaceNode[], extra: Record<string, unknown> = {}) {
  return mount(SurfaceRenderer, { props: { nodes, locale: 'en', values: {}, ...extra } });
}

const outputField = {
  key: 'output',
  type: 'select',
  label: { en: 'Where the signed document goes' },
  options: [
    { value: 'version', label: { en: 'A new version of this file' } },
    { value: 'sibling', label: { en: 'A new file beside it' } },
  ],
};

describe('no dropdowns in a surface', () => {
  it('a `select` is a row of buttons — every option readable without clicking', async () => {
    const w = draw([{ type: 'form', props: { fields: [outputField] } }], { values: { output: 'version' } });
    expect(w.findAll('select')).toHaveLength(0);
    const buttons = w.findAll('[data-testid^="fe-choice-output-"]');
    expect(buttons.map((b) => b.text())).toEqual(['A new version of this file', 'A new file beside it']);
    // Radio semantics: one is chosen and says so to a screen reader.
    expect(w.find('[data-testid="fe-choice-output-version"]').attributes('aria-checked')).toBe('true');
    expect(w.find('[data-testid="fe-choice-output-sibling"]').attributes('aria-checked')).toBe('false');

    await w.find('[data-testid="fe-choice-output-sibling"]').trigger('click');
    expect(w.emitted('update:values')?.at(-1)).toEqual([{ output: 'sibling' }]);
  });

  it('`multi: true` takes several, and sends them in the DECLARED order', async () => {
    const field = {
      key: 'kinds',
      type: 'select',
      multi: true,
      options: [{ value: 'pdf' }, { value: 'docx' }, { value: 'odt' }],
    };
    const w = draw([{ type: 'form', props: { fields: [field] } }], { values: { kinds: ['odt'] } });
    expect(w.find('[data-testid="choice-buttons"]').attributes('role')).toBe('group');
    await w.find('[data-testid="fe-choice-kinds-pdf"]').trigger('click');
    // ⚠ Declared order, not click order: two people who picked the same two
    // options send the same value, and the plugin reads a stable list.
    expect(w.emitted('update:values')?.at(-1)).toEqual([{ kinds: ['pdf', 'odt'] }]);
  });

  it('a `bool` that DECIDES something is two buttons; a plain toggle stays a toggle', async () => {
    const decision = { key: 'overwrite', type: 'bool', required: true, label: { en: 'Replace the original?' } };
    const toggle = { key: 'notify', type: 'bool', label: { en: 'Send a notification' } };
    const w = draw([{ type: 'form', props: { fields: [decision, toggle] } }], { values: {} });

    // The decision: Yes / No, both on the screen, neither pre-answered.
    expect(w.find('[data-testid="fe-choice-overwrite-true"]').text()).toBe('Yes');
    expect(w.find('[data-testid="fe-choice-overwrite-false"]').text()).toBe('No');
    await w.find('[data-testid="fe-choice-overwrite-false"]').trigger('click');
    expect(w.emitted('update:values')?.at(-1)).toEqual([{ overwrite: false }]);

    // The switch: still a checkbox, because "on/off" is not a question.
    expect(w.find('#fe-cf-notify').exists() || w.findAll('input[type="checkbox"]').length > 0).toBe(true);
    expect(w.find('[data-testid="fe-choice-notify-true"]').exists()).toBe(false);
  });

  it('`style` is read: the plugin SAYS which control it wants, and is obeyed', async () => {
    // `wire.Field.Style` (`switch` | `choice`) has been in the contract while
    // nothing in the browser read it — so "should this installation add time
    // stamps?", which has no options and is not required, could only ever be
    // a tickbox to notice rather than a question to read.
    const said = { key: 'tsa_enabled', type: 'bool', style: 'choice', label: { en: 'Add a time stamp?' } };
    // …and the other way round: a plugin that names its two labels but wants
    // a switch gets a switch. Said outright beats the guess, both directions.
    const sworn = {
      key: 'notify',
      type: 'bool',
      style: 'switch',
      label: { en: 'Send a notification' },
      options: [{ value: 'true', label: { en: 'Yes please' } }, { value: 'false', label: { en: 'No' } }],
    };
    const w = draw([{ type: 'form', props: { fields: [said, sworn] } }], { values: {} });

    expect(w.find('[data-testid="fe-choice-tsa_enabled-true"]').text()).toBe('Yes');
    expect(w.find('[data-testid="fe-choice-tsa_enabled-false"]').text()).toBe('No');
    await w.find('[data-testid="fe-choice-tsa_enabled-true"]').trigger('click');
    expect(w.emitted('update:values')?.at(-1)).toEqual([{ tsa_enabled: true }]);

    expect(w.find('[data-testid="fe-choice-notify-true"]').exists()).toBe(false);
    expect(w.findAll('input[type="checkbox"]')).toHaveLength(1);
  });

  it('`style: "switch"` cannot re-open a dropdown on a `select`', () => {
    // ⚠ `style` answers "toggle or two buttons" for a `bool`. A `select` has
    // no third rendering to fall back to — rule 1 is that a surface has no
    // dropdown, and a field-level word must not become the way around it.
    const w = draw([{ type: 'form', props: { fields: [{ ...outputField, style: 'switch' }] } }]);
    expect(w.findAll('select')).toHaveLength(0);
    expect(w.findAll('[data-testid^="fe-choice-output-"]')).toHaveLength(2);
  });
});

describe('no hidden sections', () => {
  it('`advanced` is ignored: the field is on the step like any other', () => {
    // ⚠ v3 removed `Field.Advanced` from the contract. A plugin that still
    // sends it must not get a collapsed block — "if a field matters it is on
    // the step; if it does not, it is not in the manifest".
    const w = draw([
      { type: 'form', props: { fields: [{ key: 'a', type: 'string' }, { key: 'b', type: 'string', advanced: true }] } },
    ]);
    expect(w.find('.fe-cfield__adv').exists()).toBe(false);
    expect(w.find('#fe-cf-b').exists()).toBe(true);
  });
});

describe('fields that depend on other fields', () => {
  const fields = [
    outputField,
    {
      key: 'name',
      type: 'string',
      label: { en: 'Name of the new file' },
      show_when: { key: 'output', equals: ['sibling'] },
      required_when: { key: 'output', equals: ['sibling'] },
    },
  ];

  it('the contradiction cannot be drawn', async () => {
    // "goes: a new version of this file" with a file-name box above it is the
    // screen this rule exists to make impossible.
    const version = draw([{ type: 'form', props: { fields } }], { values: { output: 'version' } });
    expect(version.find('#fe-cf-name').exists()).toBe(false);

    const sibling = draw([{ type: 'form', props: { fields } }], { values: { output: 'sibling' } });
    expect(sibling.find('#fe-cf-name').exists()).toBe(true);
    // …and once it is on the step, it is required.
    expect(sibling.find('label[for="fe-cf-name"] .fe-cfield__req').exists()).toBe(true);
  });

  it('a hidden field’s value is DROPPED from the event, not blanked', async () => {
    const posted: Array<Record<string, unknown>> = [];
    const surface: PluginSurface = {
      nodes: [{ id: 'f', type: 'form', props: { fields, values: { output: 'version', name: 'invoice.pdf' } } }],
      actions: [{ id: 'go', label: { en: 'Go' }, primary: true }],
    };
    const conv = usePluginSurface(
      {
        api: {
          pluginViewEvent: async (_p, _v, body) => {
            posted.push(body.data?.values ?? {});
            return { surface: { ...surface, done: true } };
          },
        },
        plugin: 'sign',
        view: 'v',
        locale: () => 'en',
        errorText: () => 'err',
      },
      { onOp: () => {}, onDone: () => {}, onToast: () => {} },
    );
    conv.setSurface(surface);
    await conv.press(surface.actions![0]);
    expect(posted[0]).toEqual({ output: 'version' });
    expect('name' in posted[0]).toBe(false);
  });

  it('a visible, required, empty field blocks the submit locally and is pointed at', async () => {
    const api = { pluginViewEvent: async () => ({ surface: { nodes: [] } }) };
    const surface: PluginSurface = {
      nodes: [{ id: 'f', type: 'form', props: { fields, values: { output: 'sibling' } } }],
      actions: [{ id: 'go', label: { en: 'Go' }, primary: true }],
    };
    const conv = usePluginSurface(
      { api, plugin: 'sign', view: 'v', locale: () => 'en', errorText: () => 'err' },
      { onOp: () => {}, onDone: () => {}, onToast: () => {} },
    );
    conv.setSurface(surface);
    await conv.press(surface.actions![0]);
    // ⚠ Refused HERE. The server refuses it too — that is the boundary — but
    // a round trip to be told "this is required" about a box on the screen is
    // a round trip nobody should have to make.
    expect(conv.invalidKeys.value).toEqual(['name']);

    const w = mount(SurfaceConversation, { props: { conv, locale: 'en' } });
    expect(w.find('.fe-cfield__row.is-invalid').exists()).toBe(true);

    // Answering it clears the mark as it is typed, not on the next press.
    conv.updateValues({ output: 'sibling', name: 'x' });
    expect(conv.invalidKeys.value).toEqual([]);
  });
});

describe('one step asks one thing', () => {
  it('Back first, one primary last, a second primary demoted', () => {
    const surface: PluginSurface = {
      nodes: [{ id: 's', type: 'steps', props: { items: [{ id: '1', label: { en: 'Who signs' }, state: 'active' }] } }],
      actions: [
        { id: 'back', label: { en: 'Back' } },
        { id: 'send', label: { en: 'Send' }, primary: true },
        { id: 'send_now', label: { en: 'Send now' }, primary: true },
      ],
    };
    const conv = usePluginSurface(
      {
        api: { pluginViewEvent: async () => ({ surface }) },
        plugin: 'p',
        view: 'v',
        locale: () => 'en',
        errorText: () => 'err',
      },
      { onOp: () => {}, onDone: () => {}, onToast: () => {} },
    );
    conv.setSurface(surface);
    const w = mount(SurfaceFooterButtons, { props: { conv, locale: 'en', testidPrefix: 'x' } });
    const ids = w.findAll('button').map((b) => b.attributes('data-testid'));
    expect(ids).toEqual(['x-action-back', 'x-action-send_now', 'x-action-send']);
    expect(w.find('[data-testid="x-action-send"]').classes()).toContain('fe-btn--primary');
    // ⚠ Kept, not hidden: dropping a plugin's button would break a wizard in
    // a way no test of ours would ever see.
    expect(w.find('[data-testid="x-action-send_now"]').classes()).not.toContain('fe-btn--primary');
    expect(w.find('[data-testid="x-action-back"]').classes()).toContain('fe-surface__back');
  });
});
