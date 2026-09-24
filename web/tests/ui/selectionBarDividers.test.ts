// The selection bar's "⋯" draws the same lines as the right click.
//
// ⚠ The owner, 2026-09-21: "dönüştür ve imzalama pluginlerinin menüleri —
// yani her plugin'in menüleri — arasına çizgi çekelim". lib/pluginMenu puts a
// divider before every app's group; the right click drew them, but the bar's
// "⋯" was built from a divider-free list, so two apps' verbs ran together as
// one column right beside a menu that separated them.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { nextTick } from 'vue';

import Toolbar from '@brftech/filex-core/src/components/Toolbar.vue';

const actions = [
  { key: 'open', label: 'Open' },
  { key: 'tags', label: 'Tags…' },
  { divider: true, key: 'sep-plugins', label: '' },
  { key: 'plugin:convert/convert', label: 'Convert…' },
  { divider: true, key: 'sep-plugin:sign', label: '' },
  { key: 'plugin:sign/sign', label: 'Sign…' },
  { key: 'plugin:sign/request', label: 'Request signatures' },
];

let w: VueWrapper | null = null;
afterEach(() => {
  w?.unmount();
  w = null;
  document.body.innerHTML = '';
});

describe('selection bar "⋯"', () => {
  it('keeps a line between every app’s actions', async () => {
    // The bar teleports into the explorer's `.fe__primary`; give it one.
    document.body.innerHTML = '<div class="fe"><div id="tb"></div><div class="fe__primary"></div></div>';
    w = mount(Toolbar, {
      props: {
        viewMode: 'list',
        searchQuery: '',
        trashActive: false,
        actions,
        locale: 'en',
        selectionMode: 'single',
        selectionCount: 1,
      },
      attachTo: '#tb',
    });
    await nextTick();
    await nextTick();
    const more = document.querySelector<HTMLElement>('[data-testid="selbar-more"]');
    expect(more, 'the bar has a "⋯" for the verbs that are not icons').not.toBeNull();
    more!.click();
    await nextTick();
    const menu = [...document.querySelectorAll('.fe-ctx')].pop()!;
    const seq = [...menu.querySelectorAll('[role="menuitem"], [role="separator"]')].map((el) =>
      el.getAttribute('role') === 'separator' ? '—' : (el.textContent ?? '').trim().split('\n')[0].trim(),
    );
    const convert = seq.findIndex((s) => s.startsWith('Convert'));
    const sign = seq.findIndex((s) => s.startsWith('Sign'));
    expect(convert).toBeGreaterThan(-1);
    expect(sign).toBeGreaterThan(convert);
    // A line before the first app, and one between the two apps.
    expect(seq[convert - 1]).toBe('—');
    expect(seq[sign - 1]).toBe('—');
  });
});
