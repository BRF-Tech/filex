// 13-navigation-ui — sidebar click navigates the SPA. Catches
// router-link href regressions.

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
      cy.contains('a, button', t.link, { timeout: 10000 })
        .filter(':visible')
        .first()
        .click({ force: true });
      cy.url({ timeout: 10000 }).should('include', t.url);
    });
  }
});
