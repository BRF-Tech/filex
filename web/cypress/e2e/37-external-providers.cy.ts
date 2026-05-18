// 37-external-providers — admin External + Auth Providers admin
// list shapes + non-mutating test/probe endpoints.

describe('external services', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/admin/external returns entries[]', () => {
    cy.adminGet<{ entries?: Array<{ Name: string; Enabled: boolean; URL: string; LastState: string }> }>(
      '/api/admin/external',
    ).then((d) => {
      expect(d.entries, 'entries').to.be.an('array');
      const names = (d.entries ?? []).map((e) => e.Name);
      // The three baseline slots Capability advertises.
      for (const slot of ['drawio', 'mermaid', 'onlyoffice']) {
        expect(names, `entries has ${slot}`).to.include(slot);
      }
    });
  });

  it('test endpoint on unknown service returns 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/external/nope-doesnt-exist/test',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });
});

describe('auth providers', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('GET /api/admin/auth-providers returns providers[]', () => {
    cy.adminGet<{
      providers?: Array<{ name: string; enabled: boolean; capabilities: Record<string, boolean> }>;
    }>('/api/admin/auth-providers').then((d) => {
      expect(d.providers, 'providers').to.be.an('array');
      const names = (d.providers ?? []).map((p) => p.name);
      // local + oidc + ldap baseline (ldap may be disabled).
      for (const p of ['local', 'oidc']) {
        expect(names, `providers has ${p}`).to.include(p);
      }
      for (const p of d.providers ?? []) {
        expect(p, `${p.name} envelope`).to.have.all.keys(
          'name',
          'enabled',
          'capabilities',
          'config_redacted',
        );
      }
    });
  });

  it('test endpoint on unknown provider returns 404 (route wired)', () => {
    cy.apiLogin().then((tok) => {
      cy.request({
        method: 'POST',
        url: '/api/admin/auth-providers/nope-doesnt-exist/test',
        headers: { Authorization: `Bearer ${tok}` },
        failOnStatusCode: false,
      }).then((res) => {
        expect([400, 404]).to.include(res.status);
      });
    });
  });
});
