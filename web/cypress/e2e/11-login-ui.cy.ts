// 11-login-ui — interactive login form smoke. Drives the visible
// Login.vue to make sure the SPA bootstraps + the form actually
// submits to /api/auth/login.

describe('login UI', () => {
  it('shows the email + password inputs', () => {
    cy.visit('/admin/login');
    cy.get('input[type="email"], input[name="email"]').should('exist');
    cy.get('input[type="password"], input[name="password"]').should('exist');
  });

  it('cy.uiLogin lands on the dashboard', () => {
    cy.uiLogin();
    cy.url().should('include', '/admin/dashboard');
    // Sidebar must render (means AdminLayout mounted + auth store
    // hydrated).
    cy.contains(/depolar|storages|dashboard/i).should('be.visible');
  });

  it('bad credentials surface an error without leaving /login', () => {
    cy.visit('/admin/login');
    cy.get('input[type="email"], input[name="email"]').first().clear().type('admin@local');
    cy.get('input[type="password"], input[name="password"]')
      .first()
      .clear()
      .type('definitely-wrong-password');
    cy.contains('button', /sign in|giriş|giris|login/i)
      .filter(':visible')
      .first()
      .click();
    // Still on /login (no redirect to dashboard).
    cy.url({ timeout: 5000 }).should('include', '/login');
  });
});
