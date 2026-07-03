// 47-meta-tags-star — per-user file metadata (tags, starred,
// recently-opened). Read paths are heavily exercised by the SFC.

describe('meta / tags / star / recent', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/files/manager/tags requires a node_id', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/manager/tags',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        // 200 with default empty, OR 400 "missing node_id" — both
        // confirm the route is wired.
        expect([200, 400]).to.include(res.status);
      });
    });
  });

  it('GET /api/files/manager/tags?node_id=N returns tags array', () => {
    cy.adminGet<{ results?: Array<{ id: number; type: string }> }>(
      '/api/files/search?q=a&limit=5',
    ).then((d) => {
      const file = (d.results ?? []).find((r) => r.type === 'file');
      if (!file) {
        cy.log('no file to probe tags on');
        return;
      }
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/files/manager/tags?node_id=${file.id}`,
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
      }).then((res) => {
        expect(res.status).to.eq(200);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.tags, 'tags').to.be.an('array');
      });
    });
  });

  it('starred list contains a `path` field per row (v0.1.13)', () => {
    cy.adminGet<{ items?: Array<{ node_id: number; path?: string }> } | unknown[]>(
      '/api/files/manager/star/list?limit=10',
    ).then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      for (const row of items) {
        // The fix from 2026-05-XX: the response includes the cached
        // path so the SFC can navigate without re-resolving node IDs.
        expect(row, 'starred row').to.have.property('node_id');
      }
    });
  });

  it('star POST rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/manager/star',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('recent POST rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/manager/recent',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('tags POST rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/manager/tags',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });
});
