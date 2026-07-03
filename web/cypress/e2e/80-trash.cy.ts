// 80-trash — trash list endpoint shape + Çöp Kutusu page renders
// even when empty (the empty state mustn't crash the SPA).

describe('trash', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('trash endpoint returns an entries array', () => {
    cy.adminGet<{ entries?: unknown[]; total?: number }>(
      '/api/files/manager/trash?limit=25',
    ).then((d) => {
      expect(d.entries, 'trash.entries').to.be.an('array');
    });
  });

  it('Çöp Kutusu page renders without errors', () => {
    cy.visit('/admin/trash');
    cy.contains(/çöp|trash/i, { timeout: 10000 }).should('be.visible');
    // No stack trace / "undefined" leaks
    cy.get('body').should(($b) => {
      const text = $b.text();
      expect(text).not.to.contain('undefined');
      expect(text).not.to.match(/TypeError|ReferenceError/);
    });
  });
});
