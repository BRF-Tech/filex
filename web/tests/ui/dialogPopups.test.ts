// A popup opened inside a modal <dialog> is drawn INSIDE that dialog.
//
// ⚠⚠ The bug this locks (0.60.0, the app setup wizard's Addresses step on a
// live install): a row's Actions menu opened, its entries were in the DOM -
// and a click on "Remove" was taken by the "Add address" box under it
// (Playwright: "subtree intercepts pointer events"). The wizard is the admin
// panel's Modal, a native <dialog> opened with showModal(): the browser's
// top layer, above every z-index <body> can stack. ContextMenu and the
// table's column menu teleported to <body>, so no layer number could lift
// them over the dialog that opened them. ChoiceSelect already went into the
// open dialog its field sits in; the three now ask one helper
// (lib/popupLayer `popupHost`).
//
// The browser measurement (a click at the entry reaches the entry, Escape
// closes the menu and not the dialog) is e2e/tests/315-app-setup-wizard.spec.ts;
// the overlay case (a z-index, no top layer) stays with
// e2e/tests/128-dialog-row-menu.spec.ts.
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import RowActions from '@brftech/filex-core/src/components/RowActions.vue';
import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import ContextMenu from '@brftech/filex-core/src/components/ContextMenu.vue';
import { popupHost } from '@brftech/filex-core/src/lib/popupLayer';

const mounted: { unmount(): void }[] = [];
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
});

/** An open dialog in <body> with a host for a component inside it. */
function openDialog(): { dialog: HTMLDialogElement; host: HTMLElement } {
  const dialog = document.createElement('dialog');
  dialog.setAttribute('open', '');
  const card = document.createElement('div');
  const host = document.createElement('div');
  card.appendChild(host);
  dialog.appendChild(card);
  document.body.appendChild(dialog);
  return { dialog, host };
}

describe('a popup opened inside an open <dialog>', () => {
  it('popupHost: the open dialog the opener sits in, else <body>', () => {
    const { dialog, host } = openDialog();
    expect(popupHost(host)).toBe(dialog);
    expect(popupHost([null, host])).toBe(dialog);
    const loose = document.createElement('span');
    document.body.appendChild(loose);
    expect(popupHost(loose)).toBe(document.body);
    expect(popupHost(null)).toBe(document.body);
    dialog.removeAttribute('open');
    expect(popupHost(host), 'a closed dialog is not on the top layer').toBe(document.body);
  });

  it('a row’s Actions menu is drawn in the dialog, and its entry is reached there', async () => {
    const { dialog, host } = openDialog();
    const picked: string[] = [];
    const w = mount(RowActions, {
      attachTo: host,
      props: {
        actions: [
          { key: 'off', label: 'Switch off' },
          { key: 'remove', label: 'Remove', danger: true },
        ],
        testid: 'app-setup-base-actions-31',
        onSelect: (key: string) => picked.push(key),
      },
    });
    mounted.push(w);
    await w.get('[data-testid="app-setup-base-actions-31"]').trigger('click');
    await nextTick();
    const backdrop = document.querySelector('.fe-ctx-backdrop');
    expect(backdrop, 'the menu opened').not.toBeNull();
    expect(backdrop!.closest('dialog'), 'the menu is in the dialog, not under it in <body>').toBe(dialog);
    (dialog.querySelector('[data-testid="app-setup-base-actions-31-remove"]') as HTMLElement).click();
    await nextTick();
    expect(picked).toEqual(['remove']);
  });

  it('Escape closes the menu and is not passed on to the dialog', async () => {
    const { dialog, host } = openDialog();
    const w = mount(RowActions, { attachTo: host, props: { actions: [{ key: 'remove', label: 'Remove' }], testid: 'ra' } });
    mounted.push(w);
    await w.get('[data-testid="ra"]').trigger('click');
    await nextTick();
    const backdrop = dialog.querySelector('.fe-ctx-backdrop') as HTMLElement;
    expect(backdrop).not.toBeNull();
    // A native dialog cancels on an Escape whose default is not prevented.
    const esc = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    backdrop.dispatchEvent(esc);
    await nextTick();
    expect(esc.defaultPrevented, 'the dialog would close with the menu').toBe(true);
    expect(dialog.querySelector('.fe-ctx-backdrop')).toBeNull();
  });

  it('the table’s column menu is drawn in the dialog too', async () => {
    const { dialog, host } = openDialog();
    const w = mount(DataTable, {
      attachTo: host,
      props: {
        columns: [
          { id: 'url', label: 'Address', width: 200, lead: true },
          { id: 'state', label: 'State', width: 100 },
        ],
        rows: [{ id: 1, url: 'https://fm.example.com/l', state: 'on' }],
        rowKey: 'id',
        tableId: 'test.dialog-popups',
      },
    });
    mounted.push(w);
    await w.get('.fe-list__colmenu-btn').trigger('click');
    await nextTick();
    const backdrop = document.querySelector('.fe-colmenu__backdrop');
    expect(backdrop, 'the column menu opened').not.toBeNull();
    expect(backdrop!.closest('dialog')).toBe(dialog);
  });

  it('outside a dialog nothing moves: the menu is in <body>', async () => {
    const w = mount(ContextMenu, { attachTo: document.body, props: { locale: 'en' as const, actions: [{ key: 'open', label: 'Open' }] } });
    mounted.push(w);
    await (w.vm as unknown as { show: (e: { clientX: number; clientY: number }, n: unknown[]) => Promise<void> }).show({ clientX: 10, clientY: 10 }, []);
    await nextTick();
    const backdrop = document.querySelector('.fe-ctx-backdrop');
    expect(backdrop).not.toBeNull();
    expect(backdrop!.closest('dialog')).toBeNull();
  });
});
