/// <reference types="cypress" />

// ─── Cypress custom commands ────────────────────────────────────
//
// Keep these narrow: helpers operators actually reuse across specs
// (login, API token grab, admin fetch wrapper). Per-test setup
// belongs in the spec itself.

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace Cypress {
    interface Chainable {
      /** Logs in via the API and stashes the bearer in sessionStorage.
       *  Fast path — skips the login form. */
      apiLogin(email?: string, password?: string): Chainable<string>;
      /** Logs in via the visible form. Slow path — exercises the
       *  Login.vue happy path. */
      uiLogin(email?: string, password?: string): Chainable<void>;
      /** Authenticated GET. Returns the parsed JSON body. */
      adminGet<T = unknown>(path: string): Chainable<T>;
    }
  }
}

const DEFAULT_EMAIL = () => Cypress.env('ADMIN_EMAIL') as string;
const DEFAULT_PASSWORD = () => Cypress.env('ADMIN_PASSWORD') as string;

Cypress.Commands.add('apiLogin', (email, password) => {
  const e = email ?? DEFAULT_EMAIL();
  const p = password ?? DEFAULT_PASSWORD();
  if (!p) {
    throw new Error('CYPRESS_ADMIN_PASSWORD env var not set. See memory/filex_admin_creds.md.');
  }
  return cy
    .request({
      method: 'POST',
      url: '/api/auth/login',
      body: { email: e, password: p },
    })
    .then((res) => {
      expect(res.status, 'login status').to.eq(200);
      // cy.request occasionally hands back raw text for application/
      // json responses depending on edge-cdn caching — parse defensively.
      const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
      const tok = body.token as string;
      expect(tok, 'login bearer token').to.be.a('string').and.have.length.greaterThan(20);
      window.sessionStorage.setItem('filex.bearer', tok);
      return cy.wrap(tok, { log: false });
    });
});

Cypress.Commands.add('uiLogin', (email, password) => {
  const e = email ?? DEFAULT_EMAIL();
  const p = password ?? DEFAULT_PASSWORD();
  cy.visit('/admin/login');
  cy.get('input[type="email"], input[name="email"]').first().clear().type(e);
  cy.get('input[type="password"], input[name="password"]').first().clear().type(p);
  cy.contains('button', /sign in|giriş|giris|login/i)
    .filter(':visible')
    .first()
    .click();
  cy.url().should('include', '/admin/dashboard');
});

Cypress.Commands.add('adminGet', <T = unknown,>(path: string) => {
  const tok = window.sessionStorage.getItem('filex.bearer');
  return cy
    .request({
      method: 'GET',
      url: path,
      headers: tok ? { Authorization: `Bearer ${tok}` } : {},
    })
    .then((res) => {
      expect(res.status, `GET ${path}`).to.eq(200);
      return res.body as T;
    });
});

export {};
