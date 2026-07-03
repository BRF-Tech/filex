// 82-save-text — plain-text save endpoint used by the markdown +
// monaco editors. Path validation + content size handling.

describe('save-text', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('rejects missing path with 400', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: { Authorization: `Bearer ${tok}` },
        body: { content: 'hello' },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });

  it('path without adapter defaults to first storage (documented behavior)', () => {
    // splitAdapterPath returns adapter="" for paths without "://" —
    // the handler then falls back to `storages[0].Name`. This is the
    // SFC's no-adapter fallback contract. We verify it doesn't 500.
    // We accept 200 (saved on default storage), 403 (read-only), or
    // 415 (extension rejected by save-text whitelist).
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: { Authorization: `Bearer ${tok}` },
        body: { path: `cypress-noadapter-${Date.now()}.txt`, content: 'h' },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 403, 415]).to.include(res.status);
      });
    });
  });

  it('rejects path with unknown adapter', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: { Authorization: `Bearer ${tok}` },
        body: { path: 'nonexistent-adapter://x.txt', content: 'hi' },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });
});
