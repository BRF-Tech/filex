/**
 * 126-rtl-server-text — a sentence the SERVER wrote, drawn in Arabic.
 *
 * ⚠⚠ What this measures, and why a unit test cannot. A `server.*` message
 * reaches the admin panel as the `message` of a refusal (`extractError`), so
 * it never passes through vue-i18n and the panel's post-translation hook —
 * the hook that isolates a machine run inside a right-to-left sentence — and
 * the catalogue's own copy of the SAME sentence is drawn correctly. Found by
 * measuring the Arabic pack against the merged tree (v0.43.0):
 *
 *   `server.token.scope_unknown` names the syntax `root:<storage>://<folder>`.
 *   The closing `>` is a neutral between the Latin run and the Arabic after
 *   it, so the bidi algorithm gives it the PARAGRAPH's direction: it is drawn
 *   at the FAR LEFT of the run and, being a mirrored character, as `<`. The
 *   line read `…<root:<storage>://<folder`.
 *
 * Measured here before the fix, in this browser: `<folder>` came back in TWO
 * rectangles — `750–758` (the displaced `>`, drawn BEFORE `<storage>` at 758)
 * and `827–866` — and the drawn text carried no isolate marks at all. With
 * the fix: one rectangle at `818–866`, after `://`, and two marks in the text.
 *
 * A pack cannot fix it: bidi control characters are forbidden in a translation
 * and the pack's own validator refuses them. It is the render site's job
 * (core lib/direction `foreignText`).
 *
 * ⚠ The refusal is served by `page.route()` rather than provoked: the create
 * form offers fixed checkboxes, so no click can make the server answer
 * `scope_unknown`. Everything else — the pack, the language, the direction,
 * `extractError`, the template — is the product.
 *
 * ⚠ This file used to install the pack BEFORE opening any page, because
 * `/api/public/branding` — which carries the offered languages and their
 * `rtl` flag — was served `Cache-Control: public, max-age=60`, so a language
 * installed a moment ago was not on the picker of a page reloaded seconds
 * later. That was not a test problem; it was the defect a person meets as "I
 * installed it and nothing happened". The answer is revalidated now
 * (backend handlers/public_cache.go), so the first test installs the pack
 * with a page already open and reloads — which is both the natural shape and
 * the proof.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';
import { arabicPack, installLangPack, removeLangPack } from '../helpers/langPack';

/** The Arabic sentence under test, exactly as the pack writes it. */
const AR_SCOPE_UNKNOWN =
  '\u00ab{scope}\u00bb \u0644\u064a\u0633 \u0625\u0630\u0646\u064b\u0627 \u064a\u0635\u062f\u0631\u0647 \u0647\u0630\u0627 \u0627\u0644\u062e\u0627\u062f\u0645 (\u00abread, write, delete, mcp, admin\u00bb \u0623\u0648 root:<storage>://<folder>\u060c \u0645\u0639 \u0648\u0636\u0639 \u0627\u0633\u0645 \u0648\u062d\u062f\u0629 \u0627\u0644\u062a\u062e\u0632\u064a\u0646 \u0648\u0645\u0633\u0627\u0631 \u0627\u0644\u0645\u062c\u0644\u062f \u0645\u0643\u0627\u0646\u0647\u0645\u0627).';

/* The pack, and the one sentence this file measures written into it — the
   helper is shared with the other specs that need Arabic (helpers/langPack). */
const PACK = arabicPack({ 'server.token.scope_unknown': AR_SCOPE_UNKNOWN });

async function removePack(api: APIRequestContext) {
  await removeLangPack(api, PACK);
}

const PREFS = '/api/me/prefs?surface=web';
let prefsBefore: Record<string, unknown> = {};

interface Fragment {
  part: string;
  x: number;
  right: number;
  y: number;
  pieces: number;
}

/**
 * Where each fragment of the run is DRAWN, and in how many pieces.
 *
 * ⚠ `Range.getClientRects()`, not `getBoundingClientRect()`: a fragment the
 * bidi algorithm split across the line reports ONE union box covering both
 * pieces, and a test that reads the union sees nothing wrong (filex lesson
 * #373, the same trap in a table cell). The piece COUNT is the measurement.
 */
async function drawnRun(page: Page, testId: string, parts: string[]) {
  return page.evaluate(
    ({ testId: id, parts: want }) => {
      const host = document.querySelector(`[data-testid="${id}"]`) as HTMLElement | null;
      if (!host) return { missing: '(the element)', text: '' };
      const walker = document.createTreeWalker(host, NodeFilter.SHOW_TEXT);
      const nodes: Text[] = [];
      for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n as Text);
      const out: Array<{ part: string; x: number; right: number; y: number; pieces: number }> = [];
      for (const part of want) {
        let found: { x: number; right: number; y: number; pieces: number } | null = null;
        for (const node of nodes) {
          const at = (node.data ?? '').indexOf(part);
          if (at < 0) continue;
          const range = document.createRange();
          range.setStart(node, at);
          range.setEnd(node, at + part.length);
          const rects = Array.from(range.getClientRects()).filter((r) => r.width > 0);
          if (!rects.length) continue;
          found = {
            x: Math.min(...rects.map((r) => r.x)),
            right: Math.max(...rects.map((r) => r.right)),
            y: Math.round(rects[0].y),
            pieces: rects.length,
          };
          break;
        }
        if (!found) return { missing: part, text: host.textContent ?? '' };
        out.push({ part, ...found });
      }
      const text = host.textContent ?? '';
      return {
        fragments: out,
        text,
        dir: getComputedStyle(host).direction,
        marks: (text.match(/[\u2066-\u2069]/g) ?? []).length,
      };
    },
    { testId, parts },
  );
}

