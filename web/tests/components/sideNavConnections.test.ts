// SideNav's Connections section (task #258, the owner's decision of
// 2026-10-15): the row is itself "Connections", so the section has no
// visible heading of the same word over it - a rule separates it, at every
// width, the way the collapsed rail separates every section. The list keeps
// its name for a screen reader.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';

function nav(extra: Record<string, unknown> = {}) {
  return mount(SideNav, {
    props: { expanded: true, activeView: '', storages: [{ name: 'main' }], locale: 'tr', showConnections: true, ...extra },
  });
}

describe('SideNav - Connections', () => {
  it('has no heading over its row, a rule instead, and the row reads Bağlantılar', () => {
    const section = nav().get('[data-testid="sidenav-connections"]');
    expect(section.find('.fe-sidenav__heading').exists()).toBe(false);
    expect(section.find('hr.fe-sidenav__rule').exists()).toBe(true);
    expect(section.text()).not.toMatch(/^\s*Bağlantılar\s*Bağlantılar/);
    const row = section.get('[data-testid="sidenav-connect"]');
    expect(row.text()).toBe('Bağlantılar');
    expect(row.attributes('aria-label')).toBe('Bağlantılar');
    expect(section.get('ul').attributes('aria-label'), 'named for a screen reader').toBe('Bağlantılar');
  });

  it('is the same in English, and on the collapsed rail', () => {
    const en = nav({ locale: 'en' }).get('[data-testid="sidenav-connections"]');
    expect(en.find('.fe-sidenav__heading').exists()).toBe(false);
    expect(en.get('[data-testid="sidenav-connect"]').text()).toBe('Connections');

    const rail = nav({ expanded: false }).get('[data-testid="sidenav-connections"]');
    expect(rail.find('.fe-sidenav__heading').exists()).toBe(false);
    expect(rail.find('hr.fe-sidenav__rule').exists()).toBe(true);
    expect(rail.get('[data-testid="sidenav-connect"]').attributes('title')).toBe('Bağlantılar');
  });

  it('is not drawn when the host turns the connections off', () => {
    expect(nav({ showConnections: false }).find('[data-testid="sidenav-connections"]').exists()).toBe(false);
  });
});
