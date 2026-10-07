/**
 * Replication, end to end (#186, GitHub Discussion #91): a storage linked to a
 * replication target on the Replication page really sends its files there.
 *
 * ⚠ Before 0.53 the link was saved and nothing else happened: the server
 * handed out the bare driver of a linked storage, so nothing fanned out, no
 * failure was recorded and no notification raised - a Hetzner Storage Box
 * stayed empty with nothing in the log. And what the storage held before the
 * link was never copied at all.
 *
 * Measured here, against a real binary, both halves:
 *   1. the files the storage ALREADY held are copied by the initial copy,
 *      whose progress the page shows until it says "Done" (a `skip` rule's
 *      path stays out);
 *   2. a file uploaded after the link reaches the target by itself.
 *
 * The target is a local directory, so the spec reads it from disk; with an
 * SMB or SFTP target the path through filex is the same (the wrapper does
 * not know which driver is behind it).
 */
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';

import { test, expect, type APIRequestContext } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { pickOption } from '../helpers/choiceSelect';
import { dropStorageByName, seedLocalStorage, storageRoot, waitForOp } from '../helpers/seed';

const STAMP = Date.now();
const STORAGE = `e2e-repl-${STAMP}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const TARGET = `e2e-repl-target-${STAMP}`;
const TARGET_DIR = storageRoot(`/tmp/filex-${TARGET}`);

let storageId = 0;
let targetId = 0;
let ruleId = 0;

/** The storage's own folder on the target (the page says it; set once linked). */
let folder = '';
const onTarget = (rel: string) => path.join(TARGET_DIR, folder, ...rel.split('/'));

async function upload(request: APIRequestContext, name: string, body: string) {
  const data = Buffer.from(body);
  const begin = await request.post('/api/files/upload/begin', {
    data: {
      path: `${STORAGE}://`,
      name,
      size: data.length,
      mime: 'text/plain',
      hash: `sha256:${createHash('sha256').update(data).digest('hex')}`,
    },
  });
  expect(begin.ok(), await begin.text()).toBeTruthy();
  const { id } = await begin.json();
  const put = await request.put(`/api/files/upload/${id}`, {
    headers: { 'content-range': `bytes 0-${data.length - 1}/${data.length}`, 'content-type': 'application/octet-stream' },
    data,
  });
  expect(put.ok(), await put.text()).toBeTruthy();
  const commit = await request.post(`/api/files/upload/${id}/commit`);
  expect(commit.status(), await commit.text()).toBeLessThan(300);
  const done = await commit.json();
  if (done.op_id) await waitForOp(request, done.op_id, 60_000);
}

test.describe('Replication sends a linked storage to its target', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    // What the storage holds before it is linked: the initial copy's work.
    const root = storageRoot(MOUNT);
    for (const [rel, body] of [
      ['eski/rapor.txt', 'before the link'],
      ['kök.txt', 'root file'],
      ['geçici/x.tmp', 'never copied'],
      // filex's own folders never reach a target.
      ['.thumbs/t.jpg', 'internal'],
      ['.versions/1/1', 'internal'],
    ] as const) {
      fs.mkdirSync(path.join(root, path.dirname(rel)), { recursive: true });
      fs.writeFileSync(path.join(root, rel), body);
    }
    fs.mkdirSync(TARGET_DIR, { recursive: true });
    const row = await seedLocalStorage(request, STORAGE, MOUNT, { sync_mode: 'ondemand' });
    storageId = row.id;

    await apiLogin(request);
    const t = await request.post('/api/admin/replication-targets', {
      data: { name: TARGET, driver: 'local', config: { path: TARGET_DIR }, mode: 'async', enabled: true },
    });
    expect(t.status(), await t.text()).toBe(201);
    targetId = (await t.json()).id;
    const r = await request.post('/api/admin/replica/rules', {
      data: { path_pattern: 'geçici/**', mode: 'skip', priority: 1, enabled: true, description: 'e2e' },
    });
    expect(r.ok(), await r.text()).toBeTruthy();
    ruleId = (await r.json()).id;
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    if (ruleId) await request.delete(`/api/admin/replica/rules/${ruleId}`);
    await dropStorageByName(request, STORAGE);
    if (targetId) await request.delete(`/api/admin/replication-targets/${targetId}`);
  });

  test('link on the page: what it held is copied, and what it is sent afterwards follows', async ({ page, request }) => {
    await loginAs(page);
    await page.goto('/admin/replica');

    // Link it, the way a person does.
    await pickOption(page.getByTestId(`replica-pair-${storageId}`), String(targetId));
    await expect(page.getByText('Pairing saved. The files already on the storage are being copied to the target.')).toBeVisible();

    // The storage writes into a folder of its own on the target, named after
    // it, and the row says which.
    await apiLogin(request);
    const links = await request.get('/api/admin/replica/links');
    expect(links.ok(), await links.text()).toBeTruthy();
    const mine = ((await links.json()).items as Array<{ storage_id: number; folder: string }>).find((l) => l.storage_id === storageId);
    expect(mine?.folder, 'the linked storage has no folder on the target').toBe(STORAGE);
    folder = mine!.folder;
    await expect(page.getByTestId(`replica-pair-folder-${storageId}`)).toContainText(`${folder}/`);

    // The initial copy shows on the storage's row and ends "Done".
    const status = page.getByTestId(`replica-initial-${storageId}`);
    await expect(status).toBeVisible();
    await expect(status).toHaveAttribute('data-phase', 'done', { timeout: 60_000 });
    await expect(page.getByTestId(`replica-initial-copied-${storageId}`)).toContainText(/\d+ copied/);
    await expect(page.getByTestId(`replica-initial-excluded-${storageId}`)).toContainText('1 left out by a rule');

    expect(fs.readFileSync(onTarget('eski/rapor.txt'), 'utf8')).toBe('before the link');
    expect(fs.readFileSync(onTarget('kök.txt'), 'utf8')).toBe('root file');
    expect(fs.existsSync(onTarget('geçici/x.tmp')), 'a skip path reached the target').toBe(false);
    expect(fs.existsSync(path.join(TARGET_DIR, 'kök.txt')), 'a storage wrote into the target root').toBe(false);
    for (const internal of ['.thumbs', '.versions']) {
      expect(fs.existsSync(onTarget(internal)), `${internal} reached the target`).toBe(false);
    }

    // A file sent after the link reaches the target by itself.
    await apiLogin(request);
    await upload(request, 'sonra.txt', 'after the link');
    await expect
      .poll(() => (fs.existsSync(onTarget('sonra.txt')) ? fs.readFileSync(onTarget('sonra.txt'), 'utf8') : ''), {
        timeout: 30_000,
      })
      .toBe('after the link');

    // Nothing failed on the way.
    const failures = await request.get('/api/admin/replica/failures/count');
    expect(failures.ok()).toBeTruthy();
    expect((await failures.json()).count).toBe(0);

    // Unlinked: a file sent now stays home.
    await pickOption(page.getByTestId(`replica-pair-${storageId}`), '0');
    await expect(page.getByText('Pairing saved.', { exact: true })).toBeVisible();
    await upload(request, 'unlinked.txt', 'stays home');
    await page.waitForTimeout(1500);
    expect(fs.existsSync(onTarget('unlinked.txt')), 'an unlinked storage still fanned out').toBe(false);
  });
});
