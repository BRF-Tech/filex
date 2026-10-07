/**
 * Core `ChoiceSelect` — one answer from a list, without the native dropdown
 * (#160), and `ChoiceButtons`' segmented strip beside it (with each cell's
 * meaning as a hover tip, a description and, without hover, a line).
 *
 * Pinned here: what a screen reader is told (the WAI-ARIA select-only
 * combobox), the value that comes back (the option's own, a number stays a
 * number), the keyboard (arrows, Home / End, typing, Enter, Escape, Tab), a
 * press outside, a disabled option and a disabled control, the form contract
 * (a hidden input carries `name` and `required`; the refusal is the host's to
 * word), where the list is placed (lib/listPlacement, right to left
 * included), and where it is mounted (<body>, or the open <dialog>).
 *
 * The rule the control serves is guarded in web/tests/ui/noNativeSelect.test.ts.
 */
import { describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { defineComponent, h, markRaw, nextTick, ref } from 'vue';

import ChoiceSelect from '@brftech/filex-core/src/components/ChoiceSelect.vue';
import ChoiceButtons from '@brftech/filex-core/src/components/ChoiceButtons.vue';
import { placeListUnder } from '@brftech/filex-core/src/lib/listPlacement';
import { chosenValue, comboOf, listedOptions, listOf, openList, pickOption, shownText } from '../helpers/choiceSelect';

const FRUIT = [
  { value: 'apple', label: 'Apple' },
  { value: 'banana', label: 'Banana', help: 'yellow, curved' },
  { value: 'cherry', label: 'Cherry', disabled: true },
  { value: 'date', label: 'Date' },
  { value: 'elder', label: 'Elderberry' },
];

/** Mounted with a live v-model: an emitted value comes back as the prop. */
function draw(props: Record<string, unknown> = {}, attachTo: HTMLElement = document.body) {
  const w = mount(ChoiceSelect, {
    props: {
      modelValue: 'banana',
      options: FRUIT,
      ...props,
      'onUpdate:modelValue': (v: string | number) => w.setProps({ modelValue: v }),
    },
    attachTo,
  });
  return w;
}

function combo(w: ReturnType<typeof draw>) {
  return w.get('[role="combobox"]');
}

async function key(w: ReturnType<typeof draw>, k: string, init: KeyboardEventInit = {}) {
  await combo(w).trigger('keydown', { key: k, ...init });
  await nextTick();
  await nextTick();
}

/** The option the list marks active (aria-activedescendant). */
function activeValue(w: ReturnType<typeof draw>): string | null {
  const id = combo(w).attributes('aria-activedescendant');
  return id ? (document.getElementById(id)?.getAttribute('data-value') ?? null) : null;
}

describe('ChoiceSelect — what a screen reader is told', () => {
  it('closed: a combobox that says it opens a listbox, showing the chosen label', () => {
    const w = draw();
    const c = combo(w);
    expect(c.element.tagName).toBe('BUTTON');
    expect(c.attributes('aria-haspopup')).toBe('listbox');
    expect(c.attributes('aria-expanded')).toBe('false');
    expect(c.attributes('aria-controls')).toBeTruthy();
    expect(c.attributes('aria-activedescendant')).toBeUndefined();
    expect(listOf(c.element)).toBeNull();
    expect(shownText(c.element)).toBe('Banana');
    expect(chosenValue(c.element)).toBe('banana');
    // No native element anywhere, and no form input when no form needs one.
    expect(w.find('select').exists()).toBe(false);
    expect(w.find('input').exists()).toBe(false);
  });

  it('open: a listbox of options, the chosen one selected and active, a disabled one marked', async () => {
    const w = draw();
    const list = await openList(combo(w).element);
    expect(combo(w).attributes('aria-expanded')).toBe('true');
    expect(list.getAttribute('role')).toBe('listbox');
    expect(list.id).toBe(combo(w).attributes('aria-controls'));
    const opts = Array.from(list.querySelectorAll('[role="option"]'));
    expect(opts.map((o) => o.getAttribute('aria-selected'))).toEqual(['false', 'true', 'false', 'false', 'false']);
    expect(opts[2].getAttribute('aria-disabled')).toBe('true');
    expect(activeValue(w)).toBe('banana');
    // The second line says what the choice means.
    expect(opts[1].querySelector('.fe-select__help')?.textContent).toBe('yellow, curved');
  });

  it('shows the placeholder while nothing is chosen', () => {
    const w = draw({ modelValue: '', placeholder: 'Pick a fruit' });
    expect(shownText(combo(w).element)).toBe('Pick a fruit');
    expect(combo(w).find('.fe-select__value').classes()).toContain('is-placeholder');
  });

  it('takes its accessible name from aria-label, or from a label that points at its id', () => {
    const named = draw({ ariaLabel: 'Fruit' });
    expect(combo(named).attributes('aria-label')).toBe('Fruit');
    const byId = draw({ id: 'fruit-field' });
    expect(combo(byId).attributes('id')).toBe('fruit-field');
  });
});

describe('ChoiceSelect — the value that comes back', () => {
  it('a click on an option chooses it, says so twice (v-model and change), closes and keeps the focus', async () => {
    const w = draw();
    await pickOption(combo(w).element, 'date');
    expect(w.emitted('update:modelValue')).toEqual([['date']]);
    expect(w.emitted('change')).toEqual([['date']]);
    expect(combo(w).attributes('aria-expanded')).toBe('false');
    expect(shownText(combo(w).element)).toBe('Date');
    expect(document.activeElement).toBe(combo(w).element);
  });

  it('choosing what is already chosen says nothing', async () => {
    const w = draw();
    await pickOption(combo(w).element, 'banana');
    expect(w.emitted('update:modelValue')).toBeUndefined();
    expect(w.emitted('change')).toBeUndefined();
  });

  it('a number stays a number — no `.number` needed, and a mixed list keeps its types', async () => {
    const w = draw({
      modelValue: 0,
      options: [
        { value: 0, label: 'Never' },
        { value: 7, label: '7 days' },
        { value: '', label: 'Server default' },
      ],
    });
    expect(shownText(combo(w).element)).toBe('Never');
    await pickOption(combo(w).element, 7);
    expect(w.emitted('update:modelValue')?.[0]).toEqual([7]);
    expect(typeof w.emitted('update:modelValue')?.[0]?.[0]).toBe('number');
    await pickOption(combo(w).element, '');
    expect(w.emitted('update:modelValue')?.[1]).toEqual(['']);
  });
});

describe('ChoiceSelect — the keyboard', () => {
  it('ArrowDown opens on the chosen option; the arrows skip a disabled one; Enter chooses', async () => {
    const w = draw();
    await key(w, 'ArrowDown');
    expect(combo(w).attributes('aria-expanded')).toBe('true');
    expect(activeValue(w)).toBe('banana');
    await key(w, 'ArrowDown');
    expect(activeValue(w), 'Cherry is disabled').toBe('date');
    await key(w, 'ArrowUp');
    expect(activeValue(w)).toBe('banana');
    await key(w, 'ArrowDown');
    await key(w, 'Enter');
    expect(w.emitted('update:modelValue')).toEqual([['date']]);
    expect(combo(w).attributes('aria-expanded')).toBe('false');
  });

  it('Home and End go to the first and last options that can be chosen', async () => {
    const w = draw({ options: [{ value: 'x', label: 'X', disabled: true }, ...FRUIT, { value: 'z', label: 'Z', disabled: true }] });
    await key(w, 'End');
    expect(combo(w).attributes('aria-expanded'), 'End opens it too').toBe('true');
    expect(activeValue(w)).toBe('elder');
    await key(w, 'Home');
    expect(activeValue(w)).toBe('apple');
  });

  it('Space opens, and Space chooses', async () => {
    const w = draw();
    await key(w, ' ');
    expect(combo(w).attributes('aria-expanded')).toBe('true');
    await key(w, 'ArrowUp');
    await key(w, ' ');
    expect(w.emitted('update:modelValue')).toEqual([['apple']]);
  });

  it('Escape closes without a change, and goes no further: the dialog around it stays open', async () => {
    const outer = vi.fn();
    document.addEventListener('keydown', outer);
    try {
      const w = draw();
      await key(w, 'ArrowDown');
      await key(w, 'ArrowDown');
      // Dispatched by hand so it is certain to BUBBLE: only stopPropagation
      // can keep it from the document, where a dialog listens for Escape.
      const esc = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
      combo(w).element.dispatchEvent(esc);
      await nextTick();
      expect(esc.defaultPrevented).toBe(true);
      expect(combo(w).attributes('aria-expanded')).toBe('false');
      expect(w.emitted('update:modelValue')).toBeUndefined();
      const escapes = outer.mock.calls.filter(([e]) => (e as KeyboardEvent).key === 'Escape');
      expect(escapes, 'the Escape that closed the list must not close the dialog').toHaveLength(0);
    } finally {
      document.removeEventListener('keydown', outer);
    }
  });

  it('Tab chooses the active option and lets the focus move on', async () => {
    const w = draw();
    await key(w, 'ArrowDown');
    await key(w, 'ArrowDown');
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    combo(w).element.dispatchEvent(tab);
    await nextTick();
    expect(w.emitted('update:modelValue')).toEqual([['date']]);
    expect(tab.defaultPrevented, 'the focus goes on to the next field').toBe(false);
  });

  it('typing jumps to the next label that starts with it, and passes a disabled one by', async () => {
    // Letters typed within 600 ms are one word; the clock is held so each
    // letter here is a word of its own unless the test says otherwise.
    let now = 1_000_000;
    const clock = vi.spyOn(Date, 'now').mockImplementation(() => now);
    try {
      const w = draw();
      await key(w, 'e');
      expect(combo(w).attributes('aria-expanded'), 'a letter on the closed field opens it there').toBe('true');
      expect(activeValue(w)).toBe('elder');
      now += 1000;
      await key(w, 'c');
      expect(activeValue(w), 'Cherry cannot be chosen').toBe('elder');
      now += 1000;
      await key(w, 'd');
      expect(activeValue(w)).toBe('date');
      // Two quick letters are one word: "ba" stays on Banana past Apple.
      now += 1000;
      await key(w, 'b');
      now += 100;
      await key(w, 'a');
      expect(activeValue(w)).toBe('banana');
    } finally {
      clock.mockRestore();
    }
  });
});

describe('ChoiceSelect — closing, and what cannot be chosen', () => {
  it('a press outside closes the list without a change; a press inside it does not', async () => {
    const w = draw();
    const list = await openList(combo(w).element);
    list.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(combo(w).attributes('aria-expanded')).toBe('true');
    const elsewhere = document.createElement('div');
    document.body.appendChild(elsewhere);
    elsewhere.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(combo(w).attributes('aria-expanded')).toBe('false');
    expect(listOf(combo(w).element)).toBeNull();
    expect(w.emitted('update:modelValue')).toBeUndefined();
  });

  it('a disabled option is listed but a click on it chooses nothing and leaves the list open', async () => {
    const w = draw();
    const options = await listedOptions(combo(w).element);
    expect(options.find((o) => o.value === 'cherry')).toMatchObject({ label: 'Cherry', disabled: true });
    const list = await openList(combo(w).element);
    (list.querySelector('[data-value="cherry"]') as HTMLElement).click();
    await nextTick();
    expect(w.emitted('update:modelValue')).toBeUndefined();
    expect(combo(w).attributes('aria-expanded')).toBe('true');
  });

  it('a disabled control does not open, by click or by key', async () => {
    const w = draw({ disabled: true });
    expect(combo(w).attributes('disabled')).toBeDefined();
    await combo(w).trigger('click');
    await key(w, 'ArrowDown');
    expect(combo(w).attributes('aria-expanded')).toBe('false');
    expect(listOf(combo(w).element)).toBeNull();
  });

  it('a list open when the control is disabled closes', async () => {
    const w = draw();
    await openList(combo(w).element);
    await w.setProps({ disabled: true });
    await nextTick();
    expect(combo(w).attributes('aria-expanded')).toBe('false');
  });
});

describe('ChoiceSelect — in a form', () => {
  it('a name or `required` puts a hidden input in the form, carrying the value', () => {
    const w = draw({ name: 'fruit', required: true });
    const input = w.get('input.fe-select__native');
    expect(input.attributes('name')).toBe('fruit');
    expect(input.attributes('required')).toBeDefined();
    expect((input.element as HTMLInputElement).value).toBe('banana');
    expect(input.attributes('tabindex')).toBe('-1');
    expect(input.attributes('aria-hidden')).toBe('true');
    expect(combo(w).attributes('aria-required')).toBe('true');
  });

  it('a refusal: no browser bubble, the field marked and focused, and the host told', async () => {
    const w = draw({ modelValue: '', required: true, name: 'fruit' });
    const ev = new Event('invalid', { cancelable: true });
    w.get('input.fe-select__native').element.dispatchEvent(ev);
    await nextTick();
    expect(ev.defaultPrevented).toBe(true);
    expect(w.emitted('invalid')).toHaveLength(1);
    expect(combo(w).attributes('aria-invalid')).toBe('true');
    expect(document.activeElement).toBe(combo(w).element);
    // Choosing clears the mark.
    await pickOption(combo(w).element, 'apple');
    expect(combo(w).attributes('aria-invalid')).toBeUndefined();
  });

  it('two refused fields: the FIRST takes the focus (the rule web lib/formCheck keeps)', async () => {
    const a = ref('');
    const b = ref('');
    const Form = defineComponent({
      render: () =>
        h('form', [
          h(ChoiceSelect, { modelValue: a.value, options: FRUIT, required: true, name: 'a', testid: 'first' }),
          h(ChoiceSelect, { modelValue: b.value, options: FRUIT, required: true, name: 'b', testid: 'second' }),
        ]),
    });
    const w = mount(Form, { attachTo: document.body });
    for (const input of w.findAll('input.fe-select__native')) {
      input.element.dispatchEvent(new Event('invalid', { cancelable: true }));
    }
    await nextTick();
    expect(document.activeElement).toBe(w.get('[data-testid="first"]').element);
  });
});

describe('ChoiceSelect — where the list goes', () => {
  it('is mounted under <body>, outside the tree that opened it (no overflow can clip it)', async () => {
    const host = document.createElement('div');
    host.style.overflow = 'hidden';
    document.body.appendChild(host);
    const w = draw({}, host);
    const list = await openList(combo(w).element);
    expect(host.contains(list)).toBe(false);
    expect(list.parentElement).toBe(document.body);
  });

  it('…or inside the open <dialog> the field sits in: a modal dialog is above everything <body> stacks', async () => {
    const dialog = document.createElement('dialog');
    dialog.setAttribute('open', '');
    document.body.appendChild(dialog);
    const w = draw({}, dialog);
    const list = await openList(combo(w).element);
    expect(list.closest('dialog')).toBe(dialog);
  });

  it('carries the direction it is laid out in: right to left when the field is', async () => {
    const host = document.createElement('div');
    host.setAttribute('dir', 'rtl');
    document.body.appendChild(host);
    const w = draw({}, host);
    const list = await openList(combo(w).element);
    expect(list.getAttribute('dir')).toBe('rtl');
  });
});

describe('lib/listPlacement — under the field, along the line, inside the window', () => {
  const field = { top: 100, bottom: 134, left: 100, right: 300, width: 200 };
  const desk = { width: 1280, height: 800 };

  it('opens under the field, from its start edge, as wide as the field', () => {
    expect(placeListUnder(field, desk, 'ltr', 5)).toEqual({
      top: 138,
      bottom: 704,
      start: 100,
      width: 200,
      maxHeight: 320,
      up: false,
    });
  });

  it('right to left: starts at the field’s RIGHT edge, measured from the right of the window', () => {
    // The field's right edge is 980px from the window's right edge.
    expect(placeListUnder(field, desk, 'rtl', 5).start).toBe(980);
  });

  it('opens upwards when there is no room below and more above', () => {
    const low = { top: 700, bottom: 734, left: 100, right: 300, width: 200 };
    const at = placeListUnder(low, desk, 'ltr', 8);
    expect(at.up).toBe(true);
    expect(at.bottom).toBe(104);
    expect(at.maxHeight).toBe(320);
  });

  it('a narrow field gets a list of at least 180px; at 375px it stays inside the window', () => {
    const small = { top: 100, bottom: 134, left: 300, right: 360, width: 60 };
    const phone = { width: 375, height: 700 };
    const at = placeListUnder(small, phone, 'ltr', 3);
    expect(at.width).toBe(180);
    expect(at.start + at.width).toBeLessThanOrEqual(375 - 8);
    const wide = { top: 100, bottom: 134, left: 0, right: 375, width: 375 };
    expect(placeListUnder(wide, phone, 'ltr', 3).width, 'never wider than the window less its gutters').toBe(359);
  });
});

describe('ChoiceButtons — the segmented strip (FaSegmented, moved into core)', () => {
  const UNITS = [
    { value: 'kb', label: 'KB' },
    { value: 'mb', label: 'MB' },
    { value: 'gb', label: 'GB' },
  ];

  it('is the same radio group, drawn as one strip', () => {
    const w = mount(ChoiceButtons, {
      props: { modelValue: 'mb', options: UNITS, segmented: true, testid: 'unit', testidPrefix: 'unit' },
      attachTo: document.body,
    });
    const group = w.get('[data-testid="unit"]');
    expect(group.attributes('role')).toBe('radiogroup');
    expect(group.classes()).toContain('fe-choice--segmented');
    expect(w.get('[data-testid="unit-mb"]').attributes('aria-checked')).toBe('true');
    expect(w.findAll('[role="radio"]').map((b) => b.attributes('tabindex'))).toEqual(['-1', '0', '-1']);
  });

  it('Home and End choose the first and last answers that can be chosen', async () => {
    const w = mount(ChoiceButtons, {
      props: { modelValue: 'mb', options: [...UNITS, { value: 'tb', label: 'TB', disabled: true }], segmented: true },
      attachTo: document.body,
    });
    await w.findAll('button')[1].trigger('keydown', { key: 'End' });
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['gb']);
    await w.findAll('button')[1].trigger('keydown', { key: 'Home' });
    expect(w.emitted('update:modelValue')?.[1]).toEqual(['kb']);
  });

  it('icons only: the label is the accessible name and the hover tip', () => {
    const Sun = markRaw(defineComponent({ render: () => h('svg', { class: 'sun' }) }));
    const w = mount(ChoiceButtons, {
      props: {
        modelValue: 'light',
        options: [
          { value: 'light', label: 'Light', icon: Sun },
          { value: 'dark', label: 'Dark', icon: Sun },
        ],
        iconOnly: true,
      },
      attachTo: document.body,
    });
    const first = w.findAll('button')[0];
    expect(w.get('[data-testid="choice-buttons"]').classes()).toEqual(
      expect.arrayContaining(['fe-choice--segmented', 'fe-choice--icons']),
    );
    expect(first.attributes('aria-label')).toBe('Light');
    // The header's hover tip, not the browser's `title` (which would stack on it).
    expect(first.attributes('title')).toBeUndefined();
    const tip = first.element.nextElementSibling as HTMLElement;
    expect(tip.classList.contains('fe-choice__tip')).toBe(true);
    expect(tip.textContent).toBe('Light');
    // Its words ARE the name, so it is not a description too (heard twice).
    expect(first.attributes('aria-describedby')).toBeUndefined();
    expect(first.find('svg.sun').exists()).toBe(true);
    expect(first.find('.fe-choice__label').exists()).toBe(false);
  });

  // The owner, 2026-10-06, on the sharing dialog's access levels: "yan yana
  // düğmeler, üzerine gelince açıklaması yazar".
  const LEVELS = [
    { value: 'viewer', label: 'Viewer', help: 'view + download' },
    { value: 'editor', label: 'Editor', help: 'read + write + delete' },
    { value: 'owner', label: 'Owner', help: 'edit + manage access' },
  ];

  it('a cell says what it means: a hover tip beside it, and the description a screen reader hears', () => {
    const w = mount(ChoiceButtons, {
      props: { modelValue: 'editor', options: LEVELS, segmented: true, testidPrefix: 'lvl' },
      attachTo: document.body,
    });
    const radios = w.findAll('[role="radio"]');
    // The meaning is not part of the cell's name: the name stays the label.
    expect(radios.map((r) => r.text())).toEqual(['Viewer', 'Editor', 'Owner']);
    expect(radios.map((r) => r.attributes('title'))).toEqual([undefined, undefined, undefined]);
    LEVELS.forEach((o, i) => {
      const id = radios[i].attributes('aria-describedby');
      expect(id, `${o.value} is described`).toBeTruthy();
      const tip = document.getElementById(id!)!;
      expect(tip.textContent).toBe(o.help);
      expect(tip.classList.contains('fe-choice__tip')).toBe(true);
      // Hidden from the reading order, or the meaning would be read twice.
      expect(tip.getAttribute('aria-hidden')).toBe('true');
      // The tip sits beside its button in one cell: CSS shows it on the
      // cell's :hover and on the button's :focus-visible (`+ .fe-choice__tip`).
      expect(radios[i].element.nextElementSibling).toBe(tip);
      expect(tip.parentElement?.classList.contains('fe-choice__cell')).toBe(true);
    });
    // The ids are the group's own: two groups on one page do not share them.
    const again = mount(ChoiceButtons, {
      props: { modelValue: 'editor', options: LEVELS, segmented: true },
      attachTo: document.body,
    });
    expect(again.findAll('[role="radio"]')[0].attributes('aria-describedby')).not.toBe(
      radios[0].attributes('aria-describedby'),
    );
  });

  it('on a screen that cannot hover, the chosen cell’s meaning is a line under the strip', async () => {
    const w = mount(ChoiceButtons, {
      props: {
        modelValue: 'viewer',
        options: LEVELS,
        segmented: true,
        testidPrefix: 'lvl',
        'onUpdate:modelValue': (v: string | string[]) => w.setProps({ modelValue: v }),
      },
      attachTo: document.body,
    });
    const note = () => w.get('[data-testid="lvl-note"]');
    // Drawn always, shown by CSS only under `(hover: none)` (base.css).
    expect(note().classes()).toContain('fe-choice__note');
    expect(note().attributes('aria-hidden')).toBe('true');
    expect(note().text()).toBe('view + download');
    // Outside the strip: the strip is the bordered box, the line is under it.
    expect(note().element.closest('.fe-choice__strip')).toBeNull();
    await w.get('[data-testid="lvl-owner"]').trigger('click');
    expect(note().text()).toBe('edit + manage access');
  });

  it('the arrows still move between cells, now that each button sits in a cell of its own', async () => {
    const w = mount(ChoiceButtons, {
      props: { modelValue: 'viewer', options: LEVELS, segmented: true },
      attachTo: document.body,
    });
    const buttons = w.findAll('button');
    await buttons[0].trigger('keydown', { key: 'ArrowRight' });
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['editor']);
    expect(document.activeElement).toBe(buttons[1].element);
    await buttons[1].trigger('keydown', { key: 'End' });
    expect(document.activeElement).toBe(buttons[2].element);
  });

  it('the default look is unchanged: separate buttons, each with its second line, no tip', () => {
    const w = mount(ChoiceButtons, {
      props: { modelValue: 'a', options: [{ value: 'a', label: 'A', help: 'first' }, { value: 'b', label: 'B' }] },
      attachTo: document.body,
    });
    expect(w.get('[data-testid="choice-buttons"]').classes()).not.toContain('fe-choice--segmented');
    expect(w.find('.fe-choice__help').text()).toBe('first');
    expect(w.find('.fe-choice__tip').exists()).toBe(false);
    expect(w.find('.fe-choice__note').exists()).toBe(false);
    expect(w.findAll('button')[0].attributes('aria-describedby')).toBeUndefined();
  });
});

// The helpers the suites drive lists with resolve a combobox from around it,
// too — pinned so a refactor of the markup cannot quietly break every caller.
describe('tests/helpers/choiceSelect', () => {
  it('finds the combobox from itself, from a wrapper and from its form input', () => {
    const w = draw({ name: 'fruit' });
    const c = combo(w).element;
    expect(comboOf(c)).toBe(c);
    expect(comboOf(w.element)).toBe(c);
    expect(comboOf(w.get('input').element)).toBe(c);
  });
});
