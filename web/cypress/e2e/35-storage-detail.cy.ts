// 35-storage-detail — per-storage admin endpoints (sync-runs +
// drift) are wired and the detail page mounts.

describe('storage detail', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('per-storage sync-runs endpoint is reachable', () => {
    cy.adminGet<Array<{ id: number; name: string }>>('/api/admin/storages').then((storages) => {
      if (!storages || storages.length === 0) {
        cy.log('no storages configured — skip');
        return;
      }
      const sid = storages[0].id;
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/admin/storages/${sid}/sync-runs`,
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
      }).then((res) => {
        expect(res.status).to.eq(200);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        // Either {items:[]} or [] depending on driver — both fine.
        expect(body, 'sync-runs body').to.satisfy((b: unknown) => Array.isArray(b) || typeof b === 'object');
      });
    });
  });

  it('per-storage drift endpoint is reachable', () => {
    cy.adminGet<Array<{ id: number }>>('/api/admin/storages').then((storages) => {
      if (!storages || storages.length === 0) return;
      const sid = storages[0].id;
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/admin/storages/${sid}/drift`,
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
        failOnStatusCode: false,
      }).then((res) => {
        // 200 OK with summary OR 204 no-drift — both acceptable.
        expect([200, 204]).to.include(res.status);
      });
    });
  });

  it('Storage edit page mounts for first storage', () => {
    cy.adminGet<Array<{ id: number }>>('/api/admin/storages').then((storages) => {
      if (!storages || storages.length === 0) return;
      cy.visit(`/admin/storages/${storages[0].id}`);
      cy.contains(/depo|storage|kayıt|edit/i, { timeout: 10000 }).should('be.visible');
    });
  });
});
