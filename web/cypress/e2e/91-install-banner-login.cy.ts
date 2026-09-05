// 91-install-banner-login — the sign-in page's primary button is reachable.
//
// Twice now the PWA/desktop banner has covered something. It is fixed to the
// bottom centre of the viewport, which is exactly where the sign-in card's
// submit button sits, and it grows: the first regression was fixed by making
// the wrapper `pointer-events-none` (clicks pass through everywhere except the
// card), and the second arrived when the Windows portable download added a
// third row and the card itself reached the button. Both times the symptom was
// a Playwright `locator.click` timeout on the login spec, which reads as a
// flaky test rather than as a covered control.
//
// So this measures the thing that actually matters — can a person sign in —
// rather than the banner's markup, which is what changed both times.

describe('the sign-in page is not covered by the install banner', () => {
  // ⚠ The suite dismisses this banner before every page load (see
  // cypress/support/e2e.ts and its reasons). Opting out is the whole point
  // here: with the dismissal in place this spec passes against a build that
  // renders the banner over the sign-in button, which is a test that measures
  // nothing. Measured: without these two lines it stayed green with the fix
  // reverted.
  before(() => {
    Cypress.env('KEEP_INSTALL_BANNER', true);
  });
  after(() => {
    Cypress.env('KEEP_INSTALL_BANNER', false);
  });

  beforeEach(() => {
    // A returning PC visitor who has never dismissed anything: the state in
    // which the banner is most likely to be offered.
    cy.clearLocalStorage();
  });

  it('the submit button is on top at the widths where the banner overlaps', () => {
    // 390 is a phone; 1280 is the desktop width the banner was measured at.
    for (const [w, h] of [
      [390, 844],
      [1280, 800],
    ] as [number, number][]) {
      cy.viewport(w, h);
      cy.visit('/admin/login');

      cy.get('form button[type="submit"]')
        .should('be.visible')
        .then(($btn) => {
          const r = $btn[0].getBoundingClientRect();
          const x = Math.round(r.left + r.width / 2);
          const y = Math.round(r.top + r.height / 2);
          // What the browser would actually hand the click to. `should('be
          // visible')` does not answer this: an element under an overlay is
          // still visible by Cypress's definition.
          cy.document().then((doc) => {
            const top = doc.elementFromPoint(x, y);
            expect(
              $btn[0].contains(top) || top === $btn[0],
              `at ${w}x${h} the element at the submit button's centre is the ` +
                `button, not <${top?.tagName.toLowerCase()} class="${
                  (top as HTMLElement)?.className ?? ''
                }">`,
            ).to.eq(true);
          });
        });
    }
  });

  it('signing in still works with a clean profile', () => {
    cy.viewport(1280, 800);
    cy.visit('/admin/login');
    cy.get('form button[type="submit"]').click();
    // Whatever the outcome (the form may reject empty credentials), the click
    // has to have reached the button — a covered control times out instead.
    cy.get('form').should('exist');
  });
});
