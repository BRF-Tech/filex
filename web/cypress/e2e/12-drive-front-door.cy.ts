// 12-drive-front-door — /drive, the end-user front door (v0.30.0, GitHub #14).
//
// The same SPA bundle is served from two prefixes: /admin/ for the operator and
// /drive/ for everyone else. What makes that more than a cosmetic alias is that
// vue-router picks its history base from whichever prefix served the document
// (web/src/router/index.ts). Get that wrong and the address bar silently
// rewrites itself back to /admin/… the moment the router hydrates — which is
// literally the complaint #14 was filed about, and it leaves no trace in any
// unit test because the mount base is read from `window.location`.
//
// Every case below fails loudly if a piece of that wiring is removed:
//   - drop `r.Handle(UserUIPrefix, …)` in routes.go → the bare /drive 404s
//   - drop the userSPA mount               → /drive/ 404s
//   - break `mountBase` in router/index.ts → the URL flips to /admin/explore
//   - gate SideNav on uiProfile            → the panel disappears for users

describe('/drive front door', () => {
  it('the bare /drive redirects to /drive/', () => {
    cy.request({ url: '/drive', followRedirect: false }).then((res) => {
      expect(res.status, 'status').to.eq(301);
      expect(res.redirectedToUrl, 'Location').to.match(/\/drive\/$/);
    });
  });

  it('/drive/ serves the SPA document, not a 404', () => {
    cy.request('/drive/').then((res) => {
      expect(res.status).to.eq(200);
      expect(res.headers['content-type']).to.match(/text\/html/);
      // Same bundle as /admin/: assets are absolute (/admin/assets/…) because
      // Vite's build base stays /admin/, so one index.html works from both.
      expect(res.body, 'document has the SPA mount point').to.match(/<div id="app"/);
    });
  });

  it('/drive/ and /admin/ serve the same document', () => {
    cy.request('/drive/').then((user) => {
      cy.request('/admin/').then((admin) => {
        expect(user.body, 'one bundle, two prefixes').to.eq(admin.body);
      });
    });
  });

  it('a signed-in visitor lands on the explorer and STAYS under /drive/', () => {
    cy.apiLogin();
    cy.visit('/drive/');
    // The router's root redirect resolves to `explore` on the user base.
    cy.url({ timeout: 15000 }).should('include', '/drive/');
    cy.url().should('not.include', '/admin/');
    cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('be.visible');
  });

  it('a deep link into /drive/explore renders the file explorer', () => {
    cy.apiLogin();
    cy.visit('/drive/explore');
    cy.url({ timeout: 15000 }).should('include', '/drive/explore');
    cy.get('.fe', { timeout: 20000 }).should('exist');
    // The navigation panel ships in BOTH profiles — gating it on role is the
    // "one product becomes two" split the package exists to prevent.
    cy.get('[data-testid="sidenav"]').should('be.visible');
  });

  it('an unknown /drive/ URL falls back to the SPA instead of 404ing', () => {
    // SPA fallback: any /drive/* that is not a real file has to return
    // index.html, or a refresh on a client-side route breaks the app.
    cy.request('/drive/no-such-route').then((res) => {
      expect(res.status).to.eq(200);
      expect(res.headers['content-type']).to.match(/text\/html/);
    });
  });
});
