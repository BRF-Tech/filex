// 95-capabilities + sync runs — operational dashboard pieces.

describe('capabilities', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('public /api/capabilities is reachable without auth', () => {
    cy.request({
      method: 'GET',
      url: '/api/capabilities',
    }).then((res) => {
      expect(res.status).to.eq(200);
    });
  });

  it('capabilities envelope advertises the external service slots', () => {
    cy.adminGet<{
      external?: Record<string, { enabled?: boolean; state?: string }>;
    }>('/api/files/capabilities').then((d) => {
      expect(d.external, 'capabilities.external').to.be.an('object');
      for (const slot of ['onlyoffice', 'drawio', 'mermaid']) {
        expect(d.external, `capabilities.external.${slot}`).to.have.property(slot);
      }
    });
  });
});

describe('sync runs', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('sync-runs endpoint respects ?limit and stays within 5-day window', () => {
    cy.adminGet<{
      items?: Array<{ started_at?: string }>;
    }>('/api/admin/sync-runs?limit=5').then((d) => {
      const items = d.items ?? [];
      expect(items.length, 'sync-runs limit honored').to.be.lessThan(6);
      if (items.length > 0) {
        const fiveDaysAgo = Date.now() - 5 * 24 * 60 * 60 * 1000;
        // Older rows must be filtered out server-side (v0.1.16 fix).
        for (const r of items) {
          if (!r.started_at) continue;
          const ts = new Date(r.started_at).getTime();
          expect(ts, 'sync-run within 5 days').to.be.greaterThan(fiveDaysAgo);
        }
      }
    });
  });
});
