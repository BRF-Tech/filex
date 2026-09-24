/**
 * Walking an app's wizard (the plugin page's footer), in ONE place.
 *
 * ⚠⚠ WHY THIS FILE EXISTS. The signing specs each carried their own copy of
 * "press the footer's last button", and the copies drifted apart:
 *
 *   - 129 and 130 waited for the button to be ENABLED, then clicked
 *     `.last()`. The wait is the hazard: the step on screen when the caller
 *     decided to press is not the step on screen when the press lands, and
 *     on the REVIEW step the footer's last action is Send. A press that
 *     arrived a moment late SENT the request (seen on bc92f3a7 in 129).
 *   - 129 was fixed in place. 130 kept the old copy, passed by luck on the
 *     next full run, and failed on the v0.43.0 release run the same way —
 *     its screenshot shows the request queued, the product doing everything
 *     right. A fix made in one copy is a bug left in the others.
 *   - 99 and 113 carried an older, weaker copy (`.last().click()` with no
 *     wait at all — on a review step that IS Send). All four specs use this
 *     file now; there is no copy left to drift.
 *
 * So there is one copy, and it cannot turn a step into an act:
 *
 *   next(page)          presses the button that MOVES ON, and nothing else.
 *                       ⚠⚠ An ALLOWLIST, not a denylist: it presses the
 *                       footer's last button only when that button is `next`
 *                       (which is also "Start"). Every other action — Send,
 *                       Sign, and the status page's Expire, which a stray
 *                       press would use to kill a live request — is refused,
 *                       and so is any action the app adds tomorrow. It reads
 *                       the target AFTER the enabled-wait and clicks it BY ITS
 *                       OWN ID: a footer redrawn in between can make the click
 *                       miss loudly, never turn it into an act.
 *   advance(page)       one step forward, PROVED: `next`, then wait for the
 *                       wizard's own spine to show another step. Two presses
 *                       in a row with nothing between them can both land on
 *                       the same step (the first one's round trip had not come
 *                       back); this is the one to use for "move on N steps".
 *   walk(page, arrived) presses `next` until `arrived()` is true, waiting for
 *                       the STEP ITSELF to change between presses — never a
 *                       fixed pause (lesson #360).
 *   press(page, id)     the ONE way to perform an act: named, on purpose.
 */
import { expect, type Page } from '@playwright/test';

/** The button that moves on (and, on a first screen, "Start"). */
export const NEXT = 'plugin-page-action-next';

/** The requester's final action: sends the signature request. */
export const SEND = 'plugin-page-action-send';

/** A signer's final action: signs the document. */
export const SIGN = 'plugin-page-action-sign';

/** The ONLY actions `next` presses. Anything else is an act, done with `press`. */
export const ADVANCE_ACTIONS: readonly string[] = [NEXT];

/** Which step the wizard says it is on, read off its own spine. */
export async function activeStep(page: Page): Promise<string> {
  const el = page.locator('.fe-steps__item[aria-current="step"]');
  if ((await el.count()) === 0) return '';
  return ((await el.first().innerText()) ?? '').replace(/\s+/g, ' ').trim();
}

/**
 * Press the button that MOVES ON — and nothing that ACTS.
 *
 * ⚠ The footer is redrawn on every step: waiting for the button to be
 * ENABLED, not merely present, keeps the walk off a button that belongs to
 * the step just left. The target is read AFTER that wait and clicked by its
 * own test id, and only when it is an advance (`ADVANCE_ACTIONS`).
 *
 * @returns the id pressed, or `null` when the footer's last action is not an
 * advance (a review's Send, a signer's Sign, a status page's Expire) — there
 * is nothing left to move on to, and nothing was pressed.
 */
export async function next(page: Page): Promise<string | null> {
  const btn = page.locator('[data-testid^="plugin-page-action-"]').last();
  await expect(btn).toBeEnabled({ timeout: 20_000 });
  const id = await btn.getAttribute('data-testid');
  expect(id, 'the footer drew no action to press').toBeTruthy();
  if (!ADVANCE_ACTIONS.includes(id!)) return null;
  await page.getByTestId(id!).click();
  return id;
}

/**
 * Move on exactly ONE step, and prove it: the wizard's spine must show a
 * different step afterwards. Fails loudly when there is nothing to move on
 * to — a caller that asked to advance and got an act on the footer has a
 * wrong idea of where it is, and should hear so here, not three lines later.
 *
 * ⚠ Needs the wizard's step spine (`.fe-steps__item`), which the request
 * wizard draws. On a screen without one, use `next` and assert the content of
 * the step you expect instead.
 */
export async function advance(page: Page): Promise<void> {
  const before = await activeStep(page);
  expect(before, 'advance() needs a wizard that draws its step spine').not.toBe('');
  const id = await next(page);
  expect(id, `advance(): the footer offers no step to move on to (on "${before}")`).not.toBeNull();
  await expect
    .poll(() => activeStep(page), { timeout: 20_000, message: `the wizard left "${before}"` })
    .not.toBe(before);
}

/**
 * Walk forward until `arrived()` is true, waiting for the step to CHANGE
 * between presses (lesson #360 — decide from the screen after it moved, never
 * straight after the click). Stops without pressing when the review step is
 * up, so a walk can never send.
 */
export async function walk(page: Page, arrived: () => Promise<boolean>, presses = 6): Promise<void> {
  for (let i = 0; i < presses; i++) {
    if (await arrived()) return;
    const before = await activeStep(page);
    if ((await next(page)) === null) return;
    await expect
      .poll(async () => ((await arrived()) ? 'ARRIVED' : await activeStep(page)), { timeout: 20_000 })
      .not.toBe(before);
  }
}

/**
 * Draw a signature on the signing step's pad — once the pad is on screen.
 *
 * ⚠ It waits for the pad FIRST. Each signing spec had its own copy that read
 * `boundingBox()` straight away, usually right after the Start press; for a
 * step not yet drawn that answers null, and the `!` after it turned a race
 * into a TypeError three lines later. Same stroke as the four copies it
 * replaces (99, 113, 129, 130).
 */
export async function drawSignature(page: Page): Promise<void> {
  const canvas = page.getByTestId('surface-signature-canvas').first();
  await expect(canvas, 'the signature pad is on screen').toBeVisible({ timeout: 20_000 });
  const pad = (await canvas.boundingBox())!;
  await page.mouse.move(pad.x + 30, pad.y + pad.height / 2);
  await page.mouse.down();
  for (let k = 0; k < 15; k++) await page.mouse.move(pad.x + 30 + k * 15, pad.y + pad.height / 2 + ((k % 3) - 1) * 10);
  await page.mouse.up();
}

/** Perform one named footer action on purpose — Send, Sign, or any other act. */
export async function press(page: Page, id: string): Promise<void> {
  const btn = page.getByTestId(id);
  await expect(btn, `${id} is on the footer and pressable`).toBeEnabled({ timeout: 20_000 });
  await btn.click();
}
