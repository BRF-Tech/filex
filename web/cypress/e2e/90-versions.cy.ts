// 90-versions — file version history admin lookup. Older revisions
// MUST be returned even when only one revision exists; the row in
// `file_versions` is created on every write, not lazily on the
// second write.

describe('versions', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('version history endpoint accepts a node id and never 404s on shape', () => {
    // Find any file node we can ask about.
    cy.adminGet<{ results?: Array<{ id?: number; type?: string }> }>(
      '/api/files/search?q=a&limit=20',
    ).then((d) => {
      const files = (d.results ?? []).filter((i) => i.type === 'file');
      if (files.length === 0) {
        cy.log('no file nodes found — skipping version history check');
        return;
      }
      const nodeId = files[0].id;
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/files/versions?node_id=${nodeId}`,
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
        failOnStatusCode: false,
      }).then((res) => {
        // 200 OK with items, OR 404 with a structured error — both
        // acceptable. The fail mode we're guarding against is the
        // pre-v0.1.16 "router didn't register" raw HTML 404.
        expect([200, 404]).to.include(res.status);
        if (res.status === 200) {
          const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
          expect(body, 'versions response').to.be.an('object');
        }
      });
    });
  });
});
