/**
 * An app with its own interface, end to end: install it from a manifest and a
 * bundle the way the admin wizard does, remove it by name, and open its
 * `viewer` on a file - one copy for the specs that build such an app by hand
 * (175 the sandbox, 228 a frame of its own package and blob:, 229 ui.print).
 */
import { expect, type APIRequestContext, type Frame, type Page } from '@playwright/test';
import { loginAs } from './auth';

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
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

/**
 * Sign in, open the storage in the explorer, double-click `fileName` and wait
 * for the app's frame: drawn sandboxed, connected over the bridge, and its
 * script started (`document.body.dataset.ready`, or `.error` with the reason).
 * The interface's own frame.
 */
export async function openAppInterface(page: Page, store: string, fileName: string): Promise<Frame> {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(store)}`);
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
