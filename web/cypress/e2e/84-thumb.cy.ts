// 84-thumb — the thumbnail endpoint's authorization contract.
//
// ⚠ `GET /api/files/thumb/{id}` used to serve a rendered preview of any file on
// the instance to anybody who could count: `sig` was optional, and the signing
// key was never seeded so even a supplied signature was waved through. It now
// takes one of two proofs — a live stamp on the URL (what a header-less <img>
// in a third-party embed carries) or an authenticated caller who clears the
// node's tenancy, confinement and ACL.

describe('thumb', () => {
  it('is refused outright with no credentials and no stamp', () => {
    // ⚠ This case must come BEFORE any login in this spec: once apiLogin has
    // run, cy.request carries the session cookie and would measure the
    // authenticated path instead of the anonymous one.
    cy.request({
      method: 'GET',
      url: '/api/files/thumb/1',
      failOnStatusCode: false,
    }).then((res) => {
      expect(res.status, 'an anonymous caller must never receive image bytes').to.eq(401);
      expect(String(res.headers['content-type'] ?? '')).to.not.contain('image/');
    });
  });

  it('does not accept a junk signature as proof', () => {
    cy.request({
      method: 'GET',
      url: '/api/files/thumb/1?exp=99999999999&sig=deadbeef',
      failOnStatusCode: false,
    }).then((res) => {
      expect(res.status).to.eq(401);
    });
  });

  describe('signed in', () => {
    beforeEach(() => {
      cy.apiLogin();
    });

    it('GET /api/files/thumb/{id} with bogus id is 404 not 500', () => {
      cy.request({
        method: 'GET',
        url: '/api/files/thumb/99999999',
        failOnStatusCode: false,
      }).then((res) => {
        // 404 if not found; 410 if expired; 200 if a real thumb exists.
        expect([200, 404, 410]).to.include(res.status);
      });
    });

    it('GET /api/files/thumb/0 (invalid id) is 400/404', () => {
      cy.request({
        method: 'GET',
        url: '/api/files/thumb/0',
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });

    it('the listing stamps thumb_url so a bare <img> can still render', () => {
      cy.request({ method: 'GET', url: '/api/files/manager?action=index&path=' }).then((res) => {
        const stamped = ((res.body?.files ?? []) as Array<{ thumb_url?: string }>)
          .map((f) => f.thumb_url)
          .filter(Boolean) as string[];
        // No thumbnails on this instance is not a failure — an UNSTAMPED one is.
        stamped.forEach((u) => {
          expect(u, `thumb_url must carry a stamp: ${u}`).to.match(
            /^\/api\/files\/thumb\/\d+\?exp=\d+&sig=[0-9a-f]{64}$/,
          );
        });
      });
    });
  });
});
