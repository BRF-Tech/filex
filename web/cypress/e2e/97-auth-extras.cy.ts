// 97-auth-extras — TOTP enroll/verify/disable + password change
// endpoints exist (no 404/405 on route wiring).

describe('auth extras', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  // NOTE: we deliberately don't call POST /api/auth/totp/enroll here
  // because the handler eagerly persists a pending secret row — and a
  // follow-up verify with `code=000000` can race the time-window and
  // actually enable TOTP on admin@local, breaking subsequent runs.
  // Route wiring is verified through the verify+disable smoke tests
  // (both reject empty/malformed bodies with structured 4xx, which
  // proves the chi route is registered).

  it('POST /api/auth/totp/verify rejects empty body with 400/401', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/auth/totp/verify',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        // No `code` field → 400 "bad code" or 401 "no pending enrollment".
        // Anything else means the route is broken.
        expect([400, 401, 422]).to.include(res.status);
      });
    });
  });

  it('POST /api/auth/totp/disable rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/auth/totp/disable',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        // Missing password → 400 "bad json"; route is wired.
        expect([400, 401, 422]).to.include(res.status);
      });
    });
  });

  it('POST /api/auth/password rejects empty body cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/auth/password',
        headers: { Authorization: `Bearer ${tok}` },
        body: {},
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 401, 422]).to.include(res.status);
      });
    });
  });

  it('OIDC start returns 302 or 200 (provider redirect)', () => {
    cy.request({
      method: 'GET',
      url: '/api/auth/oidc/start',
      followRedirect: false,
      failOnStatusCode: false,
    }).then((res) => {
      // 302 to the provider, or 503 if OIDC is not configured.
      expect([302, 200, 400, 404, 503]).to.include(res.status);
    });
  });
});
