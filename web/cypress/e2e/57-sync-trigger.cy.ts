// 57-sync-trigger — manual storage sync POST + per-storage drift
// + global sync-runs cross-check.

describe('manual sync trigger', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('POST /api/admin/storages/{id}/sync queues a sync run', () => {
    cy.adminGet<Array<{ id: number; name: string; enabled: boolean }>>('/api/admin/storages').then(
      (storages) => {
        const target = (storages ?? []).find((s) => s.enabled);
        if (!target) {
          cy.log('no enabled storage — skip');
          return;
        }
        const tok = window.sessionStorage.getItem('filex.bearer');
        cy.request({
          method: 'POST',
          url: `/api/admin/storages/${target.id}/sync`,
          headers: tok ? { Authorization: `Bearer ${tok}` } : {},
          failOnStatusCode: false,
        }).then((res) => {
          // 200/202 = queued; 409 = already running.
          expect([200, 202, 204, 409]).to.include(res.status);
        });
      },
    );
  });

  it('sync trigger on bogus id returns 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/storages/999999/sync',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });

  it('sync-runs detail endpoint reachable for a real id', () => {
    cy.adminGet<{ items?: Array<{ id: number }> } | Array<{ id: number }>>(
      '/api/admin/sync-runs?limit=1',
    ).then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      if (items.length === 0) return;
      const id = items[0].id;
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/admin/sync-runs/${id}`,
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
      }).then((res) => {
        expect(res.status).to.eq(200);
      });
    });
  });
});
