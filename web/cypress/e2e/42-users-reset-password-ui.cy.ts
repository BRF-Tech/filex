// 42-users-reset-password-ui — issue #25, "Password reset icon forwards to
// user deletion … I can not reset/set to no one OIDC user (to let them use
// that pass for WebDAV)".
//
// Three faults behind one report, each measured here in a real browser:
//
//   1. The reset dialog asked the DELETE question. Its confirmation text was
//      `users.deleteConfirm` ("Delete user …?"), so the key icon read as a
//      second delete button and the reporter, reasonably, backed out.
//   2. Confirming it anyway reset the password and signed the account out
//      everywhere — then showed nothing. The server answers `new_password`,
//      the page read `password`, so the one-time value was dropped and the
//      account was left with a password nobody knows.
//   3. The create dialog marks the password optional — the natural way to add
//      an SSO account — and the server refused every such request with
//      "email and password required".

const dialog = () => cy.get('[role="dialog"]').filter(':visible').last();

function openUsers(email: string) {
  cy.visit('/admin/users');
  cy.get('input[placeholder]').filter(':visible').first().type(email);
}

describe('users: reset password from the list (issue #25)', () => {
  it('asks the reset question, shows the new password once, and that password signs in', () => {
    const email = `cypress-reset-${Date.now()}@example.invalid`;
    const original = 'CypressFixture!2026';
    // The browser signs in FIRST: apiLogin leaves a bearer behind and a
    // signed-in browser is sent straight past /admin/login.
    cy.uiLogin();
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/users',
        headers: { Authorization: `Bearer ${tok}` },
        body: { email, password: original, display_name: 'Reset Fixture', role: 'user' },
      }).then((res) => {
        const uid = (res.body.id ?? res.body.user?.id) as number;

        openUsers(email);
        // ⚠ The users list is the shared DataTable (one table, v0.43.0): its
        // rows are `role="row"` flex rows, not `<tr>`, and the row's verbs moved
        // into its Actions menu — so there is no key icon on the row to click.
        // Both used to be selectors here and both went stale together; the
        // first run of this release never got that far, because the login
        // helper failed first. The menu gives every entry an address
        // (RowActions: `<control testid>-<action key>`), which is what is used.
        cy.contains('[role="row"]', email, { timeout: 10000 }).should('be.visible');
        cy.get(`[data-testid="user-actions-${uid}"]`).click();
        cy.get(`[data-testid="user-actions-${uid}-reset"]`).click();

        dialog().should('contain.text', email);
        dialog().invoke('text').should('not.match', /delete|\bsil/i);

        dialog().contains('button', /confirm|onayla/i).click();
        dialog()
          .find('code', { timeout: 10000 })
          .invoke('text')
          .then((raw) => {
            const password = raw.trim();
            expect(password, 'the new password is shown').to.have.length.greaterThan(8);
            cy.request({ method: 'POST', url: '/api/auth/login', body: { email, password }, failOnStatusCode: false })
              .its('status')
              .should('eq', 200);
            cy.request({ method: 'POST', url: '/api/auth/login', body: { email, password: original }, failOnStatusCode: false })
              .its('status')
              .should('eq', 401);
          });

        cy.request({ method: 'DELETE', url: `/api/admin/users/${uid}`, headers: { Authorization: `Bearer ${tok}` }, failOnStatusCode: false });
      });
    });
  });

  it('creates an account without a password, as the dialog promises', () => {
    const email = `cypress-sso-${Date.now()}@example.invalid`;
    cy.uiLogin();
    cy.visit('/admin/users');
    cy.contains('button', /add|new|ekle|yeni/i).filter(':visible').first().click();
    dialog().find('input[type="email"]').type(email);
    // Submitted with Enter, the way a keyboard does it. ⚠ Not by the button's
    // TEXT: the form carries a hidden `sr-only` submit (for exactly this Enter)
    // that also says "Create", and a text match found that one — covered, so
    // the click failed with the dialog working (v0.43.0 release run).
    dialog().find('input').not('[type="email"]').not('[type="password"]').first().type('SSO Fixture{enter}');
    cy.get('dialog[open]', { timeout: 10000 }).should('not.exist');

    cy.get('input[placeholder]').filter(':visible').first().type(email);
    cy.contains('[role="row"]', email, { timeout: 10000 }).should('be.visible');

    cy.apiLogin().then((tok) => {
      cy.request({ url: `/api/admin/users?q=${encodeURIComponent(email)}`, headers: { Authorization: `Bearer ${tok}` } }).then((res) => {
        const items = (res.body.items ?? res.body) as Array<{ id: number; email: string }>;
        const u = items.find((x) => x.email === email);
        expect(u, 'the account exists').to.exist;
        cy.request({ method: 'DELETE', url: `/api/admin/users/${u!.id}`, headers: { Authorization: `Bearer ${tok}` }, failOnStatusCode: false });
      });
    });
  });
});
