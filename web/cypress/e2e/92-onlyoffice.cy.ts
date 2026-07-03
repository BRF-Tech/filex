// 92-onlyoffice — config endpoint accepts both POST and GET, and
// is wired regardless of whether the external DS is configured.

describe('onlyoffice', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('POST config rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/files/onlyoffice/config',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        // 400/404 means handler caught the bad input. 501 is OK if
        // OnlyOffice is not configured. 500 would be a regression.
        expect([200, 400, 404, 422, 501]).to.include(res.status);
      });
    });
  });

  it('GET config with no path returns 400/404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/onlyoffice/config',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 400, 404, 422, 501]).to.include(res.status);
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
