// Core's MegaMenu (GitHub #82, 0.51.0) - the admin panel's navigation.
//
// The owner's shape (2026-10-03): the dashboard as a plain link, then a few
// top entries that each open a panel whose sections are columns, every page
// with a short line under it; on a phone the same entries as a headed list,
// all open. Opens on a click, never on hover; keyboard and screen reader by
// the WAI-ARIA disclosure-navigation pattern.
//
// What is pinned here, in order:
//   1. the wiring a screen reader reads: link vs button, aria-expanded and
//      aria-controls that point at the panel, lists labelled by their
//      headings, aria-current on the page being looked at;
//   2. one panel at a time, a click to open and close, nothing on hover;
//   3. the keyboard: ↓ opens and enters, ↑/↓/Home/End inside, ↑ from the
//      first page back to the button, Esc closes and returns focus, ←/→ along
//      the top (mirrored right to left);
//   4. leaving closes: a click outside, focus moving out;
//   5. a plain click is the host's navigation, a modified one the browser's;
//   6. an emptied section and an emptied entry are not drawn;
//   7. the phone's list: every section open, no buttons, no short lines.
import { describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { defineComponent, h, nextTick } from 'vue';

import MegaMenu from '@brftech/filex-core/src/components/MegaMenu.vue';
import { pruneMegaMenu, stepAlongBar, stepFocus, type MegaMenuEntry } from '@brftech/filex-core/src/lib/megaMenu';

const Glyph = defineComponent({ render: () => h('svg', { class: 'test-glyph' }) });

function entries(): MegaMenuEntry[] {
  return [
    { id: 'dashboard', label: 'Dashboard', href: '/admin/dashboard', active: false, icon: Glyph },
    {
      id: 'files',
      label: 'Files & storage',
      sections: [
        {
          id: 'files',
          label: 'Files',
          items: [
            { id: 'explore', label: 'Files', hint: 'Open the file manager', href: '/admin/explore', icon: Glyph },
            { id: 'trash', label: 'Trash', hint: 'Deleted files', href: '/admin/trash', icon: Glyph },
          ],
        },
        { id: 'apps', label: 'Apps', items: [] },
        {
          id: 'storage',
          label: 'Storage',
          items: [{ id: 'storages', label: 'Storages', hint: 'The storages filex serves', href: '/admin/storages', active: true, iconName: 'plugin' }],
        },
      ],
    },
    {
      id: 'people',
      label: 'People & security',
      sections: [
        {
          id: 'people',
          label: 'People & access',
          items: [{ id: 'users', label: 'Users', hint: 'Accounts and their roles', href: '/admin/users' }],
        },
      ],
    },
    // Nothing the reader may open: the whole entry goes.
    { id: 'system', label: 'System', sections: [{ id: 'maintenance', label: 'Maintenance', items: [] }] },
  ];
}

/* Attached to the document, so focus really moves (setup.ts's teardown
   unmounts it and empties <body> after each test). */
function draw(props: Record<string, unknown> = {}, dir?: 'rtl') {
  const host = document.createElement('div');
  if (dir) host.setAttribute('dir', dir);
  document.body.appendChild(host);
  return mount(MegaMenu, { props: { entries: entries(), label: 'Admin menu', ...props }, attachTo: host });
}

const top = (w: VueWrapper, id: string) => w.get(`[data-testid="nav-top-${id}"]`);
const panel = (w: VueWrapper, id: string) => w.get(`[data-testid="nav-panel-${id}"]`);
const focused = () => (document.activeElement as HTMLElement | null)?.getAttribute('data-testid');

describe('MegaMenu (bar) - what a screen reader is told', () => {
  it('is a named navigation landmark', () => {
    const w = draw();
    const nav = w.get('nav');
    expect(nav.attributes('aria-label')).toBe('Admin menu');
    expect(nav.attributes('role')).toBeUndefined();
  });

  it('draws the dashboard as a LINK and every other entry as a button that controls its panel', () => {
    const w = draw();
    const dash = w.get('[data-testid="nav-dashboard"]');
    expect(dash.element.tagName).toBe('A');
    expect(dash.attributes('href')).toBe('/admin/dashboard');
    const btn = top(w, 'files');
    expect(btn.element.tagName).toBe('BUTTON');
    expect(btn.attributes('aria-expanded')).toBe('false');
    const controls = btn.attributes('aria-controls')!;
    expect(controls).toBeTruthy();
    expect(document.getElementById(controls)).toBe(panel(w, 'files').element);
  });

  it('labels every section list with its heading, and marks the page being looked at', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    const list = panel(w, 'files').get('[data-testid="nav-group-storage"] ul');
    const heading = document.getElementById(list.attributes('aria-labelledby')!);
    expect(heading?.textContent).toBe('Storage');
    expect(w.get('[data-testid="nav-storages"]').attributes('aria-current')).toBe('page');
    expect(w.get('[data-testid="nav-trash"]').attributes('aria-current')).toBeUndefined();
    // The entry holding it says so too (a visual mark; the link carries the ARIA).
    expect(top(w, 'files').classes()).toContain('is-current');
    expect(top(w, 'people').classes()).not.toContain('is-current');
  });

  it('puts the short line under each page, and draws a named core glyph as static SVG', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    const trash = w.get('[data-testid="nav-trash"]');
    expect(trash.get('.fx-mega__label').text()).toBe('Trash');
    expect(trash.get('.fx-mega__hint').text()).toBe('Deleted files');
    expect(w.get('[data-testid="nav-storages"] .fx-mega__glyph svg').exists()).toBe(true);
  });
});

