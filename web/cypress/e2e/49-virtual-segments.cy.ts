// 49-virtual-segments — the four virtual views must READ as their names, on
// every surface that prints a path segment.
//
// The defect, reported 2026-09-04: the tab strip said `.shared`. Not a typo — a
// second copy of a mapping. `.trash` predates Recent/Starred/Shared and had its
// translation written twice, once in Breadcrumb.vue and once in
// FileExplorer.tabLabel. When the other three arrived only the breadcrumb copy
// was extended, so v0.30.0 shipped a strip reading ".shared" at users while the
// breadcrumb beside it read "Shared with me". Then the inspector turned out to
// be a THIRD copy and headed itself ".starred".
//
// So the fix was one map — VIRTUAL_SEGMENTS / virtualSegmentKey() /
// virtualSegmentLabel() in packages/core/src/lib/listing.ts — and the guard has
// to be positive and per-surface. 14-explorer-sidenav already asserts the
// negative ("no sentinel anywhere"); this file asserts the POSITIVE, which is
// the stronger statement: a surface that rendered nothing at all, or fell back
// to a folder name, would pass a not-a-sentinel check and fail here.
//
// ⚠ All four views, all three surfaces, because the bug WAS "a surface somebody
// forgot". A spec that checked the tab strip only would have gone green while
// the inspector printed `.starred`.

/** The English name each sentinel has to read as, from
 *  packages/core/src/locales/en.ts. The keys are the sentinels themselves. */
const VIEWS = {
  recent: { sentinel: '.recent', label: 'Recent' },
  starred: { sentinel: '.starred', label: 'Starred' },
  shared: { sentinel: '.shared', label: 'Shared with me' },
  trash: { sentinel: '.trash', label: 'Trash' },
} as const;

function pin(win: Window) {
  win.localStorage.setItem('filex.locale', 'en');
  win.localStorage.setItem('filex.sidenav', '1');
  win.localStorage.setItem('brf-file-explorer:view-mode', 'list');
  // koru:k1 — the inspector is one of the three surfaces under test and its
  // open/closed state is a user preference (`filex.inspector`). Open it before
  // the app boots rather than clicking a toolbar button that may have folded
  // into the overflow menu at this viewport.
  win.localStorage.setItem('filex.inspector', '1');
}

describe('virtual view labels', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/drive/explore', { onBeforeLoad: pin });
    cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('be.visible');
    // ⚠ The tab strip is not rendered with a single tab, so a second one has to
    // exist before the strip is a surface at all. Without this step every
    // assertion about it would pass without ever looking at it.
    // ⚠ Type on <body>: the shortcut listener is on `window` and ignores events
    // whose target is an input or a button.
    cy.get('body').type('{ctrl}t');
    cy.get('.fe-tabs').should('be.visible');
    cy.get('.fe-tabs__tab').should('have.length.greaterThan', 1);
    cy.get('.fe-inspector', { timeout: 15000 }).should('be.visible');
  });

  for (const [view, { sentinel, label }] of Object.entries(VIEWS)) {
    it(`${view} reads as "${label}" in the tab strip, the breadcrumb AND the inspector`, () => {
      cy.get(`[data-testid="sidenav-view-${view}"]`).click();
      cy.get(`[data-testid="sidenav-view-${view}"]`, { timeout: 15000 }).should(
        'have.attr',
        'aria-current',
        'page',
      );

      // 1. The tab strip — the surface the bug was reported on. The ACTIVE tab,
      //    not "some tab": the second tab still sits on the folder it was
      //    opened from, so `contain.text` over the whole strip would pass on
      //    the wrong element.
      cy.get('.fe-tabs__tab.is-active .fe-tabs__label')
        .invoke('text')
        .then((t) => expect(t.trim(), 'active tab label').to.eq(label));

      // 2. The breadcrumb — the copy that was RIGHT, and therefore the one that
      //    made the disagreement visible ("Shared with me" beside ".shared").
      cy.get('.fe-breadcrumb').should('contain.text', label);

      // 3. The inspector — the third copy, which headed itself ".starred".
      //    Its folder-summary heading only renders with nothing selected, which
      //    is the state a view opens in.
      cy.get('.fe-inspector__name')
        .invoke('text')
        .then((t) => expect(t.trim(), 'inspector heading').to.eq(label));

      // And no surface anywhere prints the raw sentinel.
      cy.get('.fe').should(($fe) => {
        expect($fe.text(), `no raw ${sentinel} on screen`).to.not.include(sentinel);
      });
    });
  }

  it('the sentinel really is in the path — the labels are a translation, not a rename', () => {
    // ⚠ Without this the whole file could pass against a build that stopped
    // using sentinels at all, which would be a different product and would
    // silently drop every deep link. `pathPersist: 'hash+localStorage'` puts
    // the current path in the address bar, so the hash is where the sentinel
    // is observable from outside the component.
    cy.get('[data-testid="sidenav-view-starred"]').click();
    cy.get('[data-testid="sidenav-view-starred"]', { timeout: 15000 }).should(
      'have.attr',
      'aria-current',
      'page',
    );
    cy.hash().should('include', '.starred');
  });
});
