// Admin → Multi-tenant mode (views/TenancyMode.vue, task #167). What is pinned:
//   1. the switch shows the mode the NEXT start runs with, and a saved change
//      says "restart filex" until the server runs it;
//   2. FILEX_MULTI_TENANT (or the config file) pins it: the switch is locked
//      and says what to change instead; nothing is sent;
//   3. turning it off while tenants exist opens a warning that says how many,
//      what happens to them and that nothing is deleted, and saves only with
//      that number typed - never on the first click;
//   4. in Turkish, with Turkish letters.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { TenancyState } from '@/api/tenancy';

const fx = vi.hoisted(() => ({ state: {} as Record<string, unknown> }));
const api = vi.hoisted(() => ({
  get: vi.fn(async () => ({ ...fx.state })),
  set: vi.fn(async (enabled: boolean) => ({
    ...fx.state,
    saved: enabled,
    next_start: enabled,
    restart_required: enabled !== fx.state.in_force,
  })),
}));
vi.mock('@/api/tenancy', async (orig) => ({ ...(await orig<object>()), TenancyApi: api }));
vi.mock('@/stores/toast', () => ({ useToastStore: () => ({ success: vi.fn(), error: vi.fn() }) }));

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

import TenancyMode from '@/views/TenancyMode.vue';

function state(over: Partial<TenancyState> = {}): TenancyState {
  return {
    in_force: false,
    next_start: false,
    saved: null,
    locked: false,
    restart_required: false,
    tenants: 0,
    ...over,
  };
}

async function mountPage(locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/tenancy', name: 'tenancy', component: TenancyMode },
      { path: '/tenants', name: 'tenants', component: { template: '<div />' } },
    ],
  });
  await router.push('/tenancy');
  await router.isReady();
  const w = mount(TenancyMode, { global: { plugins: [i18n, router] }, attachTo: document.body });
  await flushPromises();
  return w;
}

const switchOf = (w: Awaited<ReturnType<typeof mountPage>>) => w.get('[data-testid="tenancy-switch"] button[role="switch"]');

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('Multi-tenant mode', () => {
  it('off on a single-tenant install; turning it on is saved for the next start and says so', async () => {
    fx.state = state();
    const w = await mountPage();
    expect(switchOf(w).attributes('aria-checked')).toBe('false');
    expect(w.find('[data-testid="tenancy-restart"]').exists()).toBe(false);
    expect(w.find('[data-testid="tenancy-locked"]').exists()).toBe(false);
    expect(w.get('[data-testid="tenancy-in-force"]').text()).toBe(en.tenancy.off);

    await switchOf(w).trigger('click');
    await flushPromises();
    expect(api.set).toHaveBeenCalledWith(true, undefined);
    expect(switchOf(w).attributes('aria-checked')).toBe('true');
    expect(w.get('[data-testid="tenancy-restart"]').text()).toContain(en.tenancy.restartToOn);
    expect(w.get('[data-testid="tenancy-in-force"]').text(), 'the running server is unchanged').toBe(en.tenancy.off);
    w.unmount();
  });

  it('pinned by FILEX_MULTI_TENANT: locked, says where to change it, sends nothing', async () => {
    fx.state = state({ in_force: true, next_start: true, saved: true, locked: true, locked_by: 'environment', variable: 'FILEX_MULTI_TENANT', tenants: 2 });
    const w = await mountPage();
    expect(switchOf(w).attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid="tenancy-locked"]').text()).toContain('FILEX_MULTI_TENANT');
    await switchOf(w).trigger('click');
    await flushPromises();
    expect(api.set).not.toHaveBeenCalled();
    expect(document.querySelector('[data-testid="tenancy-confirm"]')).toBeNull();
    w.unmount();
  });

  it('pinned by the config file: says so', async () => {
    fx.state = state({ locked: true, locked_by: 'config_file', variable: 'multi_tenant' });
    const w = await mountPage();
    expect(w.get('[data-testid="tenancy-locked"]').text()).toContain('multi_tenant');
    expect(w.get('[data-testid="tenancy-locked"]').text()).toContain('configuration file');
    w.unmount();
  });

  it('off with tenants: a warning with their number, saved only with that number typed', async () => {
    fx.state = state({ in_force: true, next_start: true, saved: true, tenants: 2 });
    const w = await mountPage();
    await switchOf(w).trigger('click');
    await flushPromises();
    expect(api.set, 'never on the first click').not.toHaveBeenCalled();

    const dialog = document.querySelector('[data-testid="tenancy-confirm"]') as HTMLElement;
    expect(dialog).not.toBeNull();
    expect(dialog.textContent).toContain('This instance has 2 tenants besides the platform');
    expect(dialog.textContent).toContain(en.tenancy.confirmMaintenance);
    expect(dialog.textContent).toContain(en.tenancy.confirmKept);
    const off = document.querySelector('[data-testid="tenancy-confirm-off"]') as HTMLButtonElement;
    expect(off.disabled).toBe(true);

    const input = document.querySelector('[data-testid="tenancy-confirm-input"] input') as HTMLInputElement;
    input.value = '3';
    input.dispatchEvent(new Event('input'));
    await flushPromises();
    expect(off.disabled, 'a wrong number does not open it').toBe(true);
    input.value = '2';
    input.dispatchEvent(new Event('input'));
    await flushPromises();
    expect(off.disabled).toBe(false);
    off.click();
    await flushPromises();
    expect(api.set).toHaveBeenCalledWith(false, '2');
    expect(w.get('[data-testid="tenancy-restart"]').text()).toContain(en.tenancy.restartToOff);
    w.unmount();
  });

  it('off with no tenants: no warning, saved at once', async () => {
    fx.state = state({ in_force: true, next_start: true, saved: true, tenants: 0 });
    const w = await mountPage();
    await switchOf(w).trigger('click');
    await flushPromises();
    expect(document.querySelector('[data-testid="tenancy-confirm"]')).toBeNull();
    expect(api.set).toHaveBeenCalledWith(false, undefined);
    w.unmount();
  });

  it('speaks Turkish with Turkish letters', async () => {
    fx.state = state({ in_force: true, next_start: true, saved: true, tenants: 1 });
    const w = await mountPage('tr');
    expect(w.text()).toContain('Çok kiracılı mod');
    await switchOf(w).trigger('click');
    await flushPromises();
    const dialog = document.querySelector('[data-testid="tenancy-confirm"]') as HTMLElement;
    expect(dialog.textContent).toContain('platformun kendi kiracısı dışında 1 kiracı var');
    expect(dialog.textContent).toContain('Hiçbir şey silinmez');
    w.unmount();
  });
});
