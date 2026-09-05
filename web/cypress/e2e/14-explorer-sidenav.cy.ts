// 14-explorer-sidenav — the navigation panel shipped in v0.30.0 (GitHub #14).
//
// The panel is the explorer's left column: the primary actions, the four
// virtual views (Recent / Starred / Shared with me / Trash), the storages the
// caller can see, and the two Connections entries. It collapses to an icon
// RAIL rather than to nothing, and under 560px it becomes a drawer over the
// listing.
//
// Nothing here is a screenshot check — each case pins a behaviour that has
// already been reasoned about in the source and can break silently:
//   - collapse must go to a rail, not to nothing: a panel that vanishes takes
//     its own re-open affordance with it
//   - the collapsed choice is remembered (localStorage `filex.sidenav`), and a
//     *drawer* must NOT be, or a 390px screen reopens it over the files on
//     every mount
//   - a view row has to report itself as current, or the panel and the pane
//     disagree about what is on screen
//   - the panel is never gated on role or uiProfile

const seeded = () => (Cypress.env('SEEDED_STORAGE') as string) || '';

describe('explorer navigation panel', () => {
  beforeEach(() => {
    cy.apiLogin();
    // Start from a known panel state. The component reads this at construction
    // and there is no prop to force it, so the test has to own the key.
    cy.visit('/drive/explore', {
      onBeforeLoad(win) {
        win.localStorage.setItem('filex.sidenav', '1');
      },
    });
    cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('be.visible');
  });

  it('renders expanded, with labelled destinations', () => {
    cy.get('[data-testid="sidenav"]').should('not.have.class', 'fe-sidenav--rail');
    // Expanded means text, not just glyphs — the whole point of the panel.
    cy.get('[data-testid="sidenav-view-recent"]').find('.fe-sidenav__text').should('be.visible');
    for (const view of ['recent', 'starred', 'shared', 'trash']) {
      cy.get(`[data-testid="sidenav-view-${view}"]`).should('be.visible');
    }
    cy.get('[data-testid="sidenav-upload"]').should('be.visible');
    cy.get('[data-testid="sidenav-new-folder"]').should('be.visible');
  });

  it('collapses to a RAIL — icons stay, labels go, every row is still clickable', () => {
    cy.get('[data-testid="sidenav-toggle"]').click();
    cy.get('[data-testid="sidenav"]').should('have.class', 'fe-sidenav--rail');
    // The rail is the load-bearing half of the design: the panel must still be
    // there. A collapse that unmounts it would pass a "labels are gone" check
    // and destroy the feature.
    cy.get('[data-testid="sidenav-view-recent"]').should('be.visible');
    cy.get('[data-testid="sidenav-view-recent"]').find('.fe-sidenav__text').should('not.exist');
    // …and still reachable, which is why the label lives in title/aria-label.
    cy.get('[data-testid="sidenav-view-recent"]')
      .should('have.attr', 'aria-label')
      .and('not.be.empty');
  });

  it('remembers the collapsed choice across a reload', () => {
    cy.get('[data-testid="sidenav-toggle"]').click();
    cy.window().its('localStorage').invoke('getItem', 'filex.sidenav').should('eq', '0');
    cy.reload();
    cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('have.class', 'fe-sidenav--rail');
    cy.get('[data-testid="sidenav-toggle"]').click();
    cy.get('[data-testid="sidenav"]').should('not.have.class', 'fe-sidenav--rail');
    cy.window().its('localStorage').invoke('getItem', 'filex.sidenav').should('eq', '1');
  });

  // The three cross-storage views. On a fresh instance they are empty, and the
  // empty STATE is the assertion: `empty-<view>` only renders when the view
  // actually loaded and returned zero rows, so a view whose endpoint 404s or
  // whose rows fail to map never reaches it.
  for (const view of ['recent', 'starred', 'shared'] as const) {
    it(`opens the ${view} view and marks its row current`, () => {
      cy.get(`[data-testid="sidenav-view-${view}"]`).click();
      cy.get(`[data-testid="sidenav-view-${view}"]`, { timeout: 15000 }).should(
        'have.attr',
        'aria-current',
        'page',
      );
      cy.get(`[data-testid="empty-${view}"]`, { timeout: 15000 }).should('be.visible');
    });
  }

  it('opens the trash view', () => {
    // Trash is the one view that is per-storage and goes through loadTrash(),
    // so it gets no `empty-trash` state — the row's own current-ness is the
    // measurement. ⚠ navView is set AFTER loadTrash resolves; a regression that
    // sets it before leaves this row dark.
    cy.get('[data-testid="sidenav-view-trash"]').click();
    cy.get('[data-testid="sidenav-view-trash"]', { timeout: 15000 }).should(
      'have.attr',
      'aria-current',
      'page',
    );
  });

  it('a view never leaves its raw sentinel on screen (v0.30.1 regression)', () => {
    // The virtual views park a sentinel in the path (`.recent`, `.starred`,
    // `.shared`, `.trash`) and every surface that prints a path segment has to
    // translate it. That map used to exist TWICE — Breadcrumb.vue had all four,
    // FileExplorer.tabLabel special-cased `.trash` only — so v0.30.0 shipped a
    // tab strip that read ".shared" at users while the breadcrumb beside it read
    // "Shared with me". Fixed in v0.30.1 by moving the map to lib/listing.ts.
    //
    // ⚠ The tab strip is not rendered with a single tab, so this opens a second
    // one first (Ctrl+T → onTabNew). Without that step the case would pass
    // without ever looking at the surface the bug was on.
    // ⚠ The listener is on `window` and it ignores events whose target is an
    // input or a button, so type on <body>. Clicking `.fe` first does not work:
    // its own host wrapper covers it and Cypress refuses the click.
    cy.get('body').type('{ctrl}t');
    cy.get('.fe-tabs').should('be.visible');
    cy.get('.fe-tabs__tab').should('have.length.greaterThan', 1);

    for (const view of ['recent', 'starred', 'shared', 'trash'] as const) {
      cy.get(`[data-testid="sidenav-view-${view}"]`).click();
      cy.get(`[data-testid="sidenav-view-${view}"]`, { timeout: 15000 }).should(
        'have.attr',
        'aria-current',
        'page',
      );
      cy.get('.fe-tabs__label').each(($l) => {
        const text = ($l.text() || '').trim();
        expect(text, 'tab label is a name, not a sentinel').to.not.match(
          /^\.(recent|starred|shared|trash)$/,
        );
        expect(text, 'tab label is not empty').to.not.eq('');
      });
      // The breadcrumb is the other call site of the same map.
      cy.get('.fe').should(($fe) => {
        expect($fe.text(), 'no raw sentinel anywhere in the explorer').to.not.match(
          /\.(recent|starred|shared|trash)\b/,
        );
      });
    }
  });

  it('lists the configured storages and opens one', function () {
    const name = seeded();
    if (!name) {
      // ⚠ Not a hedge: without the harness there is no deterministic storage to
      // click, and asserting on "whatever is first" is how a suite starts
      // passing for the wrong reason. Run via `node e2e/run.mjs cypress`.
      this.skip();
    }
    cy.get(`[data-testid="sidenav-storage-${name}"]`).should('be.visible').click();
    // Opening a storage leaves the virtual views: none of them may stay current.
    cy.get('[data-testid="sidenav-view-recent"]').should('not.have.attr', 'aria-current');
    cy.get(`[data-testid="sidenav-storage-${name}"]`).should('have.class', 'is-active');
  });
});

