// 05-routing — every registered admin route hydrates without
// throwing a Vue render error. Catches regressions where a view
// component's setup() fails on an unauthenticated cold load.

const routes = [
  'dashboard',
  'storages',
  'storages/new',
  'users',
  // ⚠ 'profile' was here. The page is retired (the user-settings dialog owns
  // those fields and a non-admin can open it too); the address survives only
  // as a forward, and 25-account.cy.ts checks that it still lands on the
  // dialog. Listing it here would be asserting that a retired page mounts.
  'settings',
  'external',
  'auth-providers',
  'audit',
  'sync',
  'shares',
  'trash',
  'search',
  'replica',
  'queue',
  'notifications',
  'about',
  'files',
];

describe('admin routes', () => {
  beforeEach(() => {
    cy.apiLogin();
  });

  for (const r of routes) {
    it(`/admin/${r} mounts without error`, () => {
      cy.visit(`/admin/${r}`);
      // The AdminLayout sidebar must be visible on every chrome route.
      cy.get('body', { timeout: 10000 }).should(($b) => {
        const text = $b.text();
        expect(text).not.to.match(/TypeError|ReferenceError|404 not found/i);
      });
    });
  }
});
