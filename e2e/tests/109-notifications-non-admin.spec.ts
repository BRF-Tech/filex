/**
 * 109-notifications-non-admin — a person who is not an administrator reads
 * ALL of their own notifications, without leaving the explorer.
 *
 * Owner's ruling, 2026-09-20, verbatim: *"Tüm bildirimleri gör butonu
 * explore'dan dışarı çıkıyor; adam admin değilse göremez."*
 *
 * What was broken, and why no existing test saw it: the bell's footer was a
 * `<RouterLink to="/notifications">` wrapped in `v-if="auth.isAdmin"`. That
 * route is the ADMIN panel's instance-wide audit page, behind `requiresAdmin`.
 * So for an administrator — the only person who ever looked — everything
 * worked; for everybody else the button was simply absent, and the newest
 * fifteen rows in the popover were the only notifications they could ever
 * reach. Their own mail, behind a permission they do not have.
 *
 * Every assertion here is from the NON-ADMIN's session:
 *
 *   1. the bell is in the explorer's header, with the unread count ON it;
 *   2. "see all" is offered to them;
 *   3. it opens the full list INSIDE the explorer — the URL does not change,
 *      the explorer is still mounted underneath;
 *   4. nothing on the notification path touches `/api/admin/notifications`
 *      (the network is watched — that call would 403 in front of a real user);
 *   5. the admin console link is NOT offered to them, and the admin page
 *      still bounces them if they try to walk to it by hand;
 *   6. paging reaches rows the bell never showed;
 *   7. a row with nothing to open is not clickable — no pointer, no link
 *      role, no dead click (rule 1, docs/NOTIFICATIONS.md).
 */
import { test, expect, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner } from '../helpers/auth';

const USER_EMAIL = 'notif-reader@example.com';
const USER_PASSWORD = 'notif-reader-pw-2026';

/** Open the bell's popover and wait for its rows. */
async function openBell(page: Page) {
  await page.getByTestId('notification-bell').click();
  await expect(page.getByTestId('notification-panel')).toBeVisible();
}

