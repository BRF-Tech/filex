// 13-navigation-ui — the admin menu navigates the SPA. Catches router-link
// href regressions.
//
// Since 0.51 (GitHub #82) the menu is a mega menu: the dashboard is a plain
// link and every other page sits in one of three panels (Files & storage,
// People & security, System), opened by its entry's button. Each case opens
// the panel the way a person would, then presses the page's link, found by
// its test id (the route name) rather than its words.
//
// ⚠ The old sidebar was an `overflow-y-auto` column and Cypress calls an
// element clipped by a scrolling ancestor hidden (measured 2026-09-05: seven
// of nine cases died in the hook). A panel shows its pages whole, so the
// scroll-into-view dance that column needed is gone with it.

describe('admin menu navigation', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/admin/dashboard');
  });

  const navTargets: Array<{ entry: string; page: string; url: string }> = [
    { entry: 'files', page: 'storages', url: '/admin/storages' },
    { entry: 'people', page: 'users', url: '/admin/users' },
    { entry: 'system', page: 'settings', url: '/admin/settings' },
    { entry: 'system', page: 'audit', url: '/admin/audit' },
    { entry: 'files', page: 'sync', url: '/admin/sync' },
    { entry: 'files', page: 'shares', url: '/admin/shares' },
    { entry: 'files', page: 'trash', url: '/admin/trash' },
    { entry: 'files', page: 'replica', url: '/admin/replica' },
    { entry: 'system', page: 'queue', url: '/admin/queue' },
  ];

  for (const t of navTargets) {
    it(`opens "${t.entry}" and clicks "${t.page}" → lands on ${t.url}`, () => {
      cy.get(`[data-testid="nav-top-${t.entry}"]`, { timeout: 10000 }).click();
      cy.get(`[data-testid="nav-panel-${t.entry}"] [data-testid="nav-${t.page}"]`).should('be.visible').click();
      cy.url({ timeout: 10000 }).should('include', t.url);
    });
  }
});
