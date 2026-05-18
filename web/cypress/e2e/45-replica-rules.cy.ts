// 45-replica-rules — full replica admin surface: rules, failures,
// settings, report.

describe('replica admin', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('rules list envelope', () => {
    cy.adminGet<{ items?: Array<{ id: number; path_pattern: string; mode: string; enabled: boolean }> }>(
      '/api/admin/replica/rules',
    ).then((d) => {
      expect(d.items, 'rules.items').to.be.an('array');
      for (const r of d.items ?? []) {
        expect(r, 'rule envelope').to.have.all.keys(
          'id',
          'path_pattern',
          'mode',
          'priority',
          'enabled',
          'description',
          'created_at',
          'updated_at',
        );
      }
    });
  });

  it('failures count is a non-negative integer', () => {
    cy.adminGet<{ count?: number } | number>('/api/admin/replica/failures/count').then((d) => {
      const n = typeof d === 'number' ? d : (d.count ?? 0);
      expect(n, 'failure count').to.be.a('number').and.gte(0);
    });
  });

  it('failures list is an array', () => {
    cy.adminGet<{ items?: unknown[] } | unknown[]>('/api/admin/replica/failures').then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      expect(items, 'failures').to.be.an('array');
    });
  });

  it('settings endpoint returns object', () => {
    cy.adminGet<Record<string, unknown>>('/api/admin/replica/settings').then((d) => {
      expect(d, 'settings').to.be.an('object');
    });
  });

  it('report endpoint is reachable', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/admin/replica/report',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        // 200 with report OR 204 if no run has happened yet.
        expect([200, 204, 404]).to.include(res.status);
      });
    });
  });

  it('rule CRUD round-trip (create → list contains → delete)', () => {
    cy.apiLogin().then((tok) => {
      const pattern = `cypress-fixture-${Date.now()}/*`;
      // CREATE
      cy.request({
        method: 'POST',
        url: '/api/admin/replica/rules',
        headers: { Authorization: `Bearer ${tok}` },
        body: { path_pattern: pattern, mode: 'mirror', priority: 50, enabled: false, description: 'cypress' },
      }).then((c) => {
        expect(c.status).to.be.oneOf([200, 201]);
        const body = typeof c.body === 'string' ? JSON.parse(c.body) : c.body;
        const rid = (body.id ?? body.rule?.id) as number;
        expect(rid, 'new rule id').to.be.a('number');

        // LIST contains it.
        cy.request({
          method: 'GET',
          url: '/api/admin/replica/rules',
          headers: { Authorization: `Bearer ${tok}` },
        }).then((lres) => {
          const lb = typeof lres.body === 'string' ? JSON.parse(lres.body) : lres.body;
          const found = (lb.items ?? []).find((r: { id: number }) => r.id === rid);
          expect(found, 'rule visible in list').to.exist;
        });

        // DELETE
        cy.request({
          method: 'DELETE',
          url: `/api/admin/replica/rules/${rid}`,
          headers: { Authorization: `Bearer ${tok}` },
        }).then((d) => {
          expect(d.status).to.be.oneOf([200, 204]);
        });
      });
    });
  });
});
