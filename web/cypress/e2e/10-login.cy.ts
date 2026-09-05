// 10-login — happy + sad paths for the admin login flow.
//
// ⚠ The identifier field is `input[name="email"]`, NOT `input[type="email"]`.
// It accepts a username as well as an address (the label reads "E-posta ya da
// kullanıcı adı" / "Email or username"), so `type="email"` would make the
// browser reject every username. This spec asked for `input[type="email"]`
// and could therefore never pass — measured 2026-09-05: `Expected to find
// element: input[type="email"], but never found it`, on a live instance whose
// login form was working perfectly. cy.uiLogin() in support/commands.ts has
// always matched both, which is why nobody noticed.

describe('login', () => {
  it('rejects bad credentials with an error message', () => {
    cy.visit('/admin/login');
    cy.get('input[name="email"]').first().clear().type('admin@local');
    cy.get('input[name="password"]').first().clear().type('definitely-wrong-password');
    cy.contains('button', /sign in|giriş|giris|login/i).filter(':visible').first().click();
    cy.url().should('include', '/admin/login');
    // The backend's own words ("invalid credentials"), surfaced by the store.
    cy.contains(/geçersiz|invalid|incorrect|hatalı/i, { timeout: 8000 }).should('be.visible');
  });

  it('accepts admin credentials and lands on the dashboard', () => {
    cy.uiLogin();
    cy.contains(/panel|dashboard/i, { timeout: 8000 }).should('be.visible');
  });

  it('apiLogin sets the bearer token', () => {
    cy.apiLogin().then((tok) => {
      expect(tok).to.be.a('string').and.to.have.length.greaterThan(20);
    });
  });
});
