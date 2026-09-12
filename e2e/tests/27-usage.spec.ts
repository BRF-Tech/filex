import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';

/**
 * Usage & cost — the admin page (issue #20).
 *
 * The unit tests cover the report parser, the pricing table and the handler.
 * None of them can say whether the page is reachable, whether its strings
 * resolve, or whether it obeys the one rule its own header states: never draw
 * an empty chart for an instance that has nothing configured, because "you
 * spent nothing" and "nothing is set up" look identical as a zero.
 */
test.describe('Usage & cost', () => {
  test('the page is reachable from the sidebar and its strings resolve', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/usage');

    await expect(page.getByRole('heading', { name: /usage/i })).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('usage-config')).toBeVisible();

    // A missing translation renders as its own key. Nothing on this page may.
    const body = await page.locator('body').innerText();
    expect(body).not.toMatch(/\busage\.[a-z]+\./i);

    // The sidebar entry exists, which is what makes the page findable at all.
    await expect(page.locator('a', { hasText: /usage/i }).first()).toBeVisible();
  });

  test('an unconfigured instance is told so, not shown a zero', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/usage');
    await expect(page.getByTestId('usage-config')).toBeVisible({ timeout: 10_000 });

    // Fresh instances have no provider set; the page must say that rather than
    // render a cost of zero, which reads as "this costs you nothing".
    const body = await page.locator('body').innerText();
    expect(body).toMatch(/nothing is configured/i);
    expect(body).not.toMatch(/\$0\.00/);
  });
});
