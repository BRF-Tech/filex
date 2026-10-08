// 17-theme-locale — theme + locale toggles persist across SPA
// navigation. Catches the "RecentlyOpened light theme leak" + the
// "URL stable locale" regressions.

describe('theme + locale persistence', () => {
  // The admin's web preference document as it was before the theme case
  // changed it; put back after, like the locale below.
  let webPrefsBefore: Record<string, unknown> | null = null;

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
      // The theme case's account write goes first: the PATCH below then
      // leaves the language in every document that holds one (#191).
      if (webPrefsBefore) {
        const prefs = webPrefsBefore;
        webPrefsBefore = null;
        cy.request({
          method: 'PUT',
          url: '/api/me/prefs?surface=web',
          headers: { Authorization: `Bearer ${tok}` },
          body: { prefs },
          failOnStatusCode: false,
        });
      }
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
    // ⚠⚠ Light/dark lives on the ACCOUNT (web/src/lib/theme.ts, prefs v3):
    // the toggle writes `filex.theme` AND the account's web document, and
    // every page load repaints from the account's answer - the key is only
    // the first-paint cache. This case used to write the key alone. Once the
    // account's document holds anything (since #191 it holds the account's
    // language) a load reads "no theme chosen" there and clears the key, so
    // the 0.54 run read '' on the next page. Choose the theme where the
    // toggle keeps it, then prove it survives a navigation.
    cy.apiLogin().then((tok) => {
      const headers = { Authorization: `Bearer ${tok}` };
      cy.request({ method: 'GET', url: '/api/me/prefs?surface=web', headers }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const prefs = { ...((body?.prefs ?? {}) as Record<string, unknown>) };
        webPrefsBefore = { ...prefs };
        cy.request({ method: 'PUT', url: '/api/me/prefs?surface=web', headers, body: { prefs: { ...prefs, theme: 'dark' } } });
      });
    });
    cy.visit('/admin/dashboard');
    cy.get('html').should('have.class', 'dark');
    cy.visit('/admin/storages');
    cy.get('html').should('have.class', 'dark');
    // `.should`, not `.then`: the account's answer lands after the load event.
    cy.window().should((win) => {
      expect(win.localStorage.getItem('filex.theme'), 'theme persisted').to.eq('dark');
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
    // `pathname`, not the URL: the page asks `/api/admin/dashboard?lang=<the
    // screen's language>` since 0.54 (#208), and a plain URL string does not
    // match a request that carries a query.
    cy.intercept({ method: 'GET', pathname: '/api/admin/dashboard' }).as('dash');
    cy.visit('/admin/dashboard');
    cy.wait('@dash', { timeout: 15000 });
    // At least one Turkish label should appear.
    // ⚠ In the page (`main`), not the whole document: since 0.51 the admin
    // menu's panels hold every page name, hidden until a panel opens, and an
    // unscoped cy.contains() finds that hidden menu entry first.
    cy.get('main').contains(/depolar|kullanıcı|toplam|dekslenmi/i, { timeout: 10000 }).should('be.visible');
  });
});
