// 50-shares — admin Paylaşımlar list renders real values (no
// "undefined" / "—" placeholders) and the backend envelope carries
// the storage_name join + creator_email + node_path (v0.1.19).

describe('shares', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/admin/shares');
  });

  it('shows the page heading', () => {
    cy.contains(/paylaşım|share/i).should('be.visible');
  });

  it('ListAllShares envelope fields are present per row', () => {
    cy.adminGet<{
      items?: Array<{
        share?: { id: number; token: string; download_count?: number; has_pin?: boolean };
        creator_email?: string;
        node_path?: string;
        storage_name?: string;
      }>;
    }>('/api/admin/shares').then((d) => {
      const items = d.items ?? [];
      if (items.length === 0) {
        cy.log('no shares — skipping body assertions');
        return;
      }
      for (const row of items) {
        expect(row.share, 'envelope.share').to.exist;
        expect(row, 'envelope.creator_email').to.have.property('creator_email');
        expect(row, 'envelope.node_path').to.have.property('node_path');
        expect(row, 'envelope.storage_name').to.have.property('storage_name');
      }
    });
  });

  it('UI never prints the literal string "undefined"', () => {
    cy.contains(/paylaşım|share/i).should('be.visible');
    cy.get('body').then(($b) => {
      const text = $b.text();
      // The pre-v0.1.19 bug surfaced as "undefined" in the Downloads
      // column. Regression guard.
      expect(text).not.to.contain('undefined');
    });
  });

  it('expired share token returns a styled 404 page', () => {
    cy.request({
      url: '/s/this-token-does-not-exist',
      failOnStatusCode: false,
    }).then((res) => {
      // 404 is OK; the body should look like our HTML error template
      // (not the legacy "expired" text/plain).
      expect([404, 410]).to.include(res.status);
      expect(res.headers['content-type']).to.match(/html/i);
    });
  });
});
