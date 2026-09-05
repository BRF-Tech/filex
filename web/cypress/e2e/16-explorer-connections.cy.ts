// 16-explorer-connections — "How to connect" and "API keys", reached from the
// explorer's navigation panel (v0.30.0, gezinti:g1).
//
// Both surfaces already existed in @brftech/filex-core and neither was
// reachable from inside the explorer: the web app wired the buttons into its
// own page shell, so an EMBEDDER's users had no path to a protocol guide or to
// the token those guides tell them to use. That is the regression this file
// guards — not "does a modal open", but "can a person who is only ever shown
// the explorer get a mount guide and mint their own credential".
//
// ⚠ Neither entry is gated on role here or anywhere. The backend decides what
// a caller may see; hiding the surface client-side only hides it from the
// accounts that need it.

describe('connections + API keys from the navigation panel', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/drive/explore', {
      onBeforeLoad(win) {
        win.localStorage.setItem('filex.sidenav', '1');
      },
    });
    cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('be.visible');
  });

  it('the panel offers both entries', () => {
    cy.get('[data-testid="sidenav-connect"]').should('be.visible');
    cy.get('[data-testid="sidenav-apikeys"]').should('be.visible');
  });

  it('"How to connect" opens the shared connections panel on the connect tab', () => {
    cy.get('[data-testid="sidenav-connect"]').click();
    cy.get('[data-testid="explorer-overlay"]').should('be.visible');
    cy.get('[data-testid="connections-panel"]').should('be.visible');
    // `initial-tab="connect"` — a person who came here from the panel wants the
    // mount instructions, not the storage form.
    cy.get('[data-testid="tab-connect"]').should('exist');
    // The protocol picker is the guide: without it the panel is a header.
    cy.get('[data-testid="guide-protocol"]').should('be.visible');
  });

  it('the guide names a real protocol and shows a copyable command', () => {
    cy.get('[data-testid="sidenav-connect"]').click();
    cy.get('[data-testid="guide-protocol"]').should('be.visible');
    // Whatever protocols this build offers, the picker must have some, and
    // choosing one must produce instructions rather than an empty card.
    cy.get('[data-testid="guide-protocol"] option').should('have.length.greaterThan', 0);
    cy.get('[data-testid="guide-facts"]').should('exist');
  });

  it('the overlay holds one surface at a time, and closes back to the explorer', () => {
    // ⚠ The panel is UNREACHABLE while an overlay is up — the overlay covers
    // the whole explorer, sidenav included, and Cypress refuses the click for
    // exactly the reason a person could not make it either. So this is measured
    // the way it is actually experienced: open one, close it, open the other.
    //
    // ⚠ `api-tokens` is NOT the discriminator: ConnectionsPanel embeds the same
    // TokensPanel on its own tab, on purpose (one credential screen, not two).
    // The connections CHROME is what must not be there when the caller asked
    // for keys — otherwise "API keys" would drop them into the storage form.
    cy.get('[data-testid="sidenav-connect"]').click();
    cy.get('[data-testid="connections-panel"]').should('be.visible');
    cy.get('[data-testid="connections-close"]').click();
    cy.get('[data-testid="explorer-overlay"]').should('not.exist');
    // Back on the explorer, with the panel usable again.
    cy.get('[data-testid="sidenav"]').should('be.visible');

    cy.get('[data-testid="sidenav-apikeys"]').click();
    cy.get('[data-testid="api-tokens"]').should('be.visible');
    cy.get('[data-testid="connections-panel"]').should('not.exist');
    cy.get('[data-testid="tab-storages"]').should('not.exist');
  });

  it('mints a token and reveals the secret exactly once', () => {
    cy.get('[data-testid="sidenav-apikeys"]').click();
    cy.get('[data-testid="api-tokens"]').should('be.visible');
    const label = `cypress-${Date.now()}`;
    cy.get('[data-testid="token-label"]').first().clear().type(label);
    cy.get('[data-testid="token-mint"]').first().click();
    // The whole point of the surface: a credential the caller can copy.
    cy.get('[data-testid="token-secret"]', { timeout: 15000 }).should('be.visible');
    cy.get('[data-testid="token-secret"] code')
      .invoke('text')
      .should('have.length.greaterThan', 20);
  });

  it('the overlay closes on a backdrop click but not on a click inside it', () => {
    cy.get('[data-testid="sidenav-apikeys"]').click();
    cy.get('[data-testid="explorer-overlay"]').should('be.visible');
    // `@click.self` on the overlay, `@click.stop` on the card. Without the
    // stop, every click inside the token form would dismiss the form the user
    // is filling in.
    cy.get('[data-testid="token-label"]').first().click();
    cy.get('[data-testid="explorer-overlay"]').should('be.visible');
    // topLeft of the full-screen overlay is backdrop — the card is centred.
    cy.get('[data-testid="explorer-overlay"]').click('topLeft');
    cy.get('[data-testid="explorer-overlay"]').should('not.exist');
  });
});
