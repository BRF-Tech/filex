/**
 * 191-native-surfaces-theme - the admin panel's own shell wears the theme
 * (#74).
 *
 * The explorer, the dialogs and an app's screens take the theme from its
 * `--fe-*` tokens; the panel AROUND them did not:
 *
 *   - the face: `body` was Tailwind's sans, so on a theme with a face of its
 *     own (Terminal's monospace, an operator's brand font) the admin pages and
 *     the explorer inside them were set in two different faces;
 *   - the ground: the admin layout and the explorer page painted Tailwind's
 *     zinc-50 / zinc-950, and a page that paints no ground of its own (My
 *     shares) showed the browser's white or black, under a palette that was
 *     neither.
 *
 * Measured with `getComputedStyle`, against the token as the browser resolves
 * it on the same page - a ground that merely looks close is not the theme's.
 */
import { test, expect, type Page } from '@playwright/test';
import { dismissInstallBanner, loginAs } from '../helpers/auth';

const PREFS = '/api/me/prefs?surface=web';

/** A token's colour as the browser paints it (a probe element wears it). */
function tokenColor(page: Page, token: string) {
  return page.evaluate((t) => {
    const probe = document.createElement('div');
    probe.style.background = `var(${t})`;
    document.body.appendChild(probe);
    const c = getComputedStyle(probe).backgroundColor;
    probe.remove();
    return c;
  }, token);
}

test.describe('#74 - the admin shell wears the theme', () => {
  test('a theme with a face of its own reaches the panel; the ground is the theme’s', async ({ page }) => {
    await dismissInstallBanner(page);
    await loginAs(page);
    const got = await page.request.get(PREFS);
    const before = got.ok() ? (((await got.json()) as { prefs?: Record<string, unknown> }).prefs ?? {}) : {};
    try {
      // On the ACCOUNT, and in the first-paint mirror (e2e/helpers/prefs.ts
      // says why both).
      const put = await page.request.put(PREFS, { data: { prefs: { ...before, palette: 'terminal', theme: 'light' } } });
      expect(put.ok(), `prefs PUT: ${put.status()}`).toBe(true);
      await page.addInitScript(() => {
        localStorage.setItem('filex.palette', 'terminal');
        localStorage.setItem('filex.theme', 'light');
      });

      await page.goto('/admin/users');
      await expect
        .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--fe-font').trim()))
        .toMatch(/monospace/);
      // ⚠ The token is on the root before the app mounts (the first-paint
      // mirror sets it), so the poll above can pass while the admin layout is
      // not in the page yet: its ground then reads as ''. Wait for the one
      // layout to be there (e2e 198's `settled`) before measuring it.
      await expect(page.getByTestId('mega-menu')).toHaveCount(1, { timeout: 20_000 });

      // The face: the panel's own text, not only the explorer's.
      const face = await page.evaluate(() => getComputedStyle(document.body).fontFamily);
      expect(face, 'the admin shell is set in the theme\'s face').toMatch(/^ui-monospace/);

      // The ground: the admin layout's, against the theme's raised ground.
      const layoutBg = await page.evaluate(() => {
        const el = [...document.querySelectorAll('div')].find(
          (d) => d.classList.contains('min-h-screen') && d.classList.contains('flex'),
        );
        return el ? getComputedStyle(el).backgroundColor : '';
      });
      expect(layoutBg, 'the admin ground is the theme\'s, not zinc-50').toBe(await tokenColor(page, '--fe-bg-elev'));

      // A page that paints no ground of its own: the body's is the theme's.
      await page.goto('/admin/my-shares');
      await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe(
        await tokenColor(page, '--fe-bg'),
      );
      expect(await page.evaluate(() => getComputedStyle(document.body).color)).toBe(
        await page.evaluate(() => {
          const probe = document.createElement('div');
          probe.style.color = 'var(--fe-text)';
          document.body.appendChild(probe);
          const c = getComputedStyle(probe).color;
          probe.remove();
          return c;
        }),
      );
    } finally {
      await page.request.put(PREFS, { data: { prefs: before } }).catch(() => undefined);
    }
  });
});