test.describe.serial('RTL — the text the SERVER writes is isolated too', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ playwright, baseURL }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    prefsBefore = got.ok() ? ((await got.json()).prefs ?? {}) : {};
    await removePack(api);
  });

  /** Install the Arabic pack the way the admin panel does. */
  async function installPack(): Promise<void> {
    await installLangPack(api, PACK);
  }

  test.afterAll(async () => {
    await removePack(api);
    await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api.patch('/api/auth/profile', { data: { locale: 'en' } }).catch(() => undefined);
    await api.dispose();
  });

  /**
   * ⚠⚠ An installed language is offered on the NEXT page load.
   *
   * The page is opened FIRST, so it has already read the offered-language
   * list and been told Arabic is not among them; only then is the pack
   * installed, and the reload has to change the picker. While
   * `/api/public/branding` was `max-age=60`, this reload showed the same two
   * languages and a person had no way to tell a slow cache from a failed
   * install. The answer is revalidated now, so the reload asks and is told.
   */
  test('a language pack installed with the panel open is offered on the next load', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/dashboard?settings=1');
    await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await expect(page.getByTestId('user-settings-locale-en')).toBeVisible();
    await expect(page.getByTestId('user-settings-locale-ar'), 'nothing is installed yet').toHaveCount(0);

    await installPack();

    // ⚠ A fresh navigation, not `page.reload()`: the dialog takes `?settings=1`
    // off the address once it is open, so a reload would load the page without
    // it and there would be no dialog to look at.
    await page.goto('/admin/dashboard?settings=1');
    await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await expect(
      page.getByTestId('user-settings-locale-ar'),
      'the language was installed and the picker did not hear about it',
    ).toBeVisible({ timeout: 5_000 });
  });

  test('a refused API key says the token syntax in one unbroken run', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);

    await page.goto('/admin/dashboard?settings=1');
    await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await page.getByTestId('user-settings-locale-ar').click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'ar');
    // The server said this language is written right to left, and the page
    // followed (lib/direction syncDocumentDir).
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await page.keyboard.press('Escape');

    // The server's refusal, in the pack's Arabic — the answer the real handler
    // gives for an unknown scope (handlers/ai_tokens_admin.go).
    const message = AR_SCOPE_UNKNOWN.replace('{scope}', 'sudo');
    await page.route('**/api/admin/ai-tokens', async (route) => {
      if (route.request().method() !== 'POST') return route.fallback();
      await route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'scope_unknown', message }),
      });
    });

    await page.goto('/admin/api-mcp');
    await page.getByTestId('ai-token-new').click();
    await page.locator('form').getByRole('textbox').first().fill('rtl-e2e');
    await page.getByTestId('ai-token-scope-read').setChecked(true);
    await page.getByTestId('ai-token-create').click();

    const said = page.getByTestId('ai-token-create-error');
    await expect(said).toBeVisible({ timeout: 10_000 });
    await expect(said).toContainText('root:');

    const m = await drawnRun(page, 'ai-token-create-error', ['root:', '<storage>', '://', '<folder>']);
    expect((m as { missing?: string }).missing, 'a fragment is not in the drawn text').toBeUndefined();
    const { fragments, dir, marks } = m as { fragments: Fragment[]; dir: string; marks: number };
    expect(dir, 'the line is laid out right to left').toBe('rtl');

    // ⚠⚠ THE MEASUREMENT. Without isolation the trailing `>` of `<folder>`
    // takes the paragraph's direction and is drawn at the far left of the
    // run, so `<folder>` comes back as TWO rectangles.
    const shape = fragments.map((f) => `${f.part}@${Math.round(f.x)}..${Math.round(f.right)}/y${f.y}x${f.pieces}`).join(' ');
    for (const f of fragments) {
      expect(f.pieces, `"${f.part}" is drawn in ${f.pieces} pieces — the run is broken: ${shape}`).toBe(1);
    }
    expect(marks, 'the sentence carries the isolate marks that made that true').toBeGreaterThan(0);

    // …and on each line the pieces read left to right, each starting where the
    // one before it ended. ⚠ Per LINE: the sentence is long enough to wrap,
    // and an isolate does not stop a line from breaking inside it.
    const lines = new Map<number, Fragment[]>();
    for (const f of fragments) lines.set(f.y, [...(lines.get(f.y) ?? []), f]);
    for (const [y, row] of lines) {
      for (let i = 1; i < row.length; i++) {
        expect(row[i].x, `on line y=${y} "${row[i].part}" is drawn before "${row[i - 1].part}": ${shape}`).toBeGreaterThan(row[i - 1].x);
        const gap = row[i].x - row[i - 1].right;
        expect(Math.abs(gap), `a ${Math.round(gap)}px gap before "${row[i].part}": ${shape}`).toBeLessThan(3);
      }
    }
  });
});
