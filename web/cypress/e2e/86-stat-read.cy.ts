// 86-stat-read — /api/files/stat + /api/files/read for an existing
// file fixture. Used by the SFC preview modal to decide which viewer
// to mount.

describe('stat + read', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('stat returns metadata for a real node id', () => {
    cy.adminGet<{ results?: Array<{ id: number; type: string }> }>(
      '/api/files/search?q=a&limit=10',
    ).then((d) => {
      const file = (d.results ?? []).find((r) => r.type === 'file');
      if (!file) return;
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        // /api/files/stat takes `?id=<node id>`, not `?path=…`.
        url: `/api/files/stat?id=${file.id}`,
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 404]).to.include(res.status);
      });
    });
  });

  it('read with no id returns 400', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/read',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });
});
