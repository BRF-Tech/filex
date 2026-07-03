// 93-search-rebuild — admin /api/admin/search/rebuild triggers
// a bleve reindex job (we just probe the route is wired).

describe('search rebuild', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('admin search/stats returns object with index info', () => {
    cy.adminGet<Record<string, unknown>>('/api/admin/search/stats').then((d) => {
      expect(d, 'stats').to.be.an('object');
    });
  });

  it('admin search/rebuild route is wired (POST returns 2xx/202)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/search/rebuild',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        // 200/202 = queued; 409 = already running; never 404/405.
        expect([200, 202, 204, 409]).to.include(res.status);
      });
    });
  });
});
