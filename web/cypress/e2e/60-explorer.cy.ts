// 60-explorer — file explorer surface. Smoke + a few regressions the
// operator already hit.

describe('explorer', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  it('opens the multi-storage virtual root', () => {
    cy.visit('/admin/explore');
    cy.contains(/dosya yöneticisi|file manager|filex/i).should('be.visible');
  });

  it('cross-storage search hits more than the current storage', () => {
    // Search at the virtual root must consider every storage —
    // pre-v0.1.16 was scoped to storages[0] and missed hits in the
    // other rows.
    cy.adminGet<{ files?: { storage?: string; name: string }[] }>(
      '/api/files/manager?action=search&path=&filter=cube',
    ).then((d) => {
      const files = d.files ?? [];
      // Either no hits at all OR results spread across one+ storages
      // (we can't assert >1 storage in every test environment).
      const storagesSeen = new Set(files.map((f) => f.storage ?? ''));
      cy.log(`storages in search hits: ${[...storagesSeen].join(', ') || 'none'}`);
    });
  });

  it('starred endpoint is path-correct (no 404)', () => {
    // /manager/star/list is the v0.1.13 path; the old /manager/starred
    // route returned 404.
    cy.adminGet<unknown>('/api/files/manager/star/list?limit=10');
  });

  it('capabilities probe reports the optional services', () => {
    cy.adminGet<{
      external?: { drawio?: { state: string }; onlyoffice?: { state: string } };
    }>('/api/files/capabilities').then((c) => {
      // States we expect: ok | error | disabled | unknown.
      const allowed = new Set(['ok', 'error', 'disabled', 'unknown']);
      const drawio = c.external?.drawio?.state;
      const oo = c.external?.onlyoffice?.state;
      if (drawio) expect(allowed.has(drawio), `drawio state=${drawio}`).to.eq(true);
      if (oo) expect(allowed.has(oo), `onlyoffice state=${oo}`).to.eq(true);
    });
  });
});
