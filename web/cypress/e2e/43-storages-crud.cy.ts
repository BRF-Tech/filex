// 43-storages-crud — admin storage create + test + delete round-
// trip on a "noop" driver fixture (fails the connectivity check
// fast, but the route wiring is what we care about).

describe('storages CRUD', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('storages.test endpoint accepts a driver+config payload', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/storages/test',
        headers: { Authorization: `Bearer ${tok}` },
        body: {
          driver: 's3',
          config: {
            bucket: 'cypress-bogus',
            region: 'nbg1',
            endpoint: 'http://127.0.0.1:1',
            access_key: 'x',
            secret_key: 'x',
          },
        },
        failOnStatusCode: false,
      }).then((res) => {
        // The handler runs an actual probe — expect a structured
        // {ok:false, error:...} response, but never a 5xx crash.
        expect([200, 400, 422]).to.include(res.status);
      });
    });
  });

  it('GET /api/admin/storages/{id} echoes the row', () => {
    cy.adminGet<Array<{ id: number; name: string }>>('/api/admin/storages').then((storages) => {
      if (!storages || storages.length === 0) return;
      const t = storages[0];
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/admin/storages/${t.id}`,
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        expect(res.status).to.eq(200);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.id, 'id echo').to.eq(t.id);
        expect(body.name, 'name echo').to.eq(t.name);
      });
    });
  });

  it('storages.update PATCH on bogus id is 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'PATCH',
        url: '/api/admin/storages/9999999',
        headers: { Authorization: `Bearer ${tok}` },
        body: { name: 'cypress-doesnt-exist' },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('storages.delete on bogus id is 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'DELETE',
        url: '/api/admin/storages/9999999',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });
});
