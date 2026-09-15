// 43-external-test-detail-ui — issue #17, fourth round: the Test button said
// "not reachable" while `curl` from the filex container loaded ONLYOFFICE's
// welcome page. The document server's /healthcheck was a 502 from its own nginx
// (docservice behind it was stopped), and nothing on the page said so — the
// operator went back to checking the network.
//
// The server leg now carries what the probe saw. Measured end to end: the
// ONLYOFFICE slot is pointed at a port nothing listens on, the real Test runs,
// and the connection error is printed under the server leg.

describe('external services: a failed server probe says what it saw (issue #17)', () => {
  let before: { enabled?: boolean; url?: string } = {};

  it('prints the probe detail under the server leg', () => {
    cy.uiLogin();
    cy.apiLogin().then((tok) => {
      const auth = { Authorization: `Bearer ${tok}` };
      cy.request({ url: '/api/admin/external', headers: auth }).then((res) => {
        const row = (res.body.entries ?? []).find((e: { Name: string }) => e.Name === 'onlyoffice');
        before = { enabled: row?.Enabled, url: row?.URL };
      });
      cy.request({
        method: 'PATCH',
        url: '/api/admin/external/onlyoffice',
        headers: auth,
        body: { enabled: true, url: 'http://127.0.0.1:1', secret: 'cypress-detail-secret' },
      });
    });

    cy.visit('/admin/external');
    cy.get('[data-testid="legs-onlyoffice"]').should('be.visible');
    cy.get('[data-testid="legs-onlyoffice"]')
      .parents()
      .filter(':has(button)')
      .first()
      .within(() => {
        cy.contains('button', /test/i).click();
      });

    cy.get('[data-testid="leg-server-detail-onlyoffice"]', { timeout: 15000 })
      .should('be.visible')
      .invoke('text')
      .should('match', /GET http:\/\/127\.0\.0\.1:1\/healthcheck: .*refused/i);
  });

  after(() => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'PATCH',
        url: '/api/admin/external/onlyoffice',
        headers: { Authorization: `Bearer ${tok}` },
        body: { enabled: before.enabled ?? false, url: before.url ?? '', secret: '' },
        failOnStatusCode: false,
      });
    });
  });
});
