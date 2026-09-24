/**
 * Aim at an element only once it has stopped changing — and hit THAT element.
 *
 * ⚠⚠ WHY. Spec 106 right-clicks a file in the explorer's virtual views and on
 * the Home cards, and it flaked all through v0.43.0: the click landed, the
 * row or card under it was replaced a moment later, the contextmenu handler
 * found nothing selected, and the EMPTY-BACKGROUND menu opened — which reads
 * like "a verb is missing" rather than "the click missed". Its first guard
 * compared two bounding boxes 100 ms apart. That catches a row that MOVES,
 * and not one that is REDRAWN IN PLACE: a list that re-renders when a late
 * answer lands can put a brand-new node exactly where the old one was, so two
 * identical boxes proved nothing, and Playwright's own stability check inside
 * `click()` looks at the node it already resolved and cannot see the swap.
 *
 * `settled` therefore asks three things, over several samples:
 *   - the locator still resolves to the SAME DOM node,
 *   - that node is still attached,
 *   - and its box has not changed —
 * and returns that node, so the click that follows goes to exactly the element
 * that was measured, not to whatever the locator resolves to a frame later.
 */
import { expect, type ElementHandle, type Locator } from '@playwright/test';

export interface SettledOptions {
  /** How many reads must agree (the first included). */
  samples?: number;
  /** Milliseconds between reads. */
  interval?: number;
  /** How long to keep trying before failing. */
  timeout?: number;
}

export async function settled(
  locator: Locator,
  { samples = 4, interval = 120, timeout = 10_000 }: SettledOptions = {},
): Promise<ElementHandle<Element>> {
  await expect(locator, 'the element is on screen').toBeVisible({ timeout });
  const page = locator.page();
  let held: ElementHandle<Element> | null = null;
  await expect(async () => {
    if (held) await held.dispose();
    held = (await locator.elementHandle()) as ElementHandle<Element> | null;
    expect(held, 'the locator resolves to an element').not.toBeNull();
    const first = await held!.boundingBox();
    expect(first, 'the element has a box').not.toBeNull();
    for (let i = 1; i < samples; i++) {
      await page.waitForTimeout(interval);
      const now = (await locator.elementHandle()) as ElementHandle<Element> | null;
      const sameNode = now ? await now.evaluate((a, b) => a === b && a.isConnected, held!) : false;
      if (now) await now.dispose();
      expect(sameNode, 'the element was replaced (the list redrew under it)').toBe(true);
      const box = await held!.boundingBox();
      expect(
        !!box && box.x === first!.x && box.y === first!.y && box.width === first!.width && box.height === first!.height,
        'the element is still moving',
      ).toBe(true);
    }
  }).toPass({ timeout });
  return held!;
}
