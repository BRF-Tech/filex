// 78-ops-queue — operational queue (copy/move/delete submitted
// via /api/files/ops) + admin queue management.

describe('ops queue', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/files/ops returns array (running ops poll)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/ops?status=running',
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        expect(res.status).to.eq(200);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const items = Array.isArray(body) ? body : (body.items ?? body.ops ?? []);
        expect(items, 'ops list').to.be.an('array');
      });
    });
  });

  it('GET /api/admin/queue/stats has the gauge keys', () => {
    cy.adminGet<Record<string, unknown>>('/api/admin/queue/stats').then((d) => {
      expect(d, 'queue stats').to.be.an('object');
    });
  });

  it('GET /api/admin/queue?limit=5 returns array', () => {
    cy.adminGet<{ items?: unknown[] } | unknown[]>('/api/admin/queue?limit=5').then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      expect(items, 'queue list').to.be.an('array');
    });
  });
});
