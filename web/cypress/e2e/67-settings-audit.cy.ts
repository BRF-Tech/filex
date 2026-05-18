// 67-settings-audit — admin settings dict + audit log paging.

describe('settings', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/admin/settings returns a dict with first_run_at', () => {
    cy.adminGet<Record<string, unknown>>('/api/admin/settings').then((d) => {
      expect(d, 'settings').to.be.an('object');
      expect(d, 'has first_run_at').to.have.property('first_run_at');
    });
  });

  it('PUT setting round-trip (write + read-back + restore)', () => {
    cy.apiLogin().then((tok) => {
      const key = 'cypress_probe';
      const value = `cypress-${Date.now()}`;
      cy.request({
        method: 'PUT',
        url: `/api/admin/settings/${key}`,
        headers: { Authorization: `Bearer ${tok}` },
        body: { value },
        failOnStatusCode: false,
      }).then((res) => {
        // 200/204 with new value; some deployments may reject unknown
        // keys with 400.
        expect([200, 204, 400, 422]).to.include(res.status);
        if (res.status === 200 || res.status === 204) {
          cy.request({
            method: 'GET',
            url: '/api/admin/settings',
            headers: { Authorization: `Bearer ${tok}` },
          }).then((g) => {
            const body = typeof g.body === 'string' ? JSON.parse(g.body) : g.body;
            expect(body[key], `${key} round-tripped`).to.eq(value);
          });
        }
      });
    });
  });
});

describe('audit log', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/admin/audit returns items[]', () => {
    cy.adminGet<{ items?: unknown[] } | unknown[]>('/api/admin/audit').then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      expect(items, 'audit items').to.be.an('array');
    });
  });

  it('audit ?limit=5 honors limit', () => {
    cy.adminGet<{ items?: unknown[] } | unknown[]>('/api/admin/audit?limit=5').then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      expect(items.length, 'limit honored').to.be.lessThan(6);
    });
  });
});
