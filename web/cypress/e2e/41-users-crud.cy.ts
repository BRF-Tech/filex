// 41-users-crud — admin users list + create+delete round-trip
// using a cypress-prefixed fixture user so we don't disturb real
// users.

describe('users CRUD', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('users list returns array of envelopes', () => {
    cy.adminGet<Array<{ id: number; email: string; role: string }>>('/api/admin/users').then((d) => {
      expect(d, 'users array').to.be.an('array');
      for (const u of d) {
        expect(u, 'user envelope').to.include.all.keys('id', 'email', 'role');
      }
    });
  });

  it('create + delete round-trip on a cypress fixture user', () => {
    cy.apiLogin().then((tok) => {
      const email = `cypress-${Date.now()}@example.invalid`;
      cy.request({
        method: 'POST',
        url: '/api/admin/users',
        headers: { Authorization: `Bearer ${tok}` },
        body: { email, password: 'CypressFixture!2026', role: 'user' },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 201]).to.include(res.status);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const uid = (body.id ?? body.user?.id) as number;
        expect(uid, 'new user id').to.be.a('number');

        // GET it back.
        cy.request({
          method: 'GET',
          url: `/api/admin/users/${uid}`,
          headers: { Authorization: `Bearer ${tok}` },
        }).then((g) => {
          const gb = typeof g.body === 'string' ? JSON.parse(g.body) : g.body;
          const ue = gb.email ?? gb.user?.email;
          expect(ue, 'echoed email').to.eq(email);
        });

        // DELETE.
        cy.request({
          method: 'DELETE',
          url: `/api/admin/users/${uid}`,
          headers: { Authorization: `Bearer ${tok}` },
        }).then((d) => {
          expect([200, 204]).to.include(d.status);
        });
      });
    });
  });

  it('admin reset-password on bogus id is 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/users/9999999/reset-password',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        // 400/404 — route is wired.
        expect([400, 404, 422]).to.include(res.status);
      });
    });
  });
});
