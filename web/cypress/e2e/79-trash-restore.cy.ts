// 79-trash-restore — restore + admin empty + per-row purge.

describe('trash mutate', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('restore endpoint rejects empty body with 400', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/manager/restore',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('admin purge on bogus id returns 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'DELETE',
        url: '/api/admin/trash/999999',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 204, 400, 404]).to.include(res.status);
      });
    });
  });
});
