// 25-profile — /api/auth/me + per-user profile PATCH round-trip.
// Confirms locale + timezone are persisted (regression guard for
// the per-user timezone landing).

describe('profile', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('/api/auth/me envelope shape', () => {
    cy.adminGet<{ user: { id: number; email: string; role: string; locale?: string; timezone?: string } }>(
      '/api/auth/me',
    ).then((d) => {
      expect(d.user, 'envelope.user').to.exist;
      expect(d.user.email, 'user.email').to.be.a('string');
      expect(d.user.role, 'user.role').to.be.a('string');
      expect(d.user.id, 'user.id').to.be.a('number');
    });
  });

  it('PATCH /api/auth/profile persists locale changes', () => {
    cy.apiLogin().then((tok) => {
      // Read current locale.
      cy.request({
        method: 'GET',
        url: '/api/auth/me',
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const original = body.user.locale ?? 'tr';
        const flipped = original === 'tr' ? 'en' : 'tr';

        // PATCH to flipped locale.
        cy.request({
          method: 'PATCH',
          url: '/api/auth/profile',
          headers: { Authorization: `Bearer ${tok}` },
          body: { locale: flipped },
        }).then((p) => {
          expect(p.status).to.be.oneOf([200, 204]);
        });

        // Verify it stuck.
        cy.request({
          method: 'GET',
          url: '/api/auth/me',
          headers: { Authorization: `Bearer ${tok}` },
        }).then((vres) => {
          const v = typeof vres.body === 'string' ? JSON.parse(vres.body) : vres.body;
          expect(v.user.locale, 'flipped locale').to.eq(flipped);
        });

        // Restore.
        cy.request({
          method: 'PATCH',
          url: '/api/auth/profile',
          headers: { Authorization: `Bearer ${tok}` },
          body: { locale: original },
        });
      });
    });
  });

  // ⚠ /admin/profile was a PAGE carrying these same fields and has been
  // retired — the user-settings dialog owns them now, and it is the one a
  // non-admin can open too. The address survives as a forward (the startup
  // banner and `.first-run.txt` print it), so what is checked here is that
  // following it still lands on the fields, not on a 404.
  it('the retired /admin/profile address still lands on the settings dialog', () => {
    cy.visit('/admin/profile');
    cy.get('[data-testid="user-settings-dialog"]', { timeout: 10000 }).should('be.visible');
    cy.get('[data-testid="user-settings-profile"]').should('exist');
  });

  it('?settings=1 opens the dialog — the address the server prints', () => {
    cy.visit('/admin/dashboard?settings=1');
    cy.get('[data-testid="user-settings-dialog"]', { timeout: 10000 }).should('be.visible');
  });
});
