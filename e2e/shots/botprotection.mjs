// Bot protection (#294, 0.58.0) - the check on the sign-in form, and the
// administrator's page for it.
//
//   node e2e/shots/botprotection.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/botprotection/ (or SHOTS_OUT):
//
//   login-captcha-dark-1440.png    the sign-in card, dark, with Cloudflare
//                                  Turnstile's widget solved between "Stay
//                                  signed in" and the button
//   bot-protection-dark-1440.png   Admin → Settings → Security & data → Bot
//                                  protection, dark: the status, the provider
//                                  strip, the two keys (the secret written,
//                                  never shown), where each value came from,
//                                  the forms and the outage fallback
//
// ⚠ The widget is Cloudflare's own, with its public TEST keys (a site key that
// always passes, a secret that always verifies): nothing real, and the same
// picture on every run. It needs a route to challenges.cloudflare.com; without
// one the scene fails rather than photograph a widget that is not there.
//
// ⚠ The sign-in is protected, so the scene signs in through the FORM once
// (the test key's token is accepted once per server) and takes the admin page
// in the same browser session.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, layoutProblems, log, mustSay, newContext, shot, sleep } from './scene.mjs';

export const INPUTS = [
  'web/src/views/BotProtection.vue',
  'web/src/views/Login.vue',
  'web/src/api/captcha.ts',
  'packages/core/src/components/CaptchaWidget.vue',
  'packages/core/src/composables/useCaptcha.ts',
  'packages/core/src/lib/captcha.ts',
  'backend/internal/captcha',
  'backend/internal/api/handlers',
];

const SET = 'botprotection';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

/** Cloudflare's public test keys (always pass). Not secrets. */
const SITE_KEY = '1x00000000000000000000AA';
const SECRET = '1x0000000000000000000000000000000AA';

async function main() {
  const inst = await bootInstance({
    name: SET,
    admin: ADMIN,
    env: {
      FILEX_UPDATE_CHECK: '0',
      CAPTCHA_PROVIDER: 'turnstile',
      CAPTCHA_SITE_KEY: SITE_KEY,
      CAPTCHA_SECRET: SECRET,
      CAPTCHA_FORMS: 'login,share_password,file_request',
    },
  });
  const browser = await chromium.launch();
  try {
    // ⚠ On the REAL clock: Turnstile hands no token to a browser whose clock
    // is weeks off, and the scene clock (clock.mjs, SCENE_NOW) is. Neither
    // picture shows a date, so nothing in them moves with the day it is taken.
    const ctx = await newContext(browser, { scheme: 'dark', width: 1440, height: 900, now: Date.now() });
    const page = await ctx.newPage();

    // ── the sign-in card, the widget solved ──────────────────────────────
    await page.goto(`${inst.url}/admin/login`);
    await page.fill('#email', ADMIN.email);
    await page.fill('#password', ADMIN.password);
    const widget = page.getByTestId('captcha');
    await widget.waitFor({ timeout: 20_000 });
    await page.waitForSelector('[data-testid="captcha"][data-captcha-state="solved"]', { timeout: 30_000 });
    // Turnstile draws its tick a moment after it hands out the token.
    await sleep(1500);
    if (!page.frames().some((f) => f.url().includes('challenges.cloudflare.com'))) {
      throw new Error('no Turnstile frame on the sign-in card: the picture would show an empty box');
    }
    await mustSay(page.locator('.lg-card'), 'the sign-in card', ['Sign in', 'Stay signed in']);
    const problems = await layoutProblems(page);
    if (problems.length) throw new Error(`the sign-in card does not fit: ${problems.join('; ')}`);
    // No caret in a box: the picture is of the card, not of a field being typed in.
    await page.evaluate(() => document.activeElement?.blur());
    await page.mouse.move(4, 4);
    await shot(page, SET, 'login-captcha-dark-1440.png');

    // ── the administrator's page ─────────────────────────────────────────
    await page.click('button[type="submit"]');
    await page.waitForURL(/\/admin\/(home|dashboard|explore)/, { timeout: 20_000 });
    await page.setViewportSize({ width: 1440, height: 1500 });
    await page.goto(`${inst.url}/admin/bot-protection`);
    const root = page.getByTestId('bot-protection');
    await root.waitFor({ timeout: 20_000 });
    await page.waitForSelector('[data-testid="bot-protection-state"][data-state="on"]', { timeout: 20_000 });
    await mustSay(root, 'the Bot protection page', [
      'Bot protection',
      'Cloudflare Turnstile',
      'Site key',
      'Secret key',
      'From the environment (CAPTCHA_PROVIDER).',
      'Forms',
      'When the provider cannot be reached',
    ]);
    const secret = await page.locator('input[name="bot-protection-secret"]').inputValue();
    if (secret) throw new Error('the secret box shows a value: the secret must never be read back');
    const pageProblems = await layoutProblems(page);
    if (pageProblems.length) throw new Error(`the Bot protection page does not fit: ${pageProblems.join('; ')}`);
    await page.mouse.move(4, 4);
    await sleep(300);
    await shot(page, SET, 'bot-protection-dark-1440.png');
    await ctx.close();

    log('the sign-in card with Turnstile, and the Bot protection page');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
