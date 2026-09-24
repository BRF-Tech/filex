// Two small surfaces from the release-candidate sweep of 2026-09-21.
//
// THE TOAST LAYER. The panel's dialogs are native <dialog>s opened with
// showModal(), which puts them in the browser's top layer; a toast in the
// ordinary page — whatever its z-index — was drawn UNDER the dialog's
// backdrop, so "email required" after an empty Add user was a toast nobody
// could read. The layer is a manual popover now (top layer too), and top-layer
// order is SHOW order, so it is re-shown whenever a toast arrives.
//
// THE SELF-SERVICE KEY FORM (core TokensPanel, `full`). It used to paper over
// nothing ticked with `|| 'read'` — a silent default, the very thing the
// owner's rule removes: no door mints a token without an explicit list.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';

import ToastContainer from '@/components/ToastContainer.vue';
import { useToastStore } from '@/stores/toast';
import TokensPanel from '@brftech/filex-core/src/components/TokensPanel.vue';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr as coreTr } from '@brftech/filex-core/src/locales/tr';

describe('the toast layer', () => {
  const shown: string[] = [];
  let open = false;
  beforeEach(() => {
    setActivePinia(createPinia());
    shown.length = 0;
    open = false;
    // happy-dom has no Popover API; these stand in for the browser's.
    Object.assign(HTMLElement.prototype, {
      showPopover(this: HTMLElement) {
        shown.push('show');
        open = true;
      },
      hidePopover(this: HTMLElement) {
        shown.push('hide');
        open = false;
      },
    });
    const matches = Element.prototype.matches;
    vi.spyOn(Element.prototype, 'matches').mockImplementation(function (this: Element, sel: string) {
      if (sel === ':popover-open') return open;
      return matches.call(this, sel);
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
    delete (HTMLElement.prototype as Partial<{ showPopover: unknown }>).showPopover;
    delete (HTMLElement.prototype as Partial<{ hidePopover: unknown }>).hidePopover;
  });

  it('is a manual popover, shown when the panel starts', async () => {
    const w = mount(ToastContainer, { attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="toast-layer"]').attributes('popover')).toBe('manual');
    expect(shown).toEqual(['show']);
  });

  it('rises again above a dialog opened since, each time a toast arrives', async () => {
    mount(ToastContainer, { attachTo: document.body });
    await flushPromises();
    shown.length = 0;

    useToastStore().error('Enter an email address.');
    await flushPromises();
    // Hide then show: the only way to move a top-layer element to the front.
    expect(shown).toEqual(['hide', 'show']);

    // A toast going away is no reason to move.
    shown.length = 0;
    useToastStore().clear();
    await flushPromises();
    expect(shown).toEqual([]);
  });
});

describe('the self-service key form', () => {
  const CONFIG = { apiBase: '', locale: 'en', endpoint: '/api/files/manager' } as never;
  const posted: unknown[] = [];

  beforeEach(() => {
    posted.length = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'POST') posted.push(JSON.parse(String(init.body)));
        const body = init?.method === 'POST' ? { id: 1, token: 'fx_secret', label: 'x', scopes: 'read' } : { tokens: [] };
        return {
          ok: true,
          status: 200,
          headers: { get: () => 'application/json' },
          json: async () => body,
          text: async () => JSON.stringify(body),
        };
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('offers no Create while nothing is ticked, says why, and sends nothing', async () => {
    const w = mount(TokensPanel, { props: { config: CONFIG, full: true }, attachTo: document.body });
    await flushPromises();
    await flushPromises();

    await w.find('[data-testid="token-scope-read"]').setValue(false);
    const mint = w.find('[data-testid="token-mint"]');
    expect(mint.attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="token-scopes-required"]').text()).toBe(coreEn['conn.tokens.scopesRequired']);
    await mint.trigger('click');
    await flushPromises();
    expect(posted).toEqual([]);

    // One tick, and exactly that is asked for — no default added.
    await w.find('[data-testid="token-scope-write"]').setValue(true);
    expect(w.find('[data-testid="token-scopes-required"]').exists()).toBe(false);
    await w.find('[data-testid="token-mint"]').trigger('click');
    await flushPromises();
    expect(posted).toHaveLength(1);
    expect((posted[0] as { scopes: string }).scopes).toBe('write');
  });

  it('says it in Turkish too', () => {
    expect(coreTr['conn.tokens.scopesRequired']).toBe('En az bir izin seçin — hiçbir izni olmayan bir API anahtarı verilmez.');
  });
});
