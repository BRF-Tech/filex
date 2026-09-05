// 46-star-action — starring as a real ACTION, and the gate that hides it.
//
// Two halves, and the second is the one that has already been wrong:
//
//   1. A star is reachable the way a tag is: the context menu, the toolbar,
//      the `S` key, and a chip on the card in grid AND gallery — not only the
//      badge in the list column. The Starred view then lists what was starred.
//
//   2. When the identity surfaces are suppressed — `caller_kind: "app"` from
//      /api/files/capabilities, i.e. a shared token a host's proxy injects for
//      every visitor — the star affordance disappears EVERYWHERE AT ONCE.
//      Starring under a shared token writes into a list that has no single
//      person behind it, and the panel already leaves the Starred view out, so
//      the offer led somewhere the person could not go.
//
// ⚠ Why the app case is driven by rewriting `caller_kind` on the capabilities
// response rather than by logging in with a real app token: the SPA
// authenticates with a session, and `caller_kind` is the ONE signal the
// component reads (FileExplorer.callerIsApp → identitySurfaces →
// starableNodes + the `star-enabled` prop on the three views). Rewriting the
// server's own answer drives the real code path with the real value; a spec
// that stubbed `starableNodes` would be testing itself. The HTTP half of the
// same feature — that an app token is actually refused by the server — is
// 64-token-kinds.
//
// ⚠ The three view modes are asserted inside ONE test each, by driving the
// toolbar's own view switcher rather than reloading. A `cy.visit` of the
// explorer costs ~3s here; three of them per polarity would have added ~12s to
// a suite whose whole point is that it is fast enough to gate every push.
//
// ⚠ Fixture files are created through save-text and left behind on purpose:
// the instance is thrown away after the run, and deleting them would make the
// Starred view assertion depend on the order specs happen to run in.

const STORAGE = () => (Cypress.env('SEEDED_STORAGE') as string) || '';

/** A deterministic file this spec owns, so no assertion depends on whatever
 *  another spec left in the storage. */
const FIXTURE = 'cypress-star-fixture.txt';

/** Force English + a known explorer state before the app boots. The suite
 *  flips the account's locale to `tr` in 17-theme-locale, and every string
 *  asserted below is a product string — so the locale has to be pinned here
 *  rather than assumed. `filex.locale` is the product's own key (web/src/i18n). */
function pin(win: Window) {
  win.localStorage.setItem('filex.locale', 'en');
  win.localStorage.setItem('brf-file-explorer:view-mode', 'list');
  win.localStorage.setItem('filex.sidenav', '1');
}

/** Rewrite the server's own capabilities answer so the explorer sees a shared
 *  app token. Everything else on the response is left exactly as the server
 *  sent it — this is `req.continue`, not a canned fixture, so a change to the
 *  envelope cannot make the case pass for the wrong reason. */
function asAppToken() {
  cy.intercept('GET', '**/api/files/capabilities', (req) => {
    req.continue((res) => {
      const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
      body.caller_kind = 'app';
      res.send(body);
    });
  }).as('caps');
}

/** Open the explorer on the seeded storage, list view, fixture row on screen. */
function openStorage() {
  cy.visit('/drive/explore', { onBeforeLoad: pin });
  cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('be.visible');
  cy.get(`[data-testid="sidenav-storage-${STORAGE()}"]`).click();
  cy.get(`[data-fe-path$="${FIXTURE}"]`, { timeout: 20000 }).should('exist');
}

/** Switch the explorer to a view mode through the toolbar's own switcher, and
 *  wait until that view has painted the fixture.
 *  ⚠ GalleryView carries no `data-fe-path` (only ListView and GridView do), so
 *  the gallery card is found by its label — reported as a separate nit. */
