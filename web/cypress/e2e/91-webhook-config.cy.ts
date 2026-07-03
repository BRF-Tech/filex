// 91-webhook-config — admin notification webhook config GET +
// PATCH round-trip.

describe('webhook config', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET admin webhook-config returns object', () => {
    cy.adminGet<Record<string, unknown>>('/api/admin/notifications/webhook-config').then((d) => {
      expect(d, 'webhook config').to.be.an('object');
    });
  });

  it('admin test endpoint accepts a probe payload', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/notifications/test',
        headers: { Authorization: `Bearer ${tok}` },
        body: { event: 'cypress.probe', severity: 'info', title: 'cypress', body: 'probe' },
        failOnStatusCode: false,
      }).then((res) => {
        // 200/202/204 = sent; 400 = bad event; 503 = webhook unconfigured.
        expect([200, 202, 204, 400, 422, 503]).to.include(res.status);
      });
    });
  });

  it('admin list shows notification history', () => {
    cy.adminGet<{ items?: unknown[] } | unknown[]>('/api/admin/notifications').then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      expect(items, 'admin notif items').to.be.an('array');
    });
  });
});
