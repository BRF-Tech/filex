// #162 - the way IN to the store screen: the navigation panel's "App store"
// row, under "Apps".
//
// What has to stay true:
//   · the row is drawn only when the explorer says so (`showAppStore`), and
//     the explorer has ONE rule for every host (core lib/appStoreRow,
//     appStoreRow.test.ts): the host has the screen (`config.appStorePage`),
//     the caller is a person, and the server shows them the screen - a host
//     that says nothing gets no row and the rest of the panel unchanged;
//   · it is labelled in the viewer's language, survives the icon rail with its
//     name in `title`, and pressing it ANNOUNCES the intent (`open-app-store`)
//     without moving the listing;
//   · the announcement reaches a real screen: FileExplorer forwards it, so
//     does `<filex-explorer>` (the desktop app listens), Explore.vue pushes
//     the `app-store` route, and that route exists OUTSIDE the admin-only
//     block (a source scan, as for My shares: FileExplorer is too large to
//     mount here, and a dropped link leaves a row that does nothing);
//   · no host decides visibility itself: the SPA and the desktop only say
//     they have the page (desktop/test/store-window.test.ts holds its half).
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');
const SRC = path.resolve(__dirname, '../../src');

function nav(extra: Record<string, unknown> = {}) {
  return mount(SideNav, {
    props: {
      expanded: true,
      activeView: '',
      storages: [{ name: 'main' }],
      locale: 'tr',
      showIdentitySurfaces: true,
      showAppStore: true,
      ...extra,
    },
  });
}

describe('SideNav - App store', () => {
  it('is drawn under "Apps", in the viewer’s language, even with no app home view', () => {
    const w = nav();
    const section = w.find('[data-testid="sidenav-apps"]');
    expect(section.exists()).toBe(true);
    expect(section.find('[data-testid="sidenav-app-store"]').text()).toBe('Uygulama mağazası');
    expect(nav({ locale: 'en' }).find('[data-testid="sidenav-app-store"]').text()).toBe('App store');
  });

  it('comes after the apps a person has', () => {
    const w = nav({ apps: [{ key: 'sign/home', label: 'Signatures', icon: 'plugin' }] });
    const rows = w.findAll('[data-testid="sidenav-apps"] button').map((b) => b.attributes('data-testid'));
    expect(rows).toEqual(['sidenav-app-sign/home', 'sidenav-app-store']);
  });

  it('announces the intent when pressed, and moves no listing', async () => {
    const w = nav();
    await w.find('[data-testid="sidenav-app-store"]').trigger('click');
    expect(w.emitted('open-app-store')).toEqual([[]]);
    expect(w.emitted('open-view')).toBeUndefined();
    expect(w.emitted('open-app')).toBeUndefined();
  });

  it('survives the icon rail with its name in the title', () => {
    const row = nav({ expanded: false }).find('[data-testid="sidenav-app-store"]');
    expect(row.exists()).toBe(true);
    expect(row.find('.fe-sidenav__text').exists()).toBe(false);
    expect(row.attributes('title')).toBe('Uygulama mağazası');
  });

  it('is absent when the host says nothing, and with it the empty Apps heading', () => {
    const w = nav({ showAppStore: undefined });
    expect(w.find('[data-testid="sidenav-app-store"]').exists()).toBe(false);
    expect(w.find('[data-testid="sidenav-apps"]').exists()).toBe(false);
  });

  it('has the intent wired all the way to a screen outside the admin panel', () => {
    const config = readFileSync(path.join(CORE_SRC, 'types/ExplorerConfig.ts'), 'utf8');
    expect(config).toMatch(/appStorePage\?: boolean;/);
    expect(config).not.toMatch(/appStoreVisible\?:/);
    const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
    expect(explorer).toMatch(/\(e: 'open-app-store'\): void;/);
    expect(explorer).toMatch(/@open-app-store="emit\('open-app-store'\)"/);
    // The ONE rule: the host's page, a person, the server's answer - asked by
    // the explorer itself.
    expect(explorer).toMatch(/import \{ appStoreAsks, appStoreRowShown \} from '\.\/lib\/appStoreRow';/);
    expect(explorer).toMatch(/await api\.appStoreStatus\(\)/);
    expect(explorer).toMatch(/:show-app-store="appStoreEnabled/);
    // The web component forwards it (the desktop app listens to it).
    const wc = readFileSync(path.join(CORE_SRC, '../../webcomponent/src/index.ts'), 'utf8');
    expect(wc).toMatch(/'open-app-store',/);
    expect(wc).toMatch(/onOpenAppStore: \(\) => emit\('open-app-store'\)/);
    const explore = readFileSync(path.join(SRC, 'views/Explore.vue'), 'utf8');
    expect(explore).toMatch(/@open-app-store="router\.push\(\{ name: 'app-store' \}\)/);
    // The SPA only says it has the page; it asks the server nothing itself.
    expect(explore).toMatch(/appStorePage: true,/);
    expect(explore).not.toMatch(/StoreScreenApi|\/app-store'\)|appStoreVisible/);
    const router = readFileSync(path.join(SRC, 'router/index.ts'), 'utf8');
    expect(router).toMatch(/path: '\/app-store',\s*\r?\n\s*name: 'app-store',/);
    // Outside the AdminLayout block: declared before it.
    expect(router.indexOf("name: 'app-store'")).toBeLessThan(router.indexOf('component: AdminLayout'));
  });
});