function switchTo(mode: 'list' | 'grid' | 'gallery') {
  const label = { list: 'List', grid: 'Grid', gallery: 'Gallery' }[mode];
  // The switcher is a `role="tablist"` of three icon buttons whose only text
  // is their aria-label — which is why the locale is pinned to `en` above.
  cy.get(`.fe-toolbar__view [role="tab"][aria-label="${label}"]`).click();
  cy.get(`.fe-toolbar__view [role="tab"][aria-label="${label}"]`).should(
    'have.attr',
    'aria-selected',
    'true',
  );
  if (mode === 'gallery') {
    cy.get('.fe-gal__label', { timeout: 15000 }).should('contain.text', FIXTURE);
  } else {
    cy.get(`[data-fe-path$="${FIXTURE}"]`, { timeout: 15000 }).should('exist');
  }
}

/** The fixture's card in whichever view is on screen. */
function fixtureCard(mode: 'list' | 'grid' | 'gallery') {
  return mode === 'gallery'
    ? cy.contains('.fe-gal__label', FIXTURE).parents('.fe-gal__card').first()
    : cy.get(`[data-fe-path$="${FIXTURE}"]`);
}

describe('star action', () => {
  before(() => {
    // One fixture file for the whole file. save-text is the cheapest way to
    // put a real, indexed node in the seeded storage (68-manager-mutate uses
    // the same trick).
    cy.apiLogin().then((tok) => {
      const adapter = STORAGE();
      if (!adapter) return;
      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: { Authorization: `Bearer ${tok}` },
        body: { path: `${adapter}://${FIXTURE}`, content: 'star fixture' },
        failOnStatusCode: false,
      });
    });
  });

  beforeEach(function () {
    if (!STORAGE()) {
      // ⚠ Not a hedge: without the harness there is no storage to put a file
      // in, and every assertion below is about a row. Run via
      // `node e2e/run.mjs cypress`.
      this.skip();
    }
    cy.apiLogin();
  });

  // ── the affordance exists, in every view ──────────────────────────────

  it('offers the star chip on the card in list, grid AND gallery', () => {
    openStorage();
    for (const mode of ['list', 'grid', 'gallery'] as const) {
      switchTo(mode);
      // ⚠ Scoped to the fixture's own card. A bare `star-toggle` count would
      // pass on any other file's star and say nothing about this row.
      fixtureCard(mode)
        .find('[data-testid="star-toggle"]')
        .should('exist')
        .and('have.attr', 'aria-pressed');
    }
  });

  it('offers Star in the context menu, and starring writes it through', () => {
    cy.intercept('POST', '**/api/files/manager/star').as('starPost');
    openStorage();
    cy.get(`[data-fe-path$="${FIXTURE}"]`).rightclick();
    cy.get('.fe-ctx', { timeout: 10000 }).should('be.visible');
    cy.get('.fe-ctx__label').should('contain.text', 'Star');
    cy.get('.fe-ctx__item').contains(/^Star$/).click();
    cy.wait('@starPost').its('response.statusCode').should('eq', 200);
    // The server, not the optimistic local flag, is the assertion.
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/manager/star/list?limit=100',
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const names = (body.nodes ?? []).map((n: { name: string }) => n.name);
        expect(names, 'starred list holds the fixture').to.include(FIXTURE);
      });
    });
  });

  it('offers the star in the toolbar for a selection', () => {
    openStorage();
    cy.get(`[data-fe-path$="${FIXTURE}"]`).click();
    // ⚠ `.fe-toolbar__measure` and not the visible strip: the toolbar folds
    // whatever does not fit into a "⋯" menu, so which buttons are on screen
    // is a function of the viewport. The measurement strip always renders
    // EVERY entry of `toolbarActions`, which is the list under test.
    cy.get('.fe-toolbar__measure .fe-btn__label').should(($l) => {
      const labels = $l.toArray().map((e) => (e.textContent || '').trim());
      expect(labels, 'toolbar offers star/unstar').to.satisfy((ls: string[]) =>
        ls.some((s) => /^(Star|Unstar)$/.test(s)),
      );
    });
  });

  it('the S key stars the selection', () => {
    cy.intercept('POST', '**/api/files/manager/star').as('starPost');
    openStorage();
    cy.get(`[data-fe-path$="${FIXTURE}"]`).click();
    // ⚠ On <body>: the shortcut listener is on `window` and ignores events
    // whose target is an input or a button (so it never eats a keystroke meant
    // for a filename), which is why 14-explorer-sidenav types on body too.
    cy.get('body').type('s');
    cy.wait('@starPost').its('response.statusCode').should('eq', 200);
  });

  it('the Starred view lists what was starred', () => {
    // Star over the API too, so this case does not depend on spec order.
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: `/api/files/manager?action=index&path=${STORAGE()}://`,
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const row = (body.files ?? []).find(
          (f: { basename: string }) => f.basename === FIXTURE,
        );
        expect(row, `${FIXTURE} is in the storage`).to.exist;
        cy.request({
          method: 'POST',
          url: '/api/files/manager/star',
          headers: { Authorization: `Bearer ${tok}` },
          body: { node_id: row.id, starred: true },
        });
      });
    });
    cy.visit('/drive/explore', { onBeforeLoad: pin });
    cy.get('[data-testid="sidenav-view-starred"]', { timeout: 20000 }).click();
    cy.get('[data-testid="sidenav-view-starred"]', { timeout: 15000 }).should(
      'have.attr',
      'aria-current',
      'page',
    );
    cy.get('.fe', { timeout: 15000 }).should('contain.text', FIXTURE);
  });

  // ── the gate: an app token gets no star anywhere ──────────────────────

  describe('suppressed for a shared app token', () => {
    beforeEach(() => {
      asAppToken();
    });

    it('the panel leaves the Starred view out (the premise of the gate)', () => {
      openStorage();
      cy.get('[data-testid="sidenav-view-starred"]').should('not.exist');
      // ⚠ Trash and the storages stay: suppression is the identity-bearing
      // surfaces only, not "the panel becomes read-only".
      cy.get('[data-testid="sidenav-view-trash"]').should('exist');
      cy.get(`[data-testid="sidenav-storage-${STORAGE()}"]`).should('exist');
    });

    it('no star chip in list, grid OR gallery', () => {
      // ⚠ This is the case the fix was actually about. Gating the list column
      // alone leaves the grid and gallery chips offering to write into a list
      // the visitor cannot open — which is what shipped before `starEnabled`.
      openStorage();
      for (const mode of ['list', 'grid', 'gallery'] as const) {
        switchTo(mode);
        // Whole-view, not row-scoped: a star on ANY card is the bug.
        cy.get('[data-testid="star-toggle"]').should('not.exist');
      }
    });

    it('no Star entry in the context menu', () => {
      openStorage();
      cy.get(`[data-fe-path$="${FIXTURE}"]`).rightclick();
      cy.get('.fe-ctx', { timeout: 10000 }).should('be.visible');
      // The menu still has to be a menu — asserting only "no Star" would pass
      // against a menu that failed to render at all.
      cy.get('.fe-ctx__label').should('have.length.greaterThan', 3);
      cy.get('.fe-ctx__label').each(($l) => {
        expect(($l.text() || '').trim(), 'context menu offers no star').to.not.match(
          /^(Star|Unstar)$/,
        );
      });
    });

    it('no star in the toolbar', () => {
      openStorage();
      cy.get(`[data-fe-path$="${FIXTURE}"]`).click();
      cy.get('.fe-toolbar__measure .fe-btn__label').should(($l) => {
        const labels = $l.toArray().map((e) => (e.textContent || '').trim());
        expect(labels, 'toolbar still shows the other selection actions').to.have.length
          .greaterThan(2);
        expect(labels, 'toolbar offers no star').to.satisfy((ls: string[]) =>
          ls.every((s) => !/^(Star|Unstar)$/.test(s)),
        );
      });
    });

    it('the S key does nothing', () => {
      cy.intercept('POST', '**/api/files/manager/star').as('starPost');
      openStorage();
      cy.get(`[data-fe-path$="${FIXTURE}"]`).click();
      cy.get('body').type('s');
      // ⚠ A negative on a network call needs something to wait for, or it
      // asserts "the request had not happened yet". Toggling the inspector is
      // another keyboard action on the same listener: once its effect is on
      // screen, the keydown pipeline has demonstrably run.
      cy.get('body').type('i');
      cy.get('.fe-inspector', { timeout: 10000 }).should('exist');
      cy.get('@starPost.all').should('have.length', 0);
    });
  });
});