test.describe('a non-admin reaches all of their notifications', () => {
  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    // Best-effort create; a rerun against the same DB already has them.
    await request.post('/api/admin/users', {
      data: { email: USER_EMAIL, password: USER_PASSWORD, role: 'user' },
    });
    // ⚠ Broadcast rows (no `user_id`), which every signed-in person sees —
    // the one way to give a brand-new account something to read without
    // granting it a storage first. More than one page of them (the screen
    // pages at 25), because "can they reach the twenty-sixth" is the whole
    // complaint this spec exists for.
    for (let i = 0; i < 30; i++) {
      await request.post('/api/admin/notifications/test');
    }
  });

  test.beforeEach(async ({ page }) => {
    await dismissInstallBanner(page);
    // The onboarding tour's card covers the explorer's header cluster, which
    // is where the bell lives (same reason 25-connections turns it off).
    await page.addInitScript(() => {
      try {
        localStorage.setItem('filex.tourDone', '1');
      } catch {
        /* storage blocked — the tour will just be present */
      }
    });
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(USER_EMAIL);
    await page.getByLabel(/password|parola/i).fill(USER_PASSWORD);
    await page
      .getByRole('button', { name: 'Sign in', exact: true })
      .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
      .first()
      .click();
    // A non-admin is sent to their own front door, not to the admin panel.
    await page.waitForURL(/\/drive\//, { timeout: 15_000 });
    await page.goto('/drive/explore');
  });

  test('the bell is in their header, with the count on the icon', async ({ page }) => {
    const bell = page.getByTestId('notification-bell');
    await expect(bell).toBeVisible();
    // Rule 3: the count is drawn ON the icon — and said out loud, because a
    // badge is a picture of a number.
    await expect(page.getByTestId('unread-badge')).toBeVisible();
    await expect(bell).toHaveAttribute('aria-label', /\d+/);
  });

  test('"see all" is offered to them, and the admin console is not', async ({ page }) => {
    await openBell(page);
    const all = page.getByTestId('notification-view-all');
    await expect(all, 'a non-admin is offered no way to see all of their notifications').toBeVisible();
    // ⚠ A BUTTON, not a link: nothing navigates, so no route guard can bounce
    // them and nothing throws away the folder they were standing in.
    await expect(all).toHaveJSProperty('tagName', 'BUTTON');
    await expect(page.getByTestId('notification-manage')).toHaveCount(0);
  });

  test('the full list opens INSIDE the explorer and asks no admin endpoint', async ({ page }) => {
    // ⚠ Scoped to the NOTIFICATION admin endpoints, not to `/api/admin`
    // wholesale. The explorer asks `/api/admin/storages` on every mount and
    // shrugs off the 403 on purpose (Explore.vue → "best-effort; roots then
    // fall back to manager-root discovery"), which is a different, older,
    // deliberate thing. Asserting on the whole prefix would make this spec go
    // red for a reason it is not about.
    const adminCalls: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/admin/notifications')) adminCalls.push(r.url());
    });

    const before = page.url();
    await openBell(page);
    await page.getByTestId('notification-view-all').click();

    const screen = page.getByTestId('notifications-screen');
    await expect(screen).toBeVisible();
    // Still on the explorer, still the same address: it opened OVER what they
    // were doing rather than replacing it.
    expect(page.url()).toBe(before);
    await expect(page.locator('.fe')).toBeVisible();

    await expect(page.getByTestId('notification-row').first()).toBeVisible();
    expect(
      adminCalls,
      `a non-admin session called ${adminCalls.join(', ')} — that endpoint 403s for them`,
    ).toEqual([]);
  });

  test('paging reaches rows the bell never showed', async ({ page }) => {
    await openBell(page);
    await page.getByTestId('notification-view-all').click();
    const pager = page.getByTestId('notifications-screen-pager');
    await expect(pager, 'no pager — seed fewer than two pages of rows?').toBeVisible();

    // ⚠ Compared by ROW ID, not by the words on screen. The seeded rows are
    // thirty identical `admin_test` notifications — same title, same body,
    // same minute — so a text comparison says "nothing changed" on a pager
    // that worked perfectly. The id is what says these are different rows.
    const firstId = await page.getByTestId('notification-row').first().getAttribute('data-notification-id');
    await pager.getByRole('button').last().click();
    await expect
      .poll(async () => page.getByTestId('notification-row').first().getAttribute('data-notification-id'))
      .not.toBe(firstId);
    // …and the twenty-sixth row — the one the bell's fifteen never showed and
    // the old "see all" could not reach — is now on screen.
    await expect(page.getByTestId('notification-row').first()).toBeVisible();
  });

  test('a row with nothing to open is not clickable at all', async ({ page }) => {
    // The seeded rows are `admin_test`, which the target table documents as
    // `none` — nothing to open, and it says so honestly.
    await openBell(page);
    await page.getByTestId('notification-view-all').click();
    const inert = page.locator('[data-testid="notification-row"][data-clickable="no"]').first();
    await expect(inert).toBeVisible();
    await expect(inert).toHaveJSProperty('tagName', 'DIV');
    await expect(inert).toHaveCSS('cursor', 'default');

    const before = page.url();
    await inert.click();
    // Reading it is the whole interaction: nothing navigates, and the list
    // they are reading does not close under them.
    expect(page.url()).toBe(before);
    await expect(page.getByTestId('notifications-screen')).toBeVisible();
  });

  test('the admin page is still theirs to be refused — it just is not where they are sent', async ({
    page,
  }) => {
    // The admin console keeps its job and its guard. The change is only that
    // nobody is POINTED at it to read their own mail.
    await page.goto('/admin/notifications');
    await expect(page).not.toHaveURL(/\/admin\/notifications$/);
  });
});
