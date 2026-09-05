// 92-onlyoffice — config endpoint accepts both POST and GET, and
// is wired regardless of whether the external DS is configured.
//
// ⚠ The expected status DEPENDS ON THE CAPABILITY PROBE, never on a hostname —
// the same rule the Playwright suite follows. With no Document Server
// configured the endpoint answers 503 ("the integration is off"), and with one
// configured it answers 400/422 for a bad body. The old fixed allow-list
// [200, 400, 404, 422, 501] said "501 is OK if OnlyOffice is not configured",
// which is not the code the backend sends: every run against a plain instance
// went red on 503, and the list was so wide that a configured instance could
// answer almost anything and still pass.

/** Is a Document Server actually wired up on this instance? */
function onlyOfficeReady(): Cypress.Chainable<boolean> {
  return cy
    .adminGet<{ external?: Record<string, { enabled?: boolean; state?: string }> }>(
      '/api/files/capabilities',
    )
    .then((d) => {
      const slot = d.external?.onlyoffice;
      return !!slot?.enabled && slot?.state !== 'disabled' && slot?.state !== 'unconfigured';
    });
}

describe('onlyoffice', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('POST config answers 503 when the integration is off, and a 4xx when it is on', () => {
    onlyOfficeReady().then((ready) => {
      cy.apiLogin().then((tok) => {
        cy.request({
          method: 'POST',
          url: '/api/files/onlyoffice/config',
          headers: { Authorization: `Bearer ${tok}` },
          body: {},
          failOnStatusCode: false,
        }).then((res) => {
          const allowed = ready ? [200, 400, 404, 422] : [503];
          expect(allowed, `onlyoffice ${ready ? 'configured' : 'off'} → ${res.status}`).to.include(
            res.status,
          );
        });
      });
    });
  });

  it('GET config with no path answers the same way (route wired)', () => {
    onlyOfficeReady().then((ready) => {
      cy.apiLogin().then((tok) => {
        cy.request({
          method: 'GET',
          url: '/api/files/onlyoffice/config',
          headers: { Authorization: `Bearer ${tok}` },
          failOnStatusCode: false,
        }).then((res) => {
          const allowed = ready ? [200, 400, 404, 422] : [503];
          expect(allowed, `onlyoffice ${ready ? 'configured' : 'off'} → ${res.status}`).to.include(
            res.status,
          );
        });
      });
    });
  });

  it('capabilities advertises onlyoffice config slot', () => {
    cy.adminGet<{
      external?: Record<string, { enabled?: boolean; state?: string; configured?: boolean }>;
    }>('/api/files/capabilities').then((d) => {
      expect(d.external?.onlyoffice, 'onlyoffice slot').to.exist;
    });
  });
});
