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

// The actions column is pinned to the right edge of the table (2026-09-19,
// owner: "admin panelinde bütün tablolarımızda işlemler bölgesi sağda sabit
// kalsın, file explorer içindeki tablolarımız gibi"). Measured in a real
// browser at a width where the Users table is wider than its card: the row's
// Edit button has to sit INSIDE the viewport without any sideways scroll, and
// it has to work from there.
describe('users list: the actions column stays at the right edge', () => {
  it('at 700px the row buttons are inside the viewport and clickable', () => {
    const email = `cypress-pinned-${Date.now()}@example.invalid`;
    cy.uiLogin();
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/users',
        headers: { Authorization: `Bearer ${tok}` },
        body: { email, password: 'CypressFixture!2026', display_name: 'Pinned Actions Fixture', role: 'user' },
      }).then((res) => {
        const uid = (res.body.id ?? res.body.user?.id) as number;

        cy.viewport(700, 900);
        cy.visit('/admin/users');
        cy.get('input[placeholder]').filter(':visible').first().type(email);

        cy.contains('tr', email, { timeout: 10000 }).within(() => {
          cy.get('td.tbl-actions').should('have.css', 'position', 'sticky');
          cy.get('td.tbl-actions').find('button').first().as('edit');
        });
        // Nothing has been scrolled sideways, so the pinned cell being inside
        // the viewport is the sticky offset doing its job, not the user.
        cy.get('.tbl-scroll').filter(':visible').first().invoke('scrollLeft').should('eq', 0);
        cy.get('@edit').should('be.visible');
        cy.get('@edit').then(($btn) => {
          const r = $btn[0].getBoundingClientRect();
          const w = $btn[0].ownerDocument.defaultView!.innerWidth;
          expect(r.right, 'edit button inside the viewport').to.be.at.most(w);
          expect(r.left, 'edit button inside the viewport').to.be.at.least(0);
          expect(r.width, 'edit button has a size').to.be.greaterThan(0);
        });
        cy.get('@edit').click();
        cy.url({ timeout: 10000 }).should('match', new RegExp(`/admin/users/${uid}([?#]|$)`));

        cy.request({ method: 'DELETE', url: `/api/admin/users/${uid}`, headers: { Authorization: `Bearer ${tok}` }, failOnStatusCode: false });
      });
    });
  });
});