describe('MegaMenu (bar) - opening and closing', () => {
  it('opens on a click and closes on the next one', async () => {
    const w = draw();
    expect(panel(w, 'files').isVisible()).toBe(false);
    await top(w, 'files').trigger('click');
    expect(top(w, 'files').attributes('aria-expanded')).toBe('true');
    expect(panel(w, 'files').isVisible()).toBe(true);
    await top(w, 'files').trigger('click');
    expect(top(w, 'files').attributes('aria-expanded')).toBe('false');
    expect(panel(w, 'files').isVisible()).toBe(false);
  });

  it('keeps one panel open at a time', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    await top(w, 'people').trigger('click');
    expect(panel(w, 'files').isVisible()).toBe(false);
    expect(panel(w, 'people').isVisible()).toBe(true);
  });

  it('does not open on hover', async () => {
    const w = draw();
    await top(w, 'files').trigger('mouseenter');
    await top(w, 'files').trigger('mouseover');
    expect(panel(w, 'files').isVisible()).toBe(false);
  });

  it('closes on a press outside it', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    const outside = document.createElement('button');
    document.body.appendChild(outside);
    outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(panel(w, 'files').isVisible()).toBe(false);
  });

  it('stays open on a press inside it', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    panel(w, 'files').element.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(panel(w, 'files').isVisible()).toBe(true);
  });

  it('closes when focus moves out of the menu', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    const outside = document.createElement('button');
    document.body.appendChild(outside);
    await top(w, 'files').trigger('focusout', { relatedTarget: outside });
    expect(panel(w, 'files').isVisible()).toBe(false);
  });

  it('closes when the page being looked at changes (Back, a link on the page)', async () => {
    const w = draw();
    await top(w, 'people').trigger('click');
    const moved = entries();
    (moved[1].sections![2].items[0] as { active?: boolean }).active = false;
    moved[2].sections![0].items[0].active = true;
    await w.setProps({ entries: moved });
    expect(panel(w, 'people').isVisible()).toBe(false);
  });
});

describe('MegaMenu (bar) - the keyboard', () => {
  it('↓ on a button opens its panel and goes to the first page', async () => {
    const w = draw();
    await top(w, 'files').trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    expect(panel(w, 'files').isVisible()).toBe(true);
    expect(focused()).toBe('nav-explore');
  });

  it('↑ on a button opens its panel and goes to the last page', async () => {
    const w = draw();
    await top(w, 'files').trigger('keydown', { key: 'ArrowUp' });
    await nextTick();
    expect(focused()).toBe('nav-storages');
  });

  it('↓ / ↑ / Home / End walk the pages in reading order, across the columns', async () => {
    const w = draw();
    await top(w, 'files').trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    const p = panel(w, 'files');
    await p.trigger('keydown', { key: 'ArrowDown' });
    expect(focused()).toBe('nav-trash');
    await p.trigger('keydown', { key: 'ArrowDown' });
    expect(focused()).toBe('nav-storages');
    // The run does not wrap: the end is the end.
    await p.trigger('keydown', { key: 'ArrowDown' });
    expect(focused()).toBe('nav-storages');
    await p.trigger('keydown', { key: 'Home' });
    expect(focused()).toBe('nav-explore');
    await p.trigger('keydown', { key: 'End' });
    expect(focused()).toBe('nav-storages');
    await p.trigger('keydown', { key: 'ArrowUp' });
    expect(focused()).toBe('nav-trash');
  });

  it('↑ from the first page goes back to the panel’s button, the panel still open', async () => {
    const w = draw();
    await top(w, 'files').trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    await panel(w, 'files').trigger('keydown', { key: 'ArrowUp' });
    expect(focused()).toBe('nav-top-files');
    expect(panel(w, 'files').isVisible()).toBe(true);
  });

  it('Esc closes the panel and gives focus back to its button', async () => {
    const w = draw();
    await top(w, 'files').trigger('keydown', { key: 'ArrowDown' });
    await nextTick();
    await panel(w, 'files').trigger('keydown', { key: 'Escape' });
    expect(panel(w, 'files').isVisible()).toBe(false);
    expect(top(w, 'files').attributes('aria-expanded')).toBe('false');
    expect(focused()).toBe('nav-top-files');
  });

  it('← / → move along the top entries and wrap', async () => {
    const w = draw();
    (w.get('[data-testid="nav-dashboard"]').element as HTMLElement).focus();
    await w.get('[data-testid="nav-dashboard"]').trigger('keydown', { key: 'ArrowRight' });
    expect(focused()).toBe('nav-top-files');
    await top(w, 'files').trigger('keydown', { key: 'ArrowRight' });
    expect(focused()).toBe('nav-top-people');
    await top(w, 'people').trigger('keydown', { key: 'ArrowRight' });
    expect(focused()).toBe('nav-dashboard');
    await w.get('[data-testid="nav-dashboard"]').trigger('keydown', { key: 'ArrowLeft' });
    expect(focused()).toBe('nav-top-people');
  });

  it('mirrors ← / → in a right-to-left interface', async () => {
    const w = draw({}, 'rtl');
    await w.get('[data-testid="nav-dashboard"]').trigger('keydown', { key: 'ArrowLeft' });
    expect(focused()).toBe('nav-top-files');
  });
});

