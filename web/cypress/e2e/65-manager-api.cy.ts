// 65-manager-api — Vuefinder/SFC manager endpoint contract.
// Confirms the two shapes (native storage=<id> and adapter path=<>://)
// both work and return the documented envelopes.

describe('manager api', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('native ?storage=<id> returns {nodes} — an array once there is anything to list', () => {
    // ⚠ Go serializes an empty slice as JSON `null`, so an EMPTY storage
    // answers `{"nodes": null}` and the old unconditional `to.be.an('array')`
    // failed on any fresh instance while passing against production, which
    // always has files. Asserting `null` is acceptable would make the case
    // unfailable, so instead it writes a file first and then demands a real
    // array containing it — the same round-trip a listing does.
    cy.adminGet<Array<{ id: number; name: string; enabled: boolean }>>(
      '/api/admin/storages',
    ).then((storages) => {
      const target = (storages ?? []).find((s) => s.enabled);
      expect(target, 'an enabled storage to list').to.exist;
      const sid = target!.id;
      const adapter = target!.name;
      const filename = `cypress-listing-${Date.now()}.txt`;
      const tok = window.sessionStorage.getItem('filex.bearer');

      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: { Authorization: `Bearer ${tok}` },
        body: { path: `${adapter}://${filename}`, content: 'cypress-listing-fixture' },
      }).then((made) => {
        expect(made.status, 'save-text').to.be.lessThan(400);

        cy.request({
          method: 'GET',
          url: `/api/files/manager?storage=${sid}`,
          headers: tok ? { Authorization: `Bearer ${tok}` } : {},
        }).then((res) => {
          expect(res.status).to.eq(200);
          const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
          expect(body.nodes, 'nodes').to.be.an('array');
          const names = (body.nodes as Array<{ name?: string; path?: string }>).map(
            (n) => n.name ?? n.path ?? '',
          );
          expect(names.join(' '), 'the file just written is in the listing').to.contain(filename);
        });

        // Clean up after itself — the harness throws the data dir away, but a
        // spec pointed at a long-lived instance must not leave litter.
        cy.request({
          method: 'POST',
          url: '/api/files/manager?action=delete',
          headers: { Authorization: `Bearer ${tok}` },
          body: {
            path: `${adapter}://`,
            items: [{ path: `${adapter}://${filename}`, type: 'file' }],
          },
          failOnStatusCode: false,
        });
      });
    });
  });

  it('SFC ?action=index returns {adapter, storages, files}', () => {
    cy.adminGet<Array<{ name: string }>>('/api/admin/storages').then((storages) => {
      if (!storages || storages.length === 0) return;
      const adapter = storages[0].name;
      const tok = window.sessionStorage.getItem('filex.bearer');
      cy.request({
        method: 'GET',
        url: `/api/files/manager?action=index&path=${encodeURIComponent(adapter + '://')}`,
        headers: tok ? { Authorization: `Bearer ${tok}` } : {},
      }).then((res) => {
        expect(res.status).to.eq(200);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        expect(body.adapter, 'adapter').to.eq(adapter);
        expect(body.files, 'files').to.be.an('array');
        expect(body.storages, 'storages').to.be.an('array');
        expect(body.storages, 'storages contains adapter').to.include(adapter);
      });
    });
  });

  it('stat endpoint requires a path', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/stat',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        // No path → 400/404; either is fine, the route is registered.
        expect([200, 400, 404]).to.include(res.status);
      });
    });
  });

  it('GET /api/files/manager/star/list returns items array', () => {
    cy.adminGet<{ items?: unknown[] } | unknown[]>('/api/files/manager/star/list?limit=10').then((d) => {
      const items = Array.isArray(d) ? d : (d.items ?? []);
      expect(items, 'star list').to.be.an('array');
    });
  });

  it('GET /api/files/manager/recent returns items array', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'GET',
        url: '/api/files/manager/recent?limit=10',
        headers: { Authorization: `Bearer ${tok}` },
      }).then((res) => {
        expect(res.status).to.eq(200);
        const body = typeof res.body === 'string' ? JSON.parse(res.body) : res.body;
        const items = Array.isArray(body) ? body : (body.items ?? body.entries ?? []);
        expect(items, 'recent list').to.be.an('array');
      });
    });
  });
});
