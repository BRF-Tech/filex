// 00-smoke — every other spec relies on these basics. If smoke fails,
// the rest are noise.

describe('smoke', () => {
  it('healthz returns ok', () => {
    cy.request('/healthz').then((res) => {
      expect(res.status).to.eq(200);
      // Backend serves `{"status":"ok"}` with `Content-Type:
      // application/json` but Cypress doesn't always auto-parse JSON
      // bodies — guard both shapes.
      const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
      expect(body).to.have.property('status', 'ok');
    });
  });

  it('public capabilities endpoint returns a sane payload', () => {
    cy.request('/api/capabilities').then((res) => {
      expect(res.status).to.eq(200);
      expect(res.body).to.have.property('storage_drivers');
      expect(res.body).to.have.property('auth_drivers');
    });
  });

  it('admin SPA boots without unauth redirect', () => {
    cy.visit('/admin/login');
    cy.contains(/oturum aç|sign in|filex/i, { timeout: 10000 }).should('be.visible');
  });
});
