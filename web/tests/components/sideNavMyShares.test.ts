// paylas:m1 — the way IN to "Paylaştıklarım" / "My shares".
//
// The screen itself is measured by `mySharesRow.test.ts`; this file is about
// the door. It existed only as a row in the account menu behind the avatar,
// which is where a person looks for *settings*, not for a link they handed
// out. The explorer's navigation already answers the mirror question one row
// above — "Shared with me", what other people handed to me — so the outgoing
// half belongs directly under it.
//
// What has to stay true, in the order the assertions run:
//
//   · the entry is IN the views group and is the row right after
//     "Shared with me" — beside its mirror, not appended under Trash and not
//     buried in the Connections corner, which is one-time setup;
//   · it is labelled in the viewer's language;
//   · pressing it ANNOUNCES the intent (`open-my-shares`) and does not move
//     the listing — the screen is a host route, not a view this panel owns;
//   · an app token, which is not a person and has shared nothing, sees
//     neither this row nor its mirror;
//   · collapsed to the icon rail the row survives with its name in `title`,
//     because the rail's promise is that no destination goes away;
//   · and the announcement reaches a real screen. ⚠ The last one is a source
//     scan on purpose: the emit only becomes navigation two components up
//     (FileExplorer forwards it, Explore.vue pushes the route), FileExplorer
//     is far too large to mount here, and if either link is dropped the row
//     stays on screen and silently does NOTHING — the exact failure a unit
//     test of the button alone cannot see.
//
// ⚠⚠ And the half that came after: the row is the FIRST entry in this panel
// whose destination lives in the HOST, so drawing it unasked is drawing a
// button that does nothing. In `<filex-explorer>` on work.example.com, in the
// fishapp and on fm.example.com nothing listens for `open-my-shares`. Hence
// `ExplorerConfig.mySharesVisible` — default OFF, `true` only from
// `web/src/views/Explore.vue`, which owns the `my-shares` route. Two more
// things have to stay true:
//
//   · a host that has not opted in gets NO row (and still gets the rest of
//     the panel — the gate is one row, not the group);
//   · the one screen with no explorer at all, Explore.vue's "no storages / no
//     access" fallback, keeps a door of its own. There is no navigation panel
//     there to carry one, and an account with live public links and no drive
//     would otherwise have no way left to see or revoke them.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import SideNav from '@brftech/filex-core/src/components/SideNav.vue';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');
const SRC = path.resolve(__dirname, '../../src');

/**
 * ⚠ `showIdentitySurfaces` is passed EXPLICITLY, and it is not decoration:
 * Vue casts an absent Boolean prop to `false`, not `undefined`, so a mount
 * that leaves it out renders the panel as it looks to an APP TOKEN — no
 * "Shared with me", no Recent, no Starred and therefore nothing for this row
 * to sit under. The host always passes it (`FileExplorer.vue`,
 * `:show-identity-surfaces="identitySurfaces"`); a test that does not would
 * be measuring a panel no person ever sees.
 */
function nav(extra: Record<string, unknown> = {}) {
  return mount(SideNav, {
    props: {
      expanded: true,
      activeView: '',
      storages: [{ name: 'main' }],
      locale: 'tr',
      showIdentitySurfaces: true,
      // ⚠ And `showMyShares` for the same reason with the opposite sense: it
      // is the gate a HOST opens, and the SPA opens it. The default here is
      // therefore `true` — these assertions describe the panel a person on
      // /explore sees. The host that says nothing is measured on its own,
      // further down.
      showMyShares: true,
      ...extra,
    },
  });
}

/** The views group's rows, in the order they are drawn. */
function viewOrder(w: ReturnType<typeof nav>): string[] {
  return w
    .findAll('[data-testid^="sidenav-view-"], [data-testid="sidenav-my-shares"]')
    .map((r) => r.attributes('data-testid') as string);
}