describe('explorer navigation panel — narrow (drawer)', () => {
  beforeEach(() => {
    cy.apiLogin();
    // 390px is an iPhone-class width; the component switches at a CONTAINER
    // width of 560 via ResizeObserver, so a viewport this size puts the
    // explorer into narrow mode.
    cy.viewport(390, 844);
    cy.visit('/drive/explore');
    cy.get('.fe', { timeout: 20000 }).should('have.class', 'fe--narrow');
  });

  it('is closed by default — a column at 390px would leave the files 158px', () => {
    cy.get('[data-testid="sidenav"]').should('not.exist');
  });

  it('the toolbar toggle opens it as a drawer, and the scrim closes it', () => {
    cy.get('[data-testid="toolbar-nav"]').filter(':visible').first().click();
    cy.get('[data-testid="sidenav"]').should('be.visible').and('have.class', 'fe-sidenav--drawer');
    // In drawer mode the labels are shown regardless of the collapsed choice —
    // a rail inside an overlay would cover the listing and show only icons.
    cy.get('[data-testid="sidenav-view-recent"]').find('.fe-sidenav__text').should('be.visible');
    cy.get('.fe-sidenav__scrim').should('exist').click({ force: true });
    cy.get('[data-testid="sidenav"]').should('not.exist');
  });

  it('the drawer is NOT remembered across a reload', () => {
    cy.get('[data-testid="toolbar-nav"]').filter(':visible').first().click();
    cy.get('[data-testid="sidenav"]').should('be.visible');
    cy.reload();
    cy.get('.fe', { timeout: 20000 }).should('have.class', 'fe--narrow');
    // ⚠ The regression this guards: reusing the desktop collapse ref for the
    // drawer. That version reopens the drawer on top of the files every single
    // time the explorer mounts on a phone.
    cy.get('[data-testid="sidenav"]').should('not.exist');
  });

  it('choosing a view closes the drawer', () => {
    cy.get('[data-testid="toolbar-nav"]').filter(':visible').first().click();
    cy.get('[data-testid="sidenav-view-recent"]').click();
    cy.get('[data-testid="sidenav"]').should('not.exist');
    cy.get('[data-testid="empty-recent"]', { timeout: 15000 }).should('be.visible');
  });
});
