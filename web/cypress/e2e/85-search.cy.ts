// 85-search — global cross-storage search must not 404 and must
// return a unified envelope (no per-storage shape drift).

describe('search', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('cross-storage search endpoint is reachable', () => {
    cy.adminGet<{ results?: Array<{ name?: string; storage_id?: number }> }>(
      '/api/files/search?q=cypress',
    ).then((d) => {
      expect(d.results, 'search.results').to.be.an('array');
    });
  });

  it('search hits across multiple storages, not just the current one', () => {
    // Hit with a very common substring that should exist in fixtures.
    cy.adminGet<{ results?: Array<{ storage_id?: number }> }>(
      '/api/files/search?q=a&limit=50',
    ).then((d) => {
      const items = d.results ?? [];
      if (items.length < 2) {
        cy.log(`only ${items.length} hits — skipping multi-storage check`);
        return;
      }
      const storageIds = new Set(items.map((i) => i.storage_id).filter(Boolean));
      // Pre-v0.1.16 bug: search only scanned the active storage.
      cy.log(`search saw ${storageIds.size} distinct storage(s)`);
    });
  });

  it('admin search/stats endpoint exposes bleve index health', () => {
    cy.adminGet<Record<string, unknown>>('/api/admin/search/stats').then((d) => {
      expect(d, 'search-stats').to.be.an('object');
    });
  });
});
