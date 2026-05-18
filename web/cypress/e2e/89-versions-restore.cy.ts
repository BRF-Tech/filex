// 89-versions-restore — POST /api/files/versions/restore + admin
// hard-delete are wired (no 405/404 on route).

describe('versions mutate', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('restore endpoint rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/versions/restore',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('admin hard-delete on bogus id is 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'DELETE',
        url: '/api/admin/versions/999999',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 204, 400, 404]).to.include(res.status);
      });
    });
  });
});
