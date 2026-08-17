import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';

test.describe('Search — admin Search index test page', () => {
  test('SearchTest view returns stats', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/search');

    // The page renders one of: localised stats labels (count/size/index),
    // a backend Bleve stats blob, or the search input itself. We just
    // need to know the route mounted SOMETHING.
    const statsLabel = page.getByText(
      /document.?count|belge sayısı|index|arama|search|veriyor/i,
    );
    const searchInput = page.getByRole('searchbox')
      .or(page.getByPlaceholder(/search|ara/i));
    await expect(statsLabel.or(searchInput).first()).toBeVisible({ timeout: 10_000 });
  });

  test('rebuild button enqueues background job', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/search');

    // ⚠ `locator.count()` does NOT auto-wait. The old version called it the
    // instant after `goto` and skipped with "rebuild button not present in
    // current admin UI" whenever the SPA had not painted yet — an
    // intermittent silent skip on a control that SearchTest.vue renders
    // unconditionally. Wait for it, then require it: if the button really is
    // gone, that is a regression and this test should say so.
    const rebuild = page.getByRole('button', { name: /rebuild|yeniden/i }).first();
    await expect(rebuild, 'the admin search view must offer a rebuild button').toBeVisible({
      timeout: 15_000,
    });
    await rebuild.click();
    // Match the toast the view actually raises — `search.rebuildStarted`
    // ("Rebuild started" / "Yeniden oluşturma başladı", SearchTest.vue:63).
    //
    // The old pattern was /started|background|başlatıldı/, which matched any
    // of those words ANYWHERE on the page. The desktop-sync card's blurb
    // ("…keep them up to date in the background") also matches, so the
    // locator resolved to two elements and Playwright's strict mode failed
    // the test — a green feature reported red because an unrelated panel
    // shipped. Anchor on the message instead: still fails if the rebuild
    // never starts, no longer fails because a neighbour learned a word.
    await expect(
      page.getByText(/rebuild started|yeniden oluşturma başladı/i).first(),
    ).toBeVisible({ timeout: 5_000 });
  });
});
