// A form field the browser refuses is refused in the PANEL's words, under the
// box — never with the browser's own bubble.
//
// ⚠⚠ Release-candidate sweep, 2026-09-22 (QA #38): "Add storage", "Edit user",
// a replica target… were checked by the browser, which put "Please fill out
// this field." — in the BROWSER's language — over a Turkish panel. The shared
// field components take the browser's verdict (the form is still not sent)
// and say it themselves (lib/formCheck).
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import { isHttpUrl, validityMessage } from '@/lib/formCheck';

describe('field validity in the panel language', () => {
  it('says why an empty required box was refused, and suppresses the bubble', async () => {
    const w = mount(Input, { props: { modelValue: '', required: true, label: 'Ad' }, attachTo: document.body });
    const el = w.find('input').element as HTMLInputElement;
    const ev = new Event('invalid', { cancelable: true });
    el.dispatchEvent(ev);
    await nextTick();
    expect(ev.defaultPrevented, "the browser's own bubble is suppressed").toBe(true);
    expect(w.find('[data-testid="field-error"]').text()).toBe(validityMessage(el));
    expect(w.find('input').attributes('aria-invalid')).toBe('true');
    // Typing clears it.
    await w.find('input').setValue('x');
    expect(w.find('[data-testid="field-error"]').exists()).toBe(false);
    w.unmount();
  });

  it('does the same for a select', async () => {
    const w = mount(Select, {
      props: { modelValue: '', required: true, options: [{ value: 'a', label: 'A' }], placeholder: '—' },
      attachTo: document.body,
    });
    const ev = new Event('invalid', { cancelable: true });
    w.find('select').element.dispatchEvent(ev);
    await nextTick();
    expect(ev.defaultPrevented).toBe(true);
    expect(w.find('[data-testid="field-error"]').exists()).toBe(true);
    w.unmount();
  });

  it('an address is empty or absolute http(s) — the rule the server applies', () => {
    expect(isHttpUrl('')).toBe(true);
    expect(isHttpUrl('https://office.example.com')).toBe(true);
    expect(isHttpUrl('http://onlyoffice:80')).toBe(true);
    expect(isHttpUrl('bu-bir-adres-degil')).toBe(false);
    expect(isHttpUrl('ftp://x')).toBe(false);
  });
});
