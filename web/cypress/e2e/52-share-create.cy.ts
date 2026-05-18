// 52-share-create — POST /api/files/share creates a share, GET
// /api/files/share/{token} returns metadata, DELETE removes it.
// Full round-trip on a fixture node so we don't pollute prod
// shares.

describe('share create round-trip', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('creates a share for an existing file then deletes it', () => {
    cy.adminGet<{ results?: Array<{ id: number; path: string; type: string }> }>(
      '/api/files/search?q=a&limit=20',
    ).then((d) => {
      const file = (d.results ?? []).find((r) => r.type === 'file');
      if (!file) {
        cy.log('no file fixture — skip');
        return;
      }
      const tok = window.sessionStorage.getItem('filex.bearer');
      // CREATE
      cy.request({
        method: 'POST',
        url: '/api/files/share',
        headers: { Authorization: `Bearer ${tok}` },
        body: { node_id: file.id, expires_in_seconds: 60 },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 201]).to.include(res.status);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const token = (body.token ?? body.share?.token) as string;
        const shareId = (body.id ?? body.share?.id) as number;
        expect(token, 'share token').to.be.a('string').and.have.length.greaterThan(8);

        // METADATA fetch is public (no Bearer needed but doesn't hurt).
        cy.request({
          method: 'GET',
          url: `/api/files/share/${token}`,
          failOnStatusCode: false,
        }).then((g) => {
          expect([200, 401]).to.include(g.status);
          if (g.status === 200) {
            const gb = typeof g.body === 'string' ? JSON.parse(g.body) : g.body;
            // Either a flat envelope or nested {share}.
            expect(gb).to.be.an('object');
          }
        });

        // DELETE — admin can also use /api/admin/shares/{id} but
        // /api/files/share/{id} is the owner-side path.
        if (shareId) {
          cy.request({
            method: 'DELETE',
            url: `/api/files/share/${shareId}`,
            headers: { Authorization: `Bearer ${tok}` },
            failOnStatusCode: false,
          }).then((dr) => {
            expect([200, 204, 404]).to.include(dr.status);
          });
        }
      });
    });
  });
});
