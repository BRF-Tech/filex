/**
 * An app with its own interface, end to end: install it from a manifest and a
 * bundle the way the admin wizard does, remove it by name, and open its
 * `viewer` on a file - one copy for the specs that build such an app by hand
 * (175 the sandbox, 228 a frame of its own package and blob:, 229 ui.print).
 */
import { expect, type APIRequestContext, type Frame, type Locator, type Page } from '@playwright/test';
import { openSignedIn } from './auth';

/** One row of the install review (wasmplugin.PermissionRow). */
export interface ReviewRow {
  id: string;
  label: string;
}

/**
 * Install an interface app from its manifest and `ui.zip`: a dry run, then
 * the install granting exactly what the review listed (the manifest's own
 * permissions plus the ones filex derives from the `ui` block). Returns the
 * review's rows.
 */
export async function installInterfaceApp(api: APIRequestContext, manifest: Record<string, unknown>, bundle: Buffer): Promise<ReviewRow[]> {
  const files = {
    manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(manifest)) },
    ui: { name: 'ui.zip', mimeType: 'application/zip', buffer: bundle },
  };
  const dry = await api.post('/api/admin/app-plugins?dry_run=1', { multipart: { ...files, grant: JSON.stringify({ permissions: [] }) } });
  expect(dry.ok(), `dry run: ${dry.status()} ${await dry.text()}`).toBe(true);
  const rows = ((await dry.json()) as { permissions: ReviewRow[] }).permissions;
  const inst = await api.post('/api/admin/app-plugins', {
    multipart: { ...files, grant: JSON.stringify({ permissions: rows.map((p) => p.id) }) },
  });
  expect(inst.ok(), `install: ${inst.status()} ${await inst.text()}`).toBe(true);
  return rows;
}

/** Remove every installed app of that name; nothing when there is none. */
export async function removeAppByName(api: APIRequestContext, name: string): Promise<void> {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of ((await list.json()) as { plugins?: Array<{ id: number; name: string }> }).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}?confirm=${encodeURIComponent(p.name)}`);
  }
}

/**
 * Sign in, open the storage in the explorer, double-click `fileName` and wait
 * for the app's frame: drawn sandboxed, connected over the bridge, and its
 * script started (`document.body.dataset.ready`, or `.error` with the reason).
 * The interface's own frame.
 */
export async function openAppInterface(page: Page, store: string, fileName: string): Promise<Frame> {
  // Signed in through the API with the explorer as the tab's first page
  // (helpers/auth openSignedIn, task #182: 175's row waited out its 10 s in
  // WebKit after the form's start page was left mid-load).
  await openSignedIn(page, `/admin/explore?storage=${encodeURIComponent(store)}`);
  const row = page.locator(`[data-fe-path="${store}://${fileName}"]`);
  await row.first().waitFor();
  await row.getByText(fileName, { exact: true }).dblclick();
  const el = page.locator('iframe[data-testid="app-frame"]');
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts');
  await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
  const frame = (await (await el.elementHandle())!.contentFrame())!;
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
  expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
  return frame;
}

/**
 * Click `button` of a question filex draws OVER an app's frame (the changes
 * an earlier opening left, 235; a print, 229) until the question is gone.
 *
 * ⚠ Task #363 (2026-11-18): in Firefox a click made the moment such a row
 * appears can be lost. ci.yml 44230897479, part "firefox 3/4": 235 clicked
 * "Apply my changes" 76 ms after the row showed (on the first attempt and
 * again on the retry) and 229 "Don't allow" 77 ms after (its first attempt).
 * Playwright logged "click action done" each time, yet no handler ran (no
 * lease request followed the apply, the print's answer never came in 30 s)
 * and the row stayed. Playwright's hit-target check counts a click whose
 * events never reached the page as done; the likeliest place they went is
 * the app's frame under the row, which the browser had not yet painted the
 * row over. The row's handlers are sound, and a person never clicks within a
 * frame or two of a row appearing: the race is the spec's click, so it is
 * made again until it takes. A second click that does reach the row is
 * harmless: the answer is taken once (AppFrame answerUnsent clears the
 * question, denyPrint aborts).
 */
export async function clickOverFrame(button: Locator, question: Locator, { timeout = 20_000 }: { timeout?: number } = {}): Promise<void> {
  await expect(async () => {
    if (await question.isVisible()) await button.click({ timeout: 2_000 });
    await expect(question).toBeHidden({ timeout: 1_000 });
  }, 'the question over the frame took the click').toPass({ timeout, intervals: [250, 500, 1_000] });
}
