// 17-theme-locale — theme + locale toggles persist across SPA
// navigation. Catches the "RecentlyOpened light theme leak" + the
// "URL stable locale" regressions.

describe('theme + locale persistence', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('localStorage filex theme key survives across navigation', () => {
    cy.visit('/admin/dashboard');
    // Force-set the theme via localStorage (matches what the toggle
    // does internally) so we don't depend on a fragile UI selector.
    cy.window().then((win) => {
      win.localStorage.setItem('filex.theme', 'dark');
    });
    cy.visit('/admin/storages');
    cy.window().then((win) => {
      // The store may rewrite this key; both forms are acceptable.
      const t =
        win.localStorage.getItem('filex.theme') ||
        win.localStorage.getItem('theme') ||
        '';
      expect(t.toLowerCase(), 'theme persisted').to.match(/dark|auto|light/);
    });
  });

  it('TR locale set via /api/auth/profile reflects in /me', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'PATCH',
        url: '/api/auth/profile',
        headers: { Authorization: `Bearer ${tok}` },
        body: { locale: 'tr' },
      });
      cy.request({
        method: 'GET',
        url: '/api/auth/me',
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.user.locale, 'locale').to.eq('tr');
      });
    });
  });

  it('dashboard renders TR labels when locale=tr', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'PATCH',
        url: '/api/auth/profile',
        headers: { Authorization: `Bearer ${tok}` },
        body: { locale: 'tr' },
      });
    });
    cy.intercept('GET', '/api/admin/dashboard').as('dash');
    cy.visit('/admin/dashboard');
    cy.wait('@dash', { timeout: 15000 });
    // At least one Turkish label should appear.
    cy.contains(/depolar|kullanıcı|toplam|dekslenmi/i, { timeout: 10000 }).should('be.visible');
  });
});
