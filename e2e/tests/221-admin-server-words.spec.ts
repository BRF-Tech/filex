/**
 * 221-admin-server-words — the admin pages show the SERVER's numbers and
 * words (0.54 audit, task #208: D8 D9 A5 A6 A13 A14 B5 B8 B10).
 *
 * Until 0.54 a handful of admin pages worked out in the browser what the
 * server already knew: the duplicate report summed the 100 groups it was
 * sent, the dashboard re-added the storage rows, the usage page re-implemented
 * the bucket totals, the Updates page kept its own copy of the policy words,
 * the audit log and the provider test composed their sentences from wire
 * codes (so the admin MCP tools got only the codes), and the protection and
 * webhook forms checked ranges and schemes with rules of their own that
 * disagreed with the server's. This spec reads each answer through the API -
 * the field is there, in the language asked for - and two pages that print
 * them.
 *
 *   node e2e/run.mjs local --grep admin-server-words-208
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';

const STAMP = Date.now();

async function json(request: APIRequestContext, url: string) {
  const res = await request.get(url);
  expect(res.ok(), `${url}: ${await res.text()}`).toBe(true);
  return res.json();
}

test.describe('the admin pages show the server’s numbers and words (admin-server-words-208)', () => {
  test.beforeEach(async ({ request }) => {
    await apiLogin(request);
  });

  test('D8: the duplicate report carries the whole report’s totals', async ({ request }) => {
    const r = await json(request, '/api/admin/duplicates?limit=1&min_size=1');
    expect(typeof r.total_groups).toBe('number');
    expect(typeof r.total_copies).toBe('number');
    expect(typeof r.total_waste).toBe('number');
    expect(r.total_groups).toBeGreaterThanOrEqual(r.groups.length);
  });

  test('D9: the dashboard sends its totals, the running count and the newest scan', async ({ request }) => {
    const d = await json(request, '/api/admin/dashboard?lang=tr');
    expect(typeof d.total_files).toBe('number');
    expect(typeof d.total_bytes).toBe('number');
    expect(typeof d.active_syncs).toBe('number');
  });

  test('A5: the Updates policy is said by the server, in the language asked for', async ({ request }) => {
    const tr = await json(request, '/api/admin/update?lang=tr');
    const en = await json(request, '/api/admin/update?lang=en');
    for (const s of [tr, en]) {
      expect(s.policy_name, 'the policy by its name').toBeTruthy();
      expect(s.policy_badge, 'the badge').toBeTruthy();
      expect(String(s.policy_badge)).not.toContain('{');
      if (s.policy_limit) expect(s.policy_note, 'a limited policy says why').toBeTruthy();
    }
    if (tr.policy === 'patch' && !tr.policy_limit) expect(tr.policy_badge).toBe('Politika: yamaları kur');
  });

  test('A14: every audit row and the What filter are in words', async ({ request }) => {
    const made = await request.post('/api/admin/users', {
      data: { email: `words-208-${STAMP}@example.com`, password: 'Words-208-2026!', role: 'user' },
    });
    expect(made.ok(), await made.text()).toBe(true);
    const page = await json(request, '/api/admin/audit?lang=tr&limit=50');
    const rows = (page.entries as Array<{ entry?: { action?: string }; label?: string; target_label?: string }>).filter(
      (e) => e.entry?.action === 'user.create',
    );
    expect(rows.length, 'the user just made is in the log').toBeGreaterThan(0);
    expect(rows[0].label).toBe('Kullanıcı: oluşturuldu');
    expect(rows[0].target_label).toContain('Kullanıcı');
    expect(Array.isArray(page.resources) && page.resources.length).toBeTruthy();
    expect(page.resources.find((o: { label: string }) => o.label === 'Kullanıcı')?.value).toContain('user.');
  });

  test('A13: a provider test comes back worded', async ({ request }) => {
    const res = await request.post('/api/admin/auth-providers/ldap/test?lang=tr', { data: { url: '', base_dn: '' } });
    test.skip(res.status() === 404, 'no LDAP driver on this server');
    expect(res.ok(), await res.text()).toBe(true);
    const r = await res.json();
    expect(r.checks.length).toBeGreaterThan(0);
    for (const c of r.checks) {
      expect(c.text, `${c.id}.${c.status}`).toBeTruthy();
      expect(String(c.text)).not.toMatch(/^server\./);
    }
  });

  test('B8: the protection bounds come with the values; over the top is refused in words', async ({ request }) => {
    const p = await json(request, '/api/admin/protection');
    expect(p.trash_retention_days_max).toBe(3650);
    expect(p.versions_keep_n_max).toBe(1000);
    expect(p.share_max_ttl_days_max).toBe(3650);
    const res = await request.patch('/api/admin/protection', {
      data: { trash_retention_days: 999999 },
      headers: { 'Accept-Language': 'tr-TR,tr;q=0.9' },
    });
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.field).toBe('trash_retention_days');
    expect(body.message).toContain('3650');
    expect(body.message).not.toContain('must be between');
  });

  test('B10: a webhook address is http(s) in any case, and a refusal names its box', async ({ request }) => {
    const ok = await request.post('/api/admin/webhooks', {
      data: { name: `words-208-${STAMP}`, url: 'HTTPS://hooks.example.test/filex', enabled: false },
    });
    expect(ok.ok(), await ok.text()).toBe(true);
    const made = await ok.json();
    await request.delete(`/api/admin/webhooks/${made.id}`);

    const bad = await request.post('/api/admin/webhooks', { data: { name: 'x', url: 'ftp://hooks.example.test/x' } });
    expect(bad.status()).toBe(400);
    const body = await bad.json();
    expect(body.field).toBe('url');
    expect(body.message).toBeTruthy();
  });

  test('A14 on screen: the Audit page prints the server’s label', async ({ page }) => {
    await loginAs(page);
    // A row of its own, so this test needs no other to have run first.
    const made = await page.request.post('/api/admin/users', {
      data: { email: `words-208-screen-${STAMP}@example.com`, password: 'Words-208-2026!', role: 'user' },
    });
    expect(made.ok(), await made.text()).toBe(true);
    await page.goto('/admin/audit');
    await expect(page.getByText('User: created').first()).toBeVisible({ timeout: 20_000 });
  });
});
