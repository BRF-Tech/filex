// 48-tags-panel — tags are BROWSABLE from the navigation panel.
//
// Tagging a file existed long before this: the tag picker wrote rows and
// `/api/files/manager/tagged` could list them, but nothing in the explorer
// ever asked. A tag you can write and cannot read is a label that only the
// database sees, so the panel now lists every tag between the views and the
// storages, and clicking one opens a cross-storage listing of what carries it.
//
// The listing is virtual: no folder exists behind a tag, so the explorer parks
// a `.tag~<name>` sentinel in the path (lib/listing.ts, TAG_SEGMENT_PREFIX) and
// every surface that prints a path segment renders it as `#<name>`. That is the
// same mechanism — and the same hazard — as the four static views, which is why
// the label side is measured in 49-virtual-segments and only the panel
// behaviour is measured here.
//
// What each case would catch:
//   - the panel never fetching `/api/files/manager/tags/all`  → no rows listed
//   - `@open-tag` not wired to loadTagView                    → click does nothing
//   - the tag listing hitting the wrong endpoint or dropping  → the tagged file
//     the cross-storage rows                                    is not on screen

const STORAGE = () => (Cypress.env('SEEDED_STORAGE') as string) || '';

/** Lower case on purpose: `Meta.SetTags` lower-cases every tag it stores, so a
 *  fixture with a capital would be asserting against a name the server never
 *  keeps. */
const TAG = 'cypressinvoices';
const FIXTURE = 'cypress-tagged-fixture.txt';

function pin(win: Window) {
  win.localStorage.setItem('filex.locale', 'en');
  win.localStorage.setItem('filex.sidenav', '1');
  win.localStorage.setItem('brf-file-explorer:view-mode', 'list');
}

describe('tags in the navigation panel', () => {
  before(function () {
    if (!STORAGE()) this.skip();
    // Create a file, find its node id in the storage listing, tag it.
    cy.apiLogin().then((tok) => {
      const h = { Authorization: `Bearer ${tok}` };
      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: h,
        body: { path: `${STORAGE()}://${FIXTURE}`, content: 'tag fixture' },
      });
      cy.request({
        method: 'GET',
        url: `/api/files/manager?action=index&path=${STORAGE()}://`,
        headers: h,
      }).then((res) => {
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const row = (body.files ?? []).find(
          (f: { basename: string }) => f.basename === FIXTURE,
        );
        expect(row, `${FIXTURE} is in the storage`).to.exist;
        cy.request({
          method: 'POST',
          url: '/api/files/manager/tags',
          headers: h,
          body: { node_id: row.id, tags: [TAG] },
        })
          .its('status')
          .should('eq', 200);
      });
    });
  });

  beforeEach(function () {
    if (!STORAGE()) this.skip();
    cy.apiLogin();
    cy.visit('/drive/explore', { onBeforeLoad: pin });
    cy.get('[data-testid="sidenav"]', { timeout: 20000 }).should('be.visible');
  });

  it('the panel lists the tags that exist', () => {
    // ⚠ The Tags section is filled from `/api/files/manager/tags/all` AFTER the
    // first listing paints, not during mount (lib/tags.ts caches it for a
    // minute across every explorer on the page), so the row needs its own wait.
    cy.get(`[data-testid="sidenav-tag-${TAG}"]`, { timeout: 20000 })
      .should('be.visible')
      .and('contain.text', TAG);
  });

  it('clicking a tag opens that tag’s contents, across storages', () => {
    cy.get(`[data-testid="sidenav-tag-${TAG}"]`, { timeout: 20000 }).click();
    // The row reports itself current, the way a view row does — the panel and
    // the pane have to agree about what is on screen.
    cy.get(`[data-testid="sidenav-tag-${TAG}"]`, { timeout: 15000 }).should(
      'have.attr',
      'aria-current',
      'page',
    );
    // And the listing is the tag's, not the folder it came from.
    cy.get('.fe', { timeout: 15000 }).should('contain.text', FIXTURE);
    // ⚠ The tag listing spans every storage, so it must NOT claim a storage
    // crumb (loadTagView clears `adapter`). A regression that kept the adapter
    // would head a cross-storage list with one storage's name.
    cy.get('.fe-breadcrumb').should('not.contain.text', STORAGE());
  });

  it('the tag view parks a `.tag~` sentinel and never shows it raw', () => {
    cy.get(`[data-testid="sidenav-tag-${TAG}"]`, { timeout: 20000 }).click();
    cy.get(`[data-testid="sidenav-tag-${TAG}"]`, { timeout: 15000 }).should(
      'have.attr',
      'aria-current',
      'page',
    );
    // The address bar carries the sentinel — that is what makes the view a
    // shareable deep link (`pathPersist: 'hash+localStorage'`).
    cy.hash().should('include', `.tag~${TAG}`);
    // …and nothing on screen prints it.
    cy.get('.fe').should(($fe) => {
      expect($fe.text(), 'no raw tag sentinel on screen').to.not.include('.tag~');
    });
    cy.get('.fe-breadcrumb').should('contain.text', `#${TAG}`);
  });
});
