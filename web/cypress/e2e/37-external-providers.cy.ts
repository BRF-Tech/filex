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
      providers?: Array<
        {
          name: string;
          enabled: boolean;
          capabilities: Record<string, boolean>;
          config_redacted: Record<string, unknown>;
          secrets_set: Record<string, unknown>;
        } & Record<string, unknown>
      >;
    }>('/api/admin/auth-providers').then((d) => {
      expect(d.providers, 'providers').to.be.an('array');
      const names = (d.providers ?? []).map((p) => p.name);
      // local + oidc + ldap baseline (ldap may be disabled).
      for (const p of ['local', 'oidc']) {
        expect(names, `providers has ${p}`).to.include(p);
      }
      // ⚠ The row's shape, from handlers/auth_providers.go `providerView`.
      // This spec pinned four keys and went stale the release the page grew
      // five more (v0.43.0 release run). The REQUIRED keys are the ones the
      // struct always writes; the OPTIONAL ones are `omitempty` and appear
      // only when they say something. Both directions are asserted: nothing
      // required is missing, and nothing outside the two lists appears — a
      // new field on this admin surface should be a deliberate edit here.
      const REQUIRED = [
        'name',
        'capabilities',
        'managed',
        'origin',
        'enabled',
        'state',
        'config_redacted',
        'secrets_set',
        'testable',
      ];
      const OPTIONAL = ['from', 'error', 'legacy', 'shadowed', 'fields'];
      for (const p of d.providers ?? []) {
        expect(p, `${p.name} envelope`).to.include.all.keys(...REQUIRED);
        const stray = Object.keys(p).filter((k) => !REQUIRED.includes(k) && !OPTIONAL.includes(k));
        expect(stray, `${p.name}: fields this spec has not been told about`).to.deep.equal([]);

        // ⚠⚠ THE property worth pinning on this row. `secrets_set` says WHICH
        // secret fields hold a value ("set — replace?") and never the value
        // itself: every entry is a boolean. And a secret field is never
        // echoed in `config_redacted` — the page learns that it is set, not
        // what it is.
        for (const [field, v] of Object.entries(p.secrets_set ?? {})) {
          expect(v, `${p.name}.secrets_set.${field} is a boolean, never the secret`).to.be.a('boolean');
          expect(p.config_redacted ?? {}, `${p.name}: secret field ${field} is not echoed in config_redacted`).not.to.have.property(field);
        }
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
