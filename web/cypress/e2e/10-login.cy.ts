// 10-login — happy + sad paths for the admin login flow.

describe('login', () => {
  it('rejects bad credentials with an error message', () => {
    cy.visit('/admin/login');
    cy.get('input[type="email"]').first().clear().type('admin@local');
    cy.get('input[type="password"]').first().clear().type('definitely-wrong-password');
    cy.contains('button', /sign in|giriş|giris|login/i).filter(':visible').first().click();
    cy.url().should('include', '/admin/login');
    cy.contains(/geçersiz|invalid|incorrect|hatalı/i, { timeout: 5000 }).should('be.visible');
  });

  it('accepts admin credentials and lands on the dashboard', () => {
    cy.uiLogin();
    cy.contains(/panel|dashboard/i, { timeout: 5000 }).should('be.visible');
  });

  it('apiLogin sets the bearer token', () => {
    cy.apiLogin().then((tok) => {
      expect(tok).to.be.a('string').and.to.have.length.greaterThan(20);
    });
  });
});