describe('SideNav — My shares', () => {
  it('draws the row immediately under "Shared with me"', () => {
    const order = viewOrder(nav());
    const shared = order.indexOf('sidenav-view-shared');
    expect(shared).toBeGreaterThanOrEqual(0);
    expect(order[shared + 1]).toBe('sidenav-my-shares');
    // ⚠ And NOT in the Connections group, where it first landed: that section
    // is "how to connect" + API keys, visited once.
    expect(order.includes('sidenav-my-shares')).toBe(true);
  });

  it('is labelled in the viewer’s language', () => {
    expect(nav().find('[data-testid="sidenav-my-shares"]').text()).toBe('Paylaştıklarım');
    expect(nav({ locale: 'en' }).find('[data-testid="sidenav-my-shares"]').text()).toBe(
      'My shares',
    );
  });

  it('announces the intent when pressed, and moves no listing', async () => {
    const w = nav();
    await w.find('[data-testid="sidenav-my-shares"]').trigger('click');
    expect(w.emitted('open-my-shares')).toEqual([[]]);
    // The row leaves the explorer; it must not also ask it to change view.
    expect(w.emitted('open-view')).toBeUndefined();
  });

  it('goes with its mirror for an app token', () => {
    const w = nav({ showIdentitySurfaces: false });
    expect(w.find('[data-testid="sidenav-view-shared"]').exists()).toBe(false);
    expect(w.find('[data-testid="sidenav-my-shares"]').exists()).toBe(false);
  });

  it('survives the icon rail with its name in the title', () => {
    const row = nav({ expanded: false }).find('[data-testid="sidenav-my-shares"]');
    expect(row.exists()).toBe(true);
    expect(row.find('.fe-sidenav__text').exists()).toBe(false);
    expect(row.attributes('title')).toBe('Paylaştıklarım');
  });

  it('has the intent wired all the way to a screen', () => {
    // 1. the explorer passes it through rather than swallowing it,
    const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
    expect(explorer).toMatch(/\(e: 'open-my-shares'\): void;/);
    expect(explorer).toMatch(/@open-my-shares="emit\('open-my-shares'\)/);
    // 2. the page that hosts it turns the intent into navigation,
    const explore = readFileSync(path.join(SRC, 'views/Explore.vue'), 'utf8');
    expect(explore).toMatch(/@open-my-shares="router\.push\(\{ name: 'my-shares' \}\)/);
    // 3. and that name is a route, not a typo nobody would ever see fail.
    const router = readFileSync(path.join(SRC, 'router/index.ts'), 'utf8');
    expect(router).toMatch(/path: '\/my-shares',\s*\r?\n\s*name: 'my-shares',/);
  });

  it('is the only door beside the panel — the header menu carries no copy', () => {
    // Two rows for one screen, a glyph apart in the same header, is the
    // duplicate this pass removes. The navigation row is the survivor.
    //
    // ⚠ Scoped to `headerActions` on purpose: the list the EMPTY screen draws
    // does carry the row (there is no panel there to carry it), and a blunt
    // "the file never says my-shares" would forbid that too.
    const explore = readFileSync(path.join(SRC, 'views/Explore.vue'), 'utf8');
    const from = explore.indexOf('const headerActions');
    const to = explore.indexOf('const emptyStateActions');
    expect(from).toBeGreaterThan(-1);
    expect(to).toBeGreaterThan(from);
    expect(explore.slice(from, to)).not.toContain("key: 'my-shares'");
  });
});

/**
 * The gate. ⚠ Not a preference — the row's screen is the HOST's, so a panel
 * that draws it for a host which never listens has drawn a button that does
 * nothing. That host is the common case: `<filex-explorer>` on work.example.com,
 * in the fishapp and on fm.example.com.
 */
describe('SideNav — My shares is the host’s to ask for', () => {
  it('is absent when the host says nothing, and takes nothing else with it', () => {
    // ⚠ `showMyShares` left out entirely — an embed that never heard of the
    // flag, which is exactly the host this gate is for.
    const w = mount(SideNav, {
      props: {
        expanded: true,
        activeView: '',
        storages: [{ name: 'main' }],
        locale: 'tr',
        showIdentitySurfaces: true,
      },
    });
    expect(w.find('[data-testid="sidenav-my-shares"]').exists()).toBe(false);
    // The gate is ONE row: its mirror and the rest of the group stay. A gate
    // that took "Shared with me" with it would be a regression dressed as a
    // fix — the embed's users still want what was handed TO them.
    expect(w.find('[data-testid="sidenav-view-shared"]').exists()).toBe(true);
    expect(w.find('[data-testid="sidenav-view-recent"]').exists()).toBe(true);
  });

  it('appears once the host asks', () => {
    expect(nav({ showMyShares: true }).find('[data-testid="sidenav-my-shares"]').exists()).toBe(true);
  });

  it('is threaded from ExplorerConfig, default off, and only this SPA opts in', () => {
    // 1. the flag is declared where a host reads the contract,
    const config = readFileSync(path.join(CORE_SRC, 'types/ExplorerConfig.ts'), 'utf8');
    expect(config).toMatch(/mySharesVisible\?: boolean;/);
    // 2. the explorer reads it as "yes only if asked" — `?? true` or a
    //    `!== false` here would hand every embed the dead row back,
    const explorer = readFileSync(path.join(CORE_SRC, 'FileExplorer.vue'), 'utf8');
    expect(explorer).toMatch(/const mySharesEnabled = computed\(\(\) => props\.config\.mySharesVisible === true\);/);
    // 3. and hands it to the panel,
    expect(explorer).toMatch(/:show-my-shares="mySharesEnabled/);
    // 4. while the host that DOES own the screen is the one that opts in.
    const explore = readFileSync(path.join(SRC, 'views/Explore.vue'), 'utf8');
    expect(explore).toMatch(/mySharesVisible: true,/);
  });
});

/**
 * The screen with no explorer on it.
 *
 * Explore.vue draws a second account menu under "no storages / no access",
 * where there is no explorer and therefore no navigation panel. The row that
 * used to live in that menu was removed in favour of the navigation one — so
 * this screen, and only this screen, lost its last door: an account whose
 * grant was revoked keeps its public links alive and can no longer reach the
 * page that lists or revokes them.
 *
 * ⚠ A source scan, for the same reason as the wiring test above: mounting
 * Explore.vue means mounting FileExplorer, the storage discovery it runs on
 * mount and the whole router — and the thing under test is which of two
 * lists one `<AccountMenu>` is handed.
 */
describe('Explore — the empty screen keeps its own door', () => {
  const explore = readFileSync(path.join(SRC, 'views/Explore.vue'), 'utf8');

  it('the fallback menu is handed the list that has the row', () => {
    // The fallback block, identified by the testid that marks it, cut at the
    // end of ITS `<AccountMenu>` tag — the page draws a second one lower down
    // (inside the explorer's header) and that one must keep `headerActions`.
    const block = explore.slice(explore.indexOf('data-testid="explore-empty-actions"'));
    const open = block.indexOf('<AccountMenu');
    expect(open).toBeGreaterThan(-1);
    const menu = block.slice(open, block.indexOf('/>', open));
    expect(menu).toContain(':actions="emptyStateActions"');
    expect(menu).not.toContain(':actions="headerActions"');
    // …and the copy inside the explorer is still the clean one.
    const header = explore.slice(explore.lastIndexOf('<AccountMenu'));
    expect(header.slice(0, header.indexOf('/>'))).toContain(':actions="headerActions"');
  });

  it('that list is the header’s rows plus this one, not a second menu', () => {
    const from = explore.indexOf('const emptyStateActions');
    const to = explore.indexOf('function runAccountAction');
    expect(from).toBeGreaterThan(-1);
    const decl = explore.slice(from, to);
    // Built FROM the shared definition — a hand-written second list is how
    // the two copies start to disagree about Sign out.
    expect(decl).toMatch(/\[\.\.\.headerActions\.value\]/);
    expect(decl).toContain("key: 'my-shares'");
    // Named in the viewer's language, from the string the screen itself uses.
    expect(decl).toContain("t('myShares.title')");
  });

  it('and pressing it lands on the same screen the panel’s row does', () => {
    const from = explore.indexOf('function runAccountAction');
    const handler = explore.slice(from, explore.indexOf('const remountKey'));
    expect(handler).toMatch(/key === 'my-shares'\) void router\.push\(\{ name: 'my-shares' \}\)/);
  });
});
