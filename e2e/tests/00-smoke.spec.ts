import { test, expect } from '@playwright/test';

test.describe('Smoke — server up and serving', () => {
  test('healthz responds with status:ok', async ({ request }) => {
    const res = await request.get('/healthz');
    expect(res.ok()).toBeTruthy();
    expect(await res.json()).toMatchObject({ status: 'ok' });
  });

  test('public capabilities endpoint returns valid JSON', async ({ request }) => {
    const res = await request.get('/api/capabilities');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body).toHaveProperty('upload');
    expect(body).toHaveProperty('thumbs');
    expect(typeof body.max_upload_size).toBe('number');
  });

  test('admin login page renders core controls', async ({ page }) => {
    await page.goto('/admin/login');
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await expect(page.getByLabel(/password|şifre/i)).toBeVisible();
    await expect(page.getByRole('button', { name: /sign in|giriş/i })).toBeVisible();
  });

  test('unauthenticated request to admin API returns 401', async ({ request }) => {
    const res = await request.get('/api/admin/dashboard');
    expect(res.status()).toBe(401);
  });
});
