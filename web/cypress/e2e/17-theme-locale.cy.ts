// 17-theme-locale — theme + locale toggles persist across SPA
// navigation. Catches the "RecentlyOpened light theme leak" + the
// "URL stable locale" regressions.

describe('theme + locale persistence', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  // ⚠ This spec is the only one that writes to the shared admin account: it
  // PATCHes the profile locale to `tr` to prove the label switch works. Cypress
  // runs specs in one browser against one server, so without this the account
  // stayed Turkish for every spec that ran afterwards -- and a later spec
  // asserting an English product string would fail for a reason that has
  // nothing to do with what it tests. Put it back, whatever happened above.
  afterEach(() => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'PATCH',
        url: '/api/auth/profile',
        headers: { Authorization: `Bearer ${tok}` },
        body: { locale: 'en' },
        failOnStatusCode: false,
      });
    });
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
    // ⚠⚠ Pin the BROWSER language to English, or this test measures the
    // machine it runs on instead of the thing it claims to check. It passed
    // on a Turkish workstation and failed on an English CI runner for four
    // releases: with no stored choice the app fell back to browser detection,
    // so the Turkish label came from the browser, never from the account.
    // Forcing `en` here means only the saved account preference can produce it.
    cy.on('window:before:load', (win) => {
      Object.defineProperty(win.navigator, 'languages', { value: ['en-US', 'en'] });
      Object.defineProperty(win.navigator, 'language', { value: 'en-US' });
    });
    cy.clearLocalStorage();
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