describe('MegaMenu - a click on a page', () => {
  it('a plain click is the host’s navigation: no page load, the panel closes', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    w.get('[data-testid="nav-trash"]').element.dispatchEvent(ev);
    await nextTick();
    expect(ev.defaultPrevented).toBe(true);
    expect(w.emitted('navigate')).toEqual([[{ id: 'trash', href: '/admin/trash' }]]);
    expect(panel(w, 'files').isVisible()).toBe(false);
  });

  it('a click with a modifier is the browser’s (a new tab): nothing is taken over', async () => {
    const w = draw();
    await top(w, 'files').trigger('click');
    // What the menu did is read as the click bubbles past it; the test then
    // stops the browser's own default so happy-dom does not try to load a page.
    let preventedByMenu: boolean | null = null;
    const watch = (e: Event) => {
      preventedByMenu = e.defaultPrevented;
      e.preventDefault();
    };
    document.addEventListener('click', watch);
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: true });
    w.get('[data-testid="nav-trash"]').element.dispatchEvent(ev);
    document.removeEventListener('click', watch);
    expect(preventedByMenu).toBe(false);
    expect(w.emitted('navigate')).toBeUndefined();
  });

  it('the dashboard link navigates the same way', async () => {
    const w = draw();
    await w.get('[data-testid="nav-dashboard"]').trigger('click', { button: 0 });
    expect(w.emitted('navigate')).toEqual([[{ id: 'dashboard', href: '/admin/dashboard' }]]);
  });
});

describe('MegaMenu - what is not drawn', () => {
  it('leaves out a section with no page and an entry with no section', () => {
    const w = draw();
    expect(w.find('[data-testid="nav-group-apps"]').exists()).toBe(false);
    expect(w.find('[data-testid="nav-top-system"]').exists()).toBe(false);
    expect(w.text()).not.toContain('System');
  });

  it('pruneMegaMenu keeps a plain-link entry and drops what emptied', () => {
    const out = pruneMegaMenu(entries());
    expect(out.map((e) => e.id)).toEqual(['dashboard', 'files', 'people']);
    expect(out[1].sections!.map((s) => s.id)).toEqual(['files', 'storage']);
  });
});

describe('MegaMenu (list) - the phone’s drawer', () => {
  it('draws every section open, with no buttons to press', () => {
    const w = draw({ mode: 'list' });
    expect(w.findAll('button')).toHaveLength(0);
    expect(w.findAll('[aria-expanded]')).toHaveLength(0);
    for (const id of ['explore', 'trash', 'storages', 'users', 'dashboard']) {
      expect(w.get(`[data-testid="nav-${id}"]`).isVisible(), id).toBe(true);
    }
  });

  it('heads each entry and each section, and labels the lists by them', () => {
    const w = draw({ mode: 'list' });
    const group = w.get('[data-testid="nav-top-files"]');
    expect(document.getElementById(group.attributes('aria-labelledby')!)?.textContent).toBe('Files & storage');
    const list = group.get('[data-testid="nav-group-storage"] ul');
    expect(document.getElementById(list.attributes('aria-labelledby')!)?.textContent).toBe('Storage');
  });

  it('leaves the short lines out, and still marks the page being looked at', () => {
    const w = draw({ mode: 'list' });
    expect(w.findAll('.fx-mega__hint')).toHaveLength(0);
    expect(w.get('[data-testid="nav-storages"]').attributes('aria-current')).toBe('page');
  });
});

describe('the step rules', () => {
  it('stepFocus: down, up, home, end; no wrap; other keys are not its', () => {
    expect(stepFocus('ArrowDown', 0, 3)).toBe(1);
    expect(stepFocus('ArrowDown', 2, 3)).toBe(2);
    expect(stepFocus('ArrowUp', 0, 3)).toBe(0);
    expect(stepFocus('Home', 2, 3)).toBe(0);
    expect(stepFocus('End', 0, 3)).toBe(2);
    expect(stepFocus('Tab', 0, 3)).toBeNull();
  });

  it('stepAlongBar: wraps, and the arrows follow the direction of the line', () => {
    expect(stepAlongBar('ArrowRight', 2, 3, 'ltr')).toBe(0);
    expect(stepAlongBar('ArrowLeft', 0, 3, 'ltr')).toBe(2);
    expect(stepAlongBar('ArrowLeft', 0, 3, 'rtl')).toBe(1);
    expect(stepAlongBar('ArrowDown', 0, 3, 'ltr')).toBeNull();
  });
});
