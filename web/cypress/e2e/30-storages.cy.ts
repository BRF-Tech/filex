// 30-storages — Depolar page renders the list, hides replica targets,
// and shows real stats (file count + total bytes) per row.

describe('storages list', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/admin/storages');
  });

  it('shows the page heading', () => {
    cy.contains(/depolar|storages/i).should('be.visible');
  });

  it('list response stays clean of replica rows', () => {
    cy.adminGet<{ id: number; name: string; role?: string }[]>('/api/admin/storages').then((rows) => {
      // After v0.1.18 migration replicas live in a separate table, so
      // /api/admin/storages should never return a `role: 'replica'`.
      const bad = rows.filter((r) => r.role === 'replica');
      expect(bad, `found replica rows in /admin/storages: ${JSON.stringify(bad)}`).to.have.length(0);
    });
  });

  it('replication-targets endpoint is reachable', () => {
    cy.adminGet<unknown[]>('/api/admin/replication-targets').then((rows) => {
      expect(rows).to.be.an('array');
    });
  });

  it('each storage row shows a stats line (N dosya · X MB)', () => {
    cy.adminGet<{ id: number; stats?: { file_count: number; total_size_bytes: number } }[]>(
      '/api/admin/storages',
    ).then((rows) => {
      if (rows.length === 0) {
        cy.log('no storages configured — skipping');
        return;
      }
      // Backend must emit the v0.1.13 stats blob.
      const missing = rows.filter((r) => !r.stats);
      expect(missing, `rows missing stats: ${JSON.stringify(missing)}`).to.have.length(0);
    });
  });

  it('clicking a storage row navigates to the detail page', () => {
    cy.adminGet<{ id: number; name: string }[]>('/api/admin/storages').then((rows) => {
      if (rows.length === 0) {
        cy.log('no storages configured — skipping');
        return;
      }
      const first = rows[0];
      cy.contains('a, [role="link"], button', first.name).first().click();
      cy.url().should('match', new RegExp(`/admin/storages/${first.id}\\b`));
    });
  });
});
