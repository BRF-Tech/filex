/**
 * 246 - a folder dropped into the explorer from the operating system arrives
 * as a folder (task #246, GitHub issue #94).
 *
 * Up to 0.55 the explorer uploaded `dataTransfer.files`. A dropped folder is in
 * that list as ONE File named after the folder: Safari on macOS puts the folder
 * there ZIPPED, so the upload became an extensionless zip; Firefox an empty
 * File, which uploaded as an empty file named after the folder; Chromium a
 * placeholder that cannot be read, so the upload failed.
 * Now the drop is read through `items[i].webkitGetAsEntry()` (lib/dropUpload)
 * and the folder is walked and made again, empty folders included.
 *
 * Two drops, both measured on the server's listing and the bytes:
 *   - every engine: a DataTransfer shaped like Safari's (the entry API gives
 *     the folder, `files` gives a zip named after it). Playwright's WebKit is
 *     not macOS Safari and cannot drop a real folder, so this is the shape,
 *     handed to the explorer's own drop handler;
 *   - Chromium: a REAL folder from disk, dropped through the DevTools protocol
 *     (Input.dispatchDragEvent with the folder's path) - the browser builds the
 *     entries and the placeholder File itself, as it does for a Finder drop.
 *
 * What the old code made of each drop is logged before the assertion fails.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORE = `e2e-folderdrop246-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;

type Row = { basename: string; type: string; size?: number };

let api: APIRequestContext;

test.beforeAll(async ({ playwright, baseURL, request }) => {
  await seedLocalStorage(request, STORE, MOUNT);
  api = await newAuthedRequest(playwright, baseURL ?? '');
});

test.afterAll(async ({ request }) => {
  await api?.dispose();
  await dropStorageByName(request, STORE);
});

async function list(dir: string): Promise<Row[]> {
  const res = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://${dir}`)}`);
  if (!res.ok()) return [];
  return ((await res.json()).files ?? []) as Row[];
}

async function body(file: string): Promise<string> {
  const res = await api.get(`/api/files/manager?action=download&path=${encodeURIComponent(`${STORE}://${file}`)}`);
  return res.ok() ? res.text() : `<${res.status()}>`;
}

/** The storage's root, open in the admin app's explorer, its listing answered. */
async function openRoot(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORE)}`);
  const listed = page.waitForResponse(
    (r) => {
      const u = new URL(r.url());
      return u.searchParams.get('action') === 'index' && (u.searchParams.get('path') ?? '') === `${STORE}://`;
    },
    { timeout: 30_000 },
  );
  await page.getByTestId(`sidenav-storage-${STORE}`).click();
  await listed;
  await expect(page.locator('.fe').first()).toBeVisible();
  await page.waitForTimeout(300);
}

/** Wait for the dropped folder to be complete on the server, logging what is there first. */
async function expectFolder(name: string) {
  await expect
    .poll(
      async () => {
        const sub = await list(`${name}/sub`);
        return sub.map((r) => r.basename).includes('b.txt');
      },
      { timeout: 30_000, intervals: [500, 1000, 2000] },
    )
    .toBe(true)
    .catch(async (err) => {
      console.log(`246 ${name}: the root holds`, JSON.stringify(await list('')));
      throw err;
    });
  const root = await list('');
  console.log(`246 ${name}: the root holds`, JSON.stringify(root.filter((r) => r.basename === name)));
  const made = root.filter((r) => r.basename === name);
  expect(made, 'one entry named after the folder, and it is a folder').toHaveLength(1);
  expect(made[0]!.type).toBe('dir');
  const inside = await list(name);
  expect(inside.map((r) => `${r.basename}:${r.type}`).sort()).toEqual(['a.txt:file', 'empty:dir', 'sub:dir']);
  expect(await list(`${name}/empty`)).toEqual([]);
  expect(await body(`${name}/a.txt`)).toBe('alpha');
  expect(await body(`${name}/sub/b.txt`)).toBe('bravo');
}

test('a folder dropped the way Safari hands it over arrives as the folder, not as a zip', async ({ page }) => {
  test.setTimeout(120_000);
  const name = 'Safari246';
  await openRoot(page);
  await page.evaluate((folder) => {
    type Entry = Record<string, unknown>;
    const later = (fn: () => void) => setTimeout(fn, 0);
    const fileEntry = (n: string, text: string): Entry => ({
      isFile: true,
      isDirectory: false,
      name: n,
      file: (ok: (f: File) => void) => later(() => ok(new File([text], n, { type: 'text/plain' }))),
    });
    const dirEntry = (n: string, kids: Entry[]): Entry => ({
      isFile: false,
      isDirectory: true,
      name: n,
      createReader: () => {
        let given = false;
        return {
          readEntries: (ok: (e: Entry[]) => void) =>
            later(() => {
              ok(given ? [] : kids);
              given = true;
            }),
        };
      },
    });
    const tree = dirEntry(folder, [fileEntry('a.txt', 'alpha'), dirEntry('sub', [fileEntry('b.txt', 'bravo')]), dirEntry('empty', [])]);
    // What Safari puts in `files` for a dropped folder: the folder, zipped, named after it.
    const zip = new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x14, 0, 0, 0])], folder, { type: '' });
    const item = { kind: 'file', type: '', getAsFile: () => zip, webkitGetAsEntry: () => tree };
    const dt = { types: ['Files'], items: Object.assign([item], { length: 1 }), files: Object.assign([zip], { length: 1 }), dropEffect: 'copy', effectAllowed: 'all' };
    const root = document.querySelector('.fe');
    if (!root) throw new Error('no explorer');
    for (const type of ['dragenter', 'dragover', 'drop']) {
      const ev = new Event(type, { bubbles: true, cancelable: true });
      Object.defineProperty(ev, 'dataTransfer', { value: dt });
      root.dispatchEvent(ev);
    }
  }, name);
  await expectFolder(name);
});

test('a real folder dropped in Chromium arrives whole, empty folders included', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'only Chromium takes a folder from disk through the DevTools protocol');
  test.setTimeout(120_000);
  const name = 'Real246';
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-246-'));
  const folder = path.join(dir, name);
  fs.mkdirSync(path.join(folder, 'sub'), { recursive: true });
  fs.mkdirSync(path.join(folder, 'empty'));
  fs.writeFileSync(path.join(folder, 'a.txt'), 'alpha');
  fs.writeFileSync(path.join(folder, 'sub', 'b.txt'), 'bravo');

  await openRoot(page);
  const box = await page.locator('.fe').first().boundingBox();
  if (!box) throw new Error('the explorer has no box');
  const at = { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
  const cdp = await page.context().newCDPSession(page);
  const data = { items: [], files: [folder], dragOperationsMask: 1 };
  for (const type of ['dragEnter', 'dragOver', 'drop'] as const) {
    await cdp.send('Input.dispatchDragEvent', { type, ...at, data });
  }
  await expectFolder(name);
  fs.rmSync(dir, { recursive: true, force: true });
});
