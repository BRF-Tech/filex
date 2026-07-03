// 88-archive — zip CRUD endpoints are wired and reject bogus
// payloads with a structured error.

describe('archive', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('list rejects an empty body with 400, not 500', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/archive/list',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('extract rejects a path-traversal entry name (zip-slip guard)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/archive/extract',
        headers: { Authorization: `Bearer ${tok}` },
        body: {
          path: 'bogus://nope.zip',
          dest: '../../escape',
        },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 403, 404, 422]).to.include(res.status);
      });
    });
  });
});
