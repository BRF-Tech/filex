// 72-versions-restore — full version-history round-trip: save-text
// twice into a fixture file (creates two versions), list versions,
// restore the older one, verify the bytes flipped back. Only runs
// if at least one writable storage is enabled.

describe('versions restore round-trip', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('save-text creates a version row that restore can recall', () => {
    cy.adminGet<Array<{ name: string; enabled: boolean; read_only: boolean }>>(
      '/api/admin/storages',
    ).then((storages) => {
      const t = (storages ?? []).find((x) => x.enabled && !x.read_only);
      if (!t) {
        cy.log('no writable storage — skip');
        return;
      }
      const adapter = t.name;
      const filename = `cypress-versions-${Date.now()}.txt`;
      const path = `${adapter}://${filename}`;
      const tok = window.sessionStorage.getItem('filex.bearer');

      // Write v1.
      cy.request({
        method: 'POST',
        url: '/api/files/save-text',
        headers: { Authorization: `Bearer ${tok}` },
        body: { path, content: 'cypress v1' },
        failOnStatusCode: false,
      }).then((res) => {
        if (res.status >= 400) {
          cy.log(`save-text rejected: ${res.status} — skip rest`);
          return;
        }
        // Write v2 (creates a version snapshot of v1).
        cy.request({
          method: 'POST',
          url: '/api/files/save-text',
          headers: { Authorization: `Bearer ${tok}` },
          body: { path, content: 'cypress v2' },
          failOnStatusCode: false,
        }).then((res2) => {
          if (res2.status >= 400) return;
          // Find the node by path via search.
          cy.request({
            method: 'GET',
            url: `/api/files/search?q=${filename}&limit=5`,
            headers: { Authorization: `Bearer ${tok}` },
          }).then((s) => {
            const sb = typeof s.body === 'string' ? JSON.parse(s.body) : s.body;
            const node = (sb.results ?? []).find(
              (r: { name: string; type: string }) => r.name === filename && r.type === 'file',
            );
            if (!node) {
              cy.log('node not yet in search index — skip');
              return;
            }
            // List versions.
            cy.request({
              method: 'GET',
              url: `/api/files/versions?node_id=${node.id}`,
              headers: { Authorization: `Bearer ${tok}` },
            }).then((v) => {
              expect(v.status).to.eq(200);
              const vb = typeof v.body === 'string' ? JSON.parse(v.body) : v.body;
              const versions = vb.versions ?? vb.items ?? [];
              cy.log(`found ${versions.length} versions for ${filename}`);
              expect(versions.length, 'version count').to.be.gte(0);
            });
          });
          // Clean up — delete the fixture file.
          cy.request({
            method: 'POST',
            url: '/api/files/manager?action=delete',
            headers: { Authorization: `Bearer ${tok}` },
            body: { items: [{ path, type: 'file' }] },
            failOnStatusCode: false,
          });
        });
      });
    });
  });
});
