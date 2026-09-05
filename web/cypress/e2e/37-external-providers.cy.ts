// 37-external-providers — admin External + Auth Providers admin
// list shapes + non-mutating test/probe endpoints.

describe('external services', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('every slot capabilities advertises has a row in the admin list', () => {
    // ⚠ The slot names are READ from the capability probe, never spelled out
    // here. This case used to assert a literal ['drawio', 'mermaid',
    // 'onlyoffice'] and there has been no `mermaid` slot for a long time: the
    // baseline is convert / drawio / onlyoffice. It passed anyway, because the
    // suite only ever ran against production — whose `external` table still
    // holds a leftover `mermaid` ROW from an older build. Against a fresh
    // instance it failed on the first try. A literal list turns a rename into a
    // test that lies in one direction and breaks in the other.
    //
    // What this measures now is the thing that actually matters: the two
    // endpoints agree. A slot advertised by /api/files/capabilities with no row
    // behind it renders an External page the operator cannot configure.
    cy.adminGet<{ external?: Record<string, unknown> }>('/api/files/capabilities').then((caps) => {
      const slots = Object.keys(caps.external ?? {});
      expect(slots, 'capabilities advertises external slots').to.have.length.greaterThan(0);
      cy.adminGet<{
        entries?: Array<{ Name: string; Enabled: boolean; URL: string; LastState: string }>;
      }>('/api/admin/external').then((d) => {
        expect(d.entries, 'entries').to.be.an('array');
        const names = (d.entries ?? []).map((e) => e.Name);
        for (const slot of slots) {
          expect(names, `admin list has a row for the advertised slot ${slot}`).to.include(slot);
        }
      });
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
