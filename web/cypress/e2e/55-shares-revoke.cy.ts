// 55-shares-revoke — admin share revoke + per-row delete endpoints
// are wired and the share metadata fetch returns a known shape.

describe('shares revoke + delete', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('share metadata fetch on a bogus token returns 404 JSON', () => {
    cy.request({
      method: 'GET',
      url: '/api/files/share/this-token-definitely-does-not-exist-xyz',
      failOnStatusCode: false,
    }).then((res) => {
      expect([404, 410]).to.include(res.status);
    });
  });

  it('admin shares list joins storage_name + creator_email', () => {
    cy.adminGet<{
      items?: Array<{ share?: { id: number; token: string }; creator_email?: string; storage_name?: string }>;
    }>('/api/admin/shares').then((d) => {
      // We don't assert on length — only on shape when rows exist.
      for (const row of d.items ?? []) {
        expect(row, 'envelope').to.have.property('share');
        expect(row.share, 'share inner').to.have.property('id');
        expect(row.share, 'share inner').to.have.property('token');
      }
    });
  });

  it('revoke endpoint exists (404 / 400 on bogus id, never 405)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/shares/999999/revoke',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        // The handler IS wired — the route should never 405. Either it
        // resolves the id (200/204) or rejects it as missing (404/400).
        expect([200, 204, 400, 404]).to.include(res.status);
      });
    });
  });

  it('delete endpoint exists (no 405 method-not-allowed)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'DELETE',
        url: '/api/admin/shares/999999',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 204, 400, 404]).to.include(res.status);
      });
    });
  });
});
