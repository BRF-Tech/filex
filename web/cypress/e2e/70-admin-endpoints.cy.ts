// 70-admin-endpoints — every admin API endpoint the SPA hits must
// answer 200 (or a documented non-4xx). Pre-v0.1.13 the SPA shipped
// with paths that 404'd against the live backend (search /admin/search,
// users /{id} GET, starred). This spec is the safety net to keep that
// drift from coming back.

const ADMIN_ENDPOINTS: { method: 'GET'; path: string; description: string }[] = [
  { method: 'GET', path: '/api/auth/me',                          description: 'whoami' },
  { method: 'GET', path: '/api/capabilities',                     description: 'capabilities (public alias)' },
  { method: 'GET', path: '/api/files/capabilities',               description: 'capabilities' },
  { method: 'GET', path: '/api/notifications',                    description: 'user notification list' },
  { method: 'GET', path: '/api/notifications/unread-count',       description: 'unread count' },
  { method: 'GET', path: '/api/notifications/settings',           description: 'notification settings' },
  { method: 'GET', path: '/api/files/quota/me',                   description: 'quota' },
  { method: 'GET', path: '/api/files/manager/trash',              description: 'trash list' },
  { method: 'GET', path: '/api/files/manager/star/list?limit=10', description: 'starred (v0.1.13 path)' },
  { method: 'GET', path: '/api/files/manager/recent?limit=10',    description: 'recently opened' },
  { method: 'GET', path: '/api/files/search?q=cypress',           description: 'cross-storage search' },
  { method: 'GET', path: '/api/admin/dashboard',                  description: 'dashboard' },
  { method: 'GET', path: '/api/admin/storages',                   description: 'storages list' },
  { method: 'GET', path: '/api/admin/replication-targets',        description: 'replication targets (v0.1.18)' },
  { method: 'GET', path: '/api/admin/users',                      description: 'users list' },
  { method: 'GET', path: '/api/admin/settings',                   description: 'settings' },
  { method: 'GET', path: '/api/admin/audit',                      description: 'audit list' },
  { method: 'GET', path: '/api/admin/sync-runs?limit=5',          description: 'sync runs (5-day filter)' },
  { method: 'GET', path: '/api/admin/shares',                     description: 'shares list' },
  { method: 'GET', path: '/api/admin/external',                   description: 'external services' },
  { method: 'GET', path: '/api/admin/auth-providers',             description: 'auth providers' },
  { method: 'GET', path: '/api/admin/search/stats',               description: 'search index stats' },
  { method: 'GET', path: '/api/admin/queue/stats',                description: 'queue stats' },
  { method: 'GET', path: '/api/admin/queue?limit=5',              description: 'queue list' },
  { method: 'GET', path: '/api/admin/notifications',              description: 'admin notification list' },
  { method: 'GET', path: '/api/admin/notifications/webhook-config', description: 'webhook config' },
  { method: 'GET', path: '/api/admin/replica/rules',              description: 'replica rules' },
  { method: 'GET', path: '/api/admin/replica/failures',           description: 'replica failures' },
  { method: 'GET', path: '/api/admin/replica/failures/count',     description: 'replica failures count' },
  { method: 'GET', path: '/api/admin/replica/settings',           description: 'replica settings' },
];

describe('admin endpoint sweep', () => {
  // Cypress default testIsolation clears sessionStorage between tests,
  // so login per-test, not per-suite.
  for (const ep of ADMIN_ENDPOINTS) {
    it(`${ep.method} ${ep.path} — ${ep.description}`, () => {
      cy.apiLogin().then((tok) => {
        cy.request({
          method: ep.method,
          url: ep.path,
          headers: { Authorization: `Bearer ${tok}` },
          failOnStatusCode: false,
        }).then((res) => {
          // Accept 200 or 204 (some endpoints return no-content for
          // "exists but no payload yet", e.g. replica report).
          expect([200, 204], `unexpected status for ${ep.path}`).to.include(res.status);
        });
      });
    });
  }
});
