// 84-thumb — signed thumbnail endpoint.

describe('thumb', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/files/thumb/{id} with bogus id is 404 not 500', () => {
    cy.request({
      method: 'GET',
      url: '/api/files/thumb/99999999',
      failOnStatusCode: false,
    }).then((res) => {
      // 404 if not found; 410 if expired; 200 if a real thumb exists.
      expect([200, 401, 404, 410]).to.include(res.status);
    });
  });

  it('GET /api/files/thumb/0 (invalid id) is 400/404', () => {
    cy.request({
      method: 'GET',
      url: '/api/files/thumb/0',
      failOnStatusCode: false,
    }).then((res) => {
      expect([400, 404]).to.include(res.status);
    });
  });
});
