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
    // ⚠ Scoped to <main>, and that is the whole point of this case.
    //
    // Unscoped, `cy.contains(/depolar|storages/i)` matched the SIDEBAR link of
    // the same name, not the stat card — so the test read as green while the
    // dashboard could have rendered nothing at all. It went red the moment the
    // suite ran on a 900px-tall window, because Cypress calls a link clipped
    // outside a scrollable ancestor hidden and the lower nav entries (Queue,
    // Audit, …) are below that fold. A test that fails for the reason it
    // passes is measuring the wrong element.
    //
    // Match on unique substrings — Turkish "İ" lowercases to "i̇" (with a
    // combining dot), so `/indekslenmiş/i` does not always match the uppercase
    // form rendered via CSS text-transform.
    cy.get('main').within(() => {
      cy.contains(/depolar|storages/i).should('be.visible');
      cy.contains(/kullanıcılar|users/i).should('be.visible');
      cy.contains(/dekslenmi|indexed/i).should('be.visible');
      cy.contains(/toplam boyut|total size/i).should('be.visible');
      cy.contains(/kuyruk|queue/i).should('be.visible');
    });
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

  it('numeric cards render as real digits, not "…" placeholders', () => {
    // Stat values land in the `Stat` component (text-2xl class).
    // Scope to the cards specifically so we don't pick up the
    // "Son senkronlar" empty-state ellipsis from elsewhere.
    //
    // ⚠ `.should()` with a callback, NOT `.then()`. `cy.wait('@dash')` proves
    // the RESPONSE arrived, not that Vue has re-rendered with it, so a `.then`
    // reads the DOM exactly once and can catch the placeholder frame. Measured
    // 2026-09-05: this case failed one full run with
    // `expected '… … … … … …' to match /\d/` and passed the next, same commit.
    // `.should` retries until the digits land or the timeout expires — so a
    // dashboard that genuinely never hydrates still fails, which is the point.
    cy.get('.text-2xl, .text-3xl').should('have.length.greaterThan', 0);
    cy.get('.text-2xl, .text-3xl').should(($els) => {
      const text = $els.map((_, el) => el.textContent || '').get().join(' ');
      expect(text, 'stat-card text').to.match(/\d/);
    });
  });
});
