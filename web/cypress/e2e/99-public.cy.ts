// 99-public — apex redirect + healthz + login page + embed JS
// bundle. These run without auth.

describe('public surface', () => {
  it('GET / → 302 /admin/', () => {
    cy.request({
      method: 'GET',
      url: '/',
      followRedirect: false,
      failOnStatusCode: false,
    }).then((res) => {
      expect([301, 302]).to.include(res.status);
      const loc = (res.headers.location as string) ?? '';
      expect(loc).to.contain('/admin');
    });
  });

  it('GET /healthz → 200 + JSON', () => {
    cy.request({
      method: 'GET',
      url: '/healthz',
    }).then((res) => {
      expect(res.status).to.eq(200);
      const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
      expect(body.status, 'healthz.status').to.eq('ok');
    });
  });

  it('GET /admin/login serves the SPA shell', () => {
    cy.request({
      method: 'GET',
      url: '/admin/login',
    }).then((res) => {
      expect(res.status).to.eq(200);
      expect(res.headers['content-type'] as string).to.match(/html/i);
    });
  });

  it('GET /embed.js bundle is reachable + JS content-type', () => {
    cy.request({
      method: 'GET',
      url: '/embed.js',
      failOnStatusCode: false,
    }).then((res) => {
      // 200 if the web-component bundle is embedded, 404 if the build
      // didn't include it. Either way, never 500.
      expect([200, 404]).to.include(res.status);
      if (res.status === 200) {
        const ct = res.headers['content-type'] as string;
        expect(ct).to.match(/javascript/i);
      }
    });
  });

  it('GET /embed.css is reachable or 404', () => {
    cy.request({
      method: 'GET',
      url: '/embed.css',
      failOnStatusCode: false,
    }).then((res) => {
      expect([200, 404]).to.include(res.status);
    });
  });

  it('admin SPA fallback: /admin/this-route-does-not-exist serves index.html', () => {
    cy.request({
      method: 'GET',
      url: '/admin/this-route-does-not-exist-zzz',
    }).then((res) => {
      expect(res.status).to.eq(200);
      expect(res.headers['content-type'] as string).to.match(/html/i);
    });
  });
});
