// 13-navigation-ui — sidebar click navigates the SPA. Catches
// router-link href regressions.
//
// ⚠ Scroll the link into view before clicking, and scope to `a.nav-link`.
//
// The sidebar is a `overflow-y-auto` column, and Cypress calls an element
// hidden when it is clipped outside a scrollable ancestor — so at the
// configured 1440x900 viewport every destination past "API / MCP" is invisible
// to `.filter(':visible')` and the click never happened. Measured 2026-09-05:
// the Audit / Sync / Shares / Trash / Replica / Queue cases all died in the
// hook, seven of the nine skipped, in a build whose sidebar was working.
// `cy.contains('a, button', …)` also matched a header button before the nav
// link on some routes, which is why the query is pinned to `a.nav-link`.

describe('sidebar navigation', () => {
  beforeEach(() => {
    cy.apiLogin();
    cy.visit('/admin/dashboard');
  });

  const navTargets: Array<{ link: RegExp; url: string }> = [
    { link: /depolar|storages/i, url: '/admin/storages' },
    { link: /kullanıcı|users/i, url: '/admin/users' },
    { link: /ayarlar|settings/i, url: '/admin/settings' },
    { link: /denetim|audit/i, url: '/admin/audit' },
    { link: /senkron|sync/i, url: '/admin/sync' },
    { link: /paylaşım|shares/i, url: '/admin/shares' },
    { link: /çöp|trash/i, url: '/admin/trash' },
    { link: /replika|replica/i, url: '/admin/replica' },
    { link: /kuyruk|queue/i, url: '/admin/queue' },
  ];

  for (const t of navTargets) {
    it(`clicks "${t.link}" → lands on ${t.url}`, () => {
      cy.get('a.nav-link', { timeout: 10000 })
        .contains(t.link)
        .first()
        .scrollIntoView()
        .click();
      cy.url({ timeout: 10000 }).should('include', t.url);
    });
  }
});
