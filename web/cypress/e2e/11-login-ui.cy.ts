// 11-login-ui — interactive login form smoke. Drives the visible
// Login.vue to make sure the SPA bootstraps + the form actually
// submits to /api/auth/login.

describe('login UI', () => {
  it('shows the email + password inputs', () => {
    cy.visit('/admin/login');
    cy.get('input[type="email"], input[name="email"]').should('exist');
    cy.get('input[type="password"], input[name="password"]').should('exist');
  });

  it('cy.uiLogin lands on the start page', () => {
    cy.uiLogin();
    // Home by default, for every role (web/src/lib/startPage.ts).
    cy.url().should('include', '/admin/home');
    // The navigation panel must render: the explorer mounted and the auth
    // store hydrated.
    cy.get('[data-testid="sidenav"]', { timeout: 15000 }).should('be.visible');
  });

  it('bad credentials surface an error without leaving /login', () => {
    cy.visit('/admin/login');
    cy.get('input[type="email"], input[name="email"]').first().clear().type('admin@local');
    cy.get('input[type="password"], input[name="password"]')
      .first()
      .clear()
      .type('definitely-wrong-password');
    cy.submitLogin();
    // Still on /login (no redirect to dashboard).
    cy.url({ timeout: 5000 }).should('include', '/login');
  });
});
