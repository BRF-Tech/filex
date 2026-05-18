// 40-replication — Replikasyon page wires through the new
// `replication_targets` entity (v0.1.18) and the per-primary pairing
// reads/writes `storages.replica_target_id` (not the deprecated
// `replica_of_id` envelope).

describe('replication', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/admin/replica');
  });

  it('shows the Replika hedefleri + Eşleştirmeler cards', () => {
    cy.contains(/replika hedef|replication target/i).should('be.visible');
    cy.contains(/eşleştirme|pair/i).should('be.visible');
  });

  it('replication-targets CRUD round-trips end-to-end', () => {
    const probeName = `cy-target-${Date.now()}`;
    cy.adminGet<unknown>('/api/admin/replication-targets'); // warm
    const tok = window.sessionStorage.getItem('filex.bearer');
    cy.request({
      method: 'POST',
      url: '/api/admin/replication-targets',
      headers: { Authorization: `Bearer ${tok}` },
      body: {
        name: probeName,
        driver: 'local',
        config: { root: '/tmp/cypress-target-probe' },
        mode: 'async',
        enabled: true,
      },
    }).then((res) => {
      expect(res.status).to.eq(201);
      const id = res.body.id as number;
      expect(id).to.be.greaterThan(0);

      cy.request({
        method: 'GET',
        url: `/api/admin/replication-targets/${id}`,
        headers: { Authorization: `Bearer ${tok}` },
      }).then((g) => {
        expect(g.status).to.eq(200);
        expect(g.body.name).to.eq(probeName);
      });

      cy.request({
        method: 'DELETE',
        url: `/api/admin/replication-targets/${id}`,
        headers: { Authorization: `Bearer ${tok}` },
      }).then((d) => {
        expect(d.status).to.eq(204);
      });
    });
  });

  it('primary storages can read their replica_target_id field', () => {
    cy.adminGet<{ id: number; replica_target_id?: number | null }[]>('/api/admin/storages').then(
      (rows) => {
        if (rows.length === 0) {
          cy.log('no storages — skipping');
          return;
        }
        // The field must be exposed (null is fine; undefined would
        // mean a backend serialization gap).
        for (const r of rows) {
          expect(r, `storage ${r.id}`).to.have.property('replica_target_id');
        }
      },
    );
  });
});
