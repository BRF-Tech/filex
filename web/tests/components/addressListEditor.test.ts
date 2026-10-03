// The address list editor (Sign-in security: the allowed addresses and the
// trusted proxies): its Add button stands beside the box it adds from, level
// with it, whatever is written under the box.
//
// ⚠ Until 0.50 the hint and the error were the box's own lines (Input's
// `hint` / `error`), inside the block the button was bottom-aligned to — so a
// hint or an error pushed the button below the box. jsdom has no layout: what
// is pinned here is the STRUCTURE that makes the alignment true (the row holds
// the box and the button and nothing else; the lines are under the row); the
// alignment itself is measured in a browser (e2e/shots/loginsecurity.mjs).
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';

import AddressListEditor from '@/components/loginSecurity/AddressListEditor.vue';
import en from '@/locales/en.json';

const mounted: VueWrapper[] = [];
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
});

function mountEditor(props: Record<string, unknown> = {}) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const w = mount(AddressListEditor, {
    props: { modelValue: [], label: 'Proxy address', removeLabel: (e: string) => `Remove ${e}`, testId: 'ed', ...props },
    global: { plugins: [i18n] },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

/** The paragraph that says exactly `text`. */
function line(w: VueWrapper, text: string): HTMLElement {
  const p = w.findAll('p').find((x) => x.text() === text);
  if (!p) throw new Error(`no line says "${text}"`);
  return p.element as HTMLElement;
}

function parts(w: VueWrapper) {
  const input = w.get('input').element as HTMLInputElement;
  const add = w.get('[data-testid="address-add"]').element as HTMLElement;
  return { input, add, row: add.parentElement as HTMLElement };
}

const follows = (a: Node, b: Node) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;

describe('AddressListEditor — the Add button stays level with the box', () => {
  it('puts the box and the button in one row that holds nothing else', () => {
    const w = mountEditor({ hint: 'Addresses and networks.' });
    const { input, row } = parts(w);
    expect(row.contains(input)).toBe(true);
    expect(row.querySelector('label'), 'the label is above the row, not in it').toBeNull();
    expect(row.querySelector('p'), 'no line of text inside the row').toBeNull();
    const label = w.get('label').element as HTMLLabelElement;
    expect(label.htmlFor).toBe(input.id);
  });

  it('writes the hint under the row, and the box is described by it', () => {
    const w = mountEditor({ hint: 'Addresses and networks.' });
    const { input, row } = parts(w);
    const hint = line(w, 'Addresses and networks.');
    expect(row.contains(hint)).toBe(false);
    expect(follows(row, hint), 'the hint comes after the row').toBe(true);
    expect(hint.id).toBeTruthy();
    expect(input.getAttribute('aria-describedby')).toBe(hint.id);
    expect(input.getAttribute('aria-invalid')).toBeNull();
  });

  it('writes an error under the row instead of the hint, and marks the box invalid', () => {
    const w = mountEditor({ hint: 'Addresses and networks.', error: '"banana" is not an address' });
    const { input, row } = parts(w);
    const err = line(w, '"banana" is not an address');
    expect(row.contains(err)).toBe(false);
    expect(follows(row, err)).toBe(true);
    expect(w.findAll('p').some((p) => p.text() === 'Addresses and networks.'), 'the hint gives way to the error').toBe(false);
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(input.getAttribute('aria-describedby')).toBe(err.id);
    expect(err.getAttribute('data-testid')).toBe('field-error');
  });

  it('with neither, describes the box with nothing', () => {
    const w = mountEditor();
    expect(parts(w).input.getAttribute('aria-describedby')).toBeNull();
  });
});
