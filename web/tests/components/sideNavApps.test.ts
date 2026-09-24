// SideNav's "Apps" section — the `home` placement. Present with its rows
// when the host hands any over, absent entirely when it hands none (a
// heading over nothing is a promise the deployment cannot keep).
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';

function nav(extra: Record<string, unknown> = {}) {
  return mount(SideNav, {
    props: { expanded: true, activeView: '', storages: [{ name: 'main' }], locale: 'tr', ...extra },
  });
}

describe('SideNav — Apps', () => {
  it('is absent without apps', () => {
    expect(nav().find('[data-testid="sidenav-apps"]').exists()).toBe(false);
    expect(nav({ apps: [] }).find('[data-testid="sidenav-apps"]').exists()).toBe(false);
  });

  it('lists one row per home view, in the viewer\'s language, and opens it', async () => {
    const w = nav({
      apps: [
        { key: 'sign/envelopes', label: 'Zarflar', icon: 'sign' },
        { key: 'conv/queue', label: 'Dönüştürme', icon: 'convert' },
      ],
    });
    const section = w.find('[data-testid="sidenav-apps"]');
    expect(section.exists()).toBe(true);
    expect(section.find('.fe-sidenav__heading').text()).toBe('Uygulamalar');
    const rows = section.findAll('.fe-sidenav__item');
    expect(rows.map((r) => r.text())).toEqual(['Zarflar', 'Dönüştürme']);
    // An unknown icon name falls back to the plugin glyph; a known one is drawn.
    expect(rows[0].find('.fe-sidenav__appicon svg').exists()).toBe(true);
    await rows[1].trigger('click');
    expect(w.emitted('open-app')).toEqual([['conv/queue']]);
  });

  it('keeps the rows on the rail, labels hidden, names in title', () => {
    const w = nav({ expanded: false, apps: [{ key: 'sign/envelopes', label: 'Envelopes' }] });
    const row = w.find('[data-testid="sidenav-app-sign/envelopes"]');
    expect(row.exists()).toBe(true);
    expect(row.find('.fe-sidenav__text').exists()).toBe(false);
    expect(row.attributes('title')).toBe('Envelopes');
  });
});
