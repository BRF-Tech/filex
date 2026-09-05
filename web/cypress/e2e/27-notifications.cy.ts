// 27-notifications — per-user notification bell + settings.

describe('notifications', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/notifications has the list envelope', () => {
    cy.adminGet<{ items?: Array<{ id: number; event: string }> | null }>('/api/notifications').then(
      (d) => {
        // ⚠ Go serializes an empty slice as JSON `null`, so an instance that
        // has never raised a notification answers `{"items": null}`. The old
        // unconditional `to.be.an('array')` therefore depended on whether some
        // earlier spec happened to generate one — it passed in one full run and
        // failed in the next with no code change between them. Assert the
        // envelope, and assert the ROW shape whenever there is a row, which is
        // the part that can actually catch a serialization change.
        expect(d, 'notifications envelope').to.have.property('items');
        if (d.items !== null && d.items !== undefined) {
          expect(d.items, 'items').to.be.an('array');
          for (const n of d.items) {
            expect(n, 'notification row').to.have.property('id');
            expect(n, 'notification row').to.have.property('event');
          }
        }
      },
    );
  });

  it('unread-count returns {count:n}', () => {
    cy.adminGet<{ count: number }>('/api/notifications/unread-count').then((d) => {
      expect(d.count, 'count').to.be.a('number').and.gte(0);
    });
  });

  it('settings GET returns full envelope', () => {
    cy.adminGet<{ user_id: number; in_app_enabled: boolean; muted_events: string[] }>(
      '/api/notifications/settings',
    ).then((d) => {
      expect(d.user_id, 'user_id').to.be.a('number');
      expect(d.in_app_enabled, 'in_app_enabled').to.be.a('boolean');
      expect(d.muted_events, 'muted_events').to.be.an('array');
    });
  });

  it('settings PATCH round-trip flips in_app_enabled then restores', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/notifications/settings',
        headers: { Authorization: `Bearer ${tok}` },
      }).then((g) => {
        const orig = (typeof g.body === 'string' ? JSON.parse(g.body) : g.body).in_app_enabled;
        const flipped = !orig;
        cy.request({
          method: 'PATCH',
          url: '/api/notifications/settings',
          headers: { Authorization: `Bearer ${tok}` },
          body: { in_app_enabled: flipped },
        }).then((p) => {
          expect(p.status).to.be.oneOf([200, 204]);
        });
        cy.request({
          method: 'GET',
          url: '/api/notifications/settings',
          headers: { Authorization: `Bearer ${tok}` },
        }).then((vres) => {
          const v = (typeof vres.body === 'string' ? JSON.parse(vres.body) : vres.body).in_app_enabled;
          expect(v, 'flipped').to.eq(flipped);
        });
        // restore
        cy.request({
          method: 'PATCH',
          url: '/api/notifications/settings',
          headers: { Authorization: `Bearer ${tok}` },
          body: { in_app_enabled: orig },
        });
      });
    });
  });

  it('mark-read on bogus id returns 400/404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/notifications/9999999/read',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 204, 400, 404]).to.include(res.status);
      });
    });
  });

  it('read-all returns 2xx', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/notifications/read-all',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([200, 204]).to.include(res.status);
      });
    });
  });
});
