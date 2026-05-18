// 20-dashboard — every stat card resolves to a real number, not the
// pre-v0.1.19 "0 B" placeholder.

describe('dashboard', () => {
  beforeEach(() => {
    cy.apiLogin();
    // Intercept the dashboard fetch so we can wait for the cards to
    // hydrate. Without this the assertions race against the SPA's
    // initial "…" placeholder state.
    cy.intercept('GET', '/api/admin/dashboard').as('dash');
    cy.visit('/admin/dashboard');
    cy.wait('@dash', { timeout: 15000 });
  });

  it('renders the five top-line stat cards', () => {
    // Match against unique substrings — Turkish "İ" lowercases to
    // "i̇" (with combining dot) so `/indekslenmiş/i` doesn't always
    // match the uppercase form rendered via CSS text-transform.
    cy.contains(/depolar|storages/i).should('be.visible');
    cy.contains(/kullanıcılar|users/i).should('be.visible');
    cy.contains(/dekslenmi|indexed/i).should('be.visible');
    cy.contains(/toplam boyut|total size/i).should('be.visible');
    cy.contains(/kuyruk|queue/i).should('be.visible');
  });

  it('total_bytes is non-zero when storage_count > 0', () => {
    cy.adminGet<{
      storages: { total_files?: number; total_bytes?: number }[];
      total_files?: number;
      total_bytes?: number;
    }>('/api/admin/dashboard').then((d) => {
      const haveStorages = (d.storages?.length ?? 0) > 0;
      const haveFiles = (d.total_files ?? 0) > 0;
      if (haveStorages && haveFiles) {
        // If we have indexed files at all, total_bytes must follow.
        // The pre-v0.1.19 bug surfaced as files > 0 / bytes = 0.
        expect(d.total_bytes ?? 0, 'total_bytes').to.be.greaterThan(0);
      } else {
        cy.log('skipping bytes check — no storages/files configured');
      }
    });
  });

  it('numeric cards render as real digits, not "—" placeholders', () => {
    // Stat values land in the `Stat` component (text-2xl class).
    // Scope to the cards specifically so we don't pick up the
    // "Son senkronlar" empty-state ellipsis from elsewhere.
    cy.get('.text-2xl, .text-3xl').should('have.length.greaterThan', 0);
    cy.get('.text-2xl, .text-3xl').then(($els) => {
      const text = $els.map((_, el) => el.textContent || '').get().join(' ');
      expect(text, 'stat-card text').to.match(/\d/);
    });
  });
});
