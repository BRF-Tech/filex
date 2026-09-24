/**
 * 140-scan-exclusions — issue #44, in a real browser against a real binary.
 *
 * A storage's *Paths to exclude from scanning* is drawn by the storage editor
 * from the descriptor endpoint's `scan_fields` (no hand-built form), saved in
 * the storage's `config`, applied by the syncer the edit restarts, and honoured
 * by the scan: nothing under a matching path is catalogued, so the explorer's
 * listing (which is the catalogue) does not show it.
 *
 * What is measured, in order:
 *   · the setting is on the editor, with its label and the help that says it
 *     is not access control;
 *   · a pattern that would exclude everything is refused OUT LOUD, and not
 *     saved;
 *   · a working set of patterns is saved, survives a reload, and the next
 *     scan leaves `.git`, `*.tmp` and `downloads/incomplete/**` out of the
 *     catalogue while everything else is catalogued as before.
 */
import fs from 'node:fs';
import path from 'node:path';

import { test, expect, type APIRequestContext } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, storageRoot } from '../helpers/seed';

const STORAGE = `e2e-scanx-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
let storageId = 0;

async function names(request: APIRequestContext, rel = ''): Promise<string[]> {
  const res = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://${rel}`)}`);
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = (await res.json()) as { files?: Array<{ basename: string }> };
  return (body.files ?? []).map((f) => f.basename).sort();
}

/** Starts a full scan and waits until a run newer than the ones before it is over. */
async function scan(request: APIRequestContext) {
  const runs = async () => {
    const res = await request.get(`/api/admin/storages/${storageId}/sync-runs?limit=50`);
    expect(res.ok()).toBeTruthy();
    return ((await res.json()) as { entries?: Array<{ id: number; status: string }> }).entries ?? [];
  };
  const before = new Set((await runs()).map((r) => r.id));
  const started = await request.post(`/api/admin/storages/${storageId}/sync`);
  expect(started.status(), await started.text()).toBeLessThan(300);
  await expect
    .poll(async () => (await runs()).find((r) => !before.has(r.id) && r.status !== 'running')?.status ?? 'pending', {
      timeout: 30_000,
    })
    .toBe('ok');
}

test.beforeAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
  const root = storageRoot(MOUNT);
  for (const rel of ['keep.txt', 'junk.tmp', '.git/HEAD', 'downloads/complete/a.txt', 'downloads/incomplete/part.001']) {
    fs.mkdirSync(path.join(root, path.dirname(rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), rel);
  }
  // On demand: nothing is catalogued until the patterns are in place.
  const row = await seedLocalStorage(request, STORAGE, MOUNT, { sync_mode: 'ondemand' });
  storageId = row.id;
});

test.afterAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
});

test('a storage can be told what not to scan', async ({ page, request }) => {
  await loginAs(page);
  await page.goto(`/admin/storages/${storageId}`);

  const field = page.getByTestId('storage-scan-fields');
  await expect(field).toContainText('Paths to exclude from scanning');
  await expect(field).toContainText('it is not access control');
  const box = field.locator('textarea');
  await expect(box).toBeVisible();

  // Refused where it is typed, in words, and nothing is saved.
  await box.fill('**');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('would exclude everything on this storage')).toBeVisible();
  await apiLogin(request);
  let row = await (await request.get(`/api/admin/storages/${storageId}`)).json();
  expect(row.config?.scan_exclude ?? '').toBe('');

  // A working set is saved and survives a reload.
  const patterns = '.git\n*.tmp\ndownloads/incomplete/**';
  await box.fill(patterns);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect.poll(async () => (await (await request.get(`/api/admin/storages/${storageId}`)).json()).config?.scan_exclude).toBe(patterns);
  await page.reload();
  await expect(page.getByTestId('storage-scan-fields').locator('textarea')).toHaveValue(patterns);

  // The scan the edit's syncer runs leaves them out of the catalogue.
  await scan(request);
  row = await (await request.get(`/api/admin/storages/${storageId}`)).json();
  expect(row.last_sync_at, 'the scan finished').toBeTruthy();
  expect(await names(request)).toEqual(['downloads', 'keep.txt']);
  expect(await names(request, 'downloads')).toEqual(['complete']);
  expect(await names(request, 'downloads/complete')).toEqual(['a.txt']);
});
