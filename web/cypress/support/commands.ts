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
      /** Presses the sign-in form's own submit button — found by what it IS,
       *  never by what it SAYS (see the note on the command). */
      submitLogin(): Chainable<void>;
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
      // ⚠ Not `failOnStatusCode: false` as a way of ignoring a refusal — the
      // assertion below still demands 200. It is here so a refusal reports the
      // server's OWN reason instead of Cypress's generic "the response was
      // 401". `backend/internal/auth/drivers/local` folds several very
      // different situations into one 401 ("invalid credentials"): a wrong
      // password, an account that has since had TOTP turned on, and any error
      // from the user lookup — and SQLite here runs on a single connection
      // (`SetMaxOpenConns(1)`), so a busy database is one of them. When a run
      // suddenly starts refusing the admin it has been using for two hundred
      // tests, the body is the only thing that tells them apart.
      failOnStatusCode: false,
    })
    .then((res) => {
      const why = typeof res.body === 'string' ? res.body : JSON.stringify(res.body ?? {});
      expect(res.status, `login as ${e} (server said: ${why})`).to.eq(200);
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
  cy.submitLogin();
  // The start page: Home for every account by default, the dashboard for an
  // admin who picked it in user settings (web/src/lib/startPage.ts, 0.41.0).
  cy.url().should('match', /\/admin\/(home|dashboard)([?#]|$)/);
});

/**
 * The sign-in form's submit button: the `type="submit"` button of the form
 * that holds the password field.
 *
 * ⚠⚠ NOT by its text. This used to be `cy.contains('button', /sign
 * in|giriş|giris|login/i)`, and Cypress's Electron follows the operating
 * system's language — so on a Turkish machine the page opens in Turkish, and
 * the day a translation pass renamed the button ("Giriş yap" → "Oturum
 * aç", one word per concept, v0.43.0) every spec behind the login helpers
 * went red with the product working (measured on the v0.43.0 release run:
 * 8 tests in 5 specs). A regex of every language the button has been in is a
 * list that goes stale at the next rename; what the button IS does not change.
 * Both sign-in layouts in Login.vue have exactly one submit button inside the
 * form that holds the password, so this finds the right one in either.
 */
Cypress.Commands.add('submitLogin', () => {
  cy.get('input[type="password"], input[name="password"]')
    .filter(':visible')
    .first()
    .closest('form')
    .find('button[type="submit"]')
    .filter(':visible')
    .first()
    .click();
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
