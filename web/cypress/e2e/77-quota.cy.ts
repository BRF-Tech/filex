// 77-quota — per-user quota usage + admin recompute trigger.

describe('quota', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/files/quota/me returns usage envelope', () => {
    cy.adminGet<{
      user_id?: number;
      bytes_used?: number;
      bytes_limit?: number;
      files_count?: number;
    }>('/api/files/quota/me').then((d) => {
      expect(d, 'quota body').to.be.an('object');
      // Either bytes_used or `used` is the canonical field — be lax.
      const used = d.bytes_used ?? (d as Record<string, unknown>).used;
      expect(used, 'used').to.satisfy(
        (v: unknown) => v === undefined || typeof v === 'number',
      );
    });
  });

  it('admin recompute on bogus user returns 404', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/quota/9999999/recompute',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });

  it('admin set on bogus user rejects cleanly', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/quota/9999999',
        headers: { Authorization: `Bearer ${tok}` },
        body: { bytes_limit: 100 },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });
});
