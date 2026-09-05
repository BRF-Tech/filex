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

  it('storage rows carry the replica pairing envelope', () => {
    cy.adminGet<
      { id: number; role?: string; replica_mode?: string; replica_target_id?: number | null }[]
    >('/api/admin/storages').then((rows) => {
      expect(rows, 'storages').to.be.an('array').and.have.length.greaterThan(0);
      for (const r of rows) {
        // `role` and `replica_mode` are always serialized — those are the two
        // fields the Replikasyon page reads to decide what a row is.
        expect(r, `storage ${r.id} role`).to.have.property('role');
        expect(r, `storage ${r.id} replica_mode`).to.have.property('replica_mode');
        // ⚠ `replica_target_id` is `omitempty`: an UNPAIRED storage does not
        // carry the key at all. The old assertion demanded it unconditionally
        // and passed only because production had a paired storage — on any
        // fresh instance it failed. What can be asserted honestly is the type
        // when the key IS there; a string id would break the pairing dropdown.
        if ('replica_target_id' in r && r.replica_target_id !== null) {
          expect(r.replica_target_id, `storage ${r.id} replica_target_id`).to.be.a('number');
        }
      }
    });
  });
});
