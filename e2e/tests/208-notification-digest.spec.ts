/**
 * 208-notification-digest - the notification digest (task #166, backend
 * internal/notify digest.go, docs/NOTIFICATIONS.md → The digest), on a real
 * server and in a real browser.
 *
 * The digest is opt-in: out of the box every kind is told at once. This
 * account holds new files for the digest, and thirty files uploaded into one
 * folder within a minute are then thirty rows in the list at once, read,
 * counted by neither the badge nor the unread list - so the browser and the
 * desktop app raise no pop-up for them. When the window ends ONE notification
 * says "Rapor: 30 files added", unread, and the bell shows it. Before the
 * digest that was thirty unread rows, thirty badge steps and thirty pop-ups
 * (the owner's report, 2026-10-05).
 *
 * ⚠ An account of its own, made here: the run's administrator is shared by
 * every spec, and a choice to hold new files on it would turn their uploads
 * into digests too.
 *
 * ⚠ The window is the server's shortest, one minute, and the background pass
 * runs every 10 seconds: this spec waits about 75 seconds, by design.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

interface Row {
  id: number;
  event: string;
  read_at?: string;
  meta?: {
    target?: { storage?: string; path?: string };
    groups?: Array<{ storage?: string; path?: string; counts?: Record<string, number> }>;
  };
}

const PASSWORD = 'digest-reader-pw-2026';

test.describe.serial('the notification digest', () => {
  let admin: APIRequestContext;
  let reader: APIRequestContext;
  let store = '';
  let email = '';

  async function rows(query: string): Promise<Row[]> {
    const res = await reader.get(`/api/notifications?${query}`);
    expect(res.ok(), `notifications: ${res.status()}`).toBe(true);
    return ((await res.json()).items ?? []) as Row[];
  }

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    const stamp = Date.now();
    store = `e2e-digest-${tag}-${stamp}`;
    email = `digest-${tag}-${stamp}@example.com`;
    admin = await newAuthedRequest(playwright, baseURL ?? '');
    await seedLocalStorage(admin, store, `/tmp/filex-${store}`);
    const made = await admin.post('/api/admin/users', { data: { email, password: PASSWORD, role: 'admin' } });
    expect(made.ok(), `user: ${made.status()} ${await made.text()}`).toBe(true);
    reader = await newAuthedRequest(playwright, baseURL ?? '', email, PASSWORD);

    // Out of the box nothing is held: a new file is told at once.
    const settings = await (await reader.get('/api/notifications/settings')).json();
    expect(settings.digest?.urgent_events ?? [], 'a kind is held out of the box').toContain('file.uploaded');

    // This account holds new files for the digest - the Urgent switch off.
    const saved = await reader.patch('/api/notifications/settings', {
      data: { in_app_enabled: true, muted_events: [], urgent_overrides: { 'file.uploaded': false } },
    });
    expect(saved.ok(), `settings: ${saved.status()} ${await saved.text()}`).toBe(true);
    const folder = await reader.post('/api/files/manager?action=newfolder', { data: { path: `${store}://`, name: 'Rapor' } });
    expect(folder.ok(), `newfolder: ${folder.status()} ${await folder.text()}`).toBe(true);
  });

  test.afterAll(async () => {
    await reader?.dispose();
    await dropStorageByName(admin, store).catch(() => undefined);
    await admin.dispose();
  });

  test('thirty uploads in a minute are one notification, folder by folder', async ({ page }) => {
    test.setTimeout(240_000);
    for (let i = 0; i < 30; i++) {
      const res = await reader.post('/api/files/manager?action=upload', {
        multipart: {
          path: `${store}://Rapor`,
          'file[]': { name: `belge-${i}.txt`, mimeType: 'text/plain', buffer: Buffer.from(`belge ${i}\n`) },
        },
      });
      expect(res.ok(), `upload ${i}: ${res.status()} ${await res.text()}`).toBe(true);
    }

    const ours = (list: Row[]) => list.filter((n) => n.event === 'file.uploaded' && n.meta?.target?.storage === store);
    // Every row is there at once - the history is not held - and read.
    await expect.poll(async () => ours(await rows('limit=200')).length, { timeout: 30_000 }).toBe(30);
    expect(ours(await rows('limit=200')).filter((n) => !n.read_at), 'a held upload reads as unread').toEqual([]);
    expect(ours(await rows('unread=true&limit=200')), 'a held upload is in the unread list').toEqual([]);

    // The window ends: one digest, unread, naming the folder and the count.
    let digest: Row | undefined;
    await expect
      .poll(
        async () => {
          digest = (await rows('unread=true&limit=200')).find(
            (n) => n.event === 'notification.digest' && (n.meta?.groups ?? []).some((g) => g.storage === store),
          );
          return !!digest;
        },
        { timeout: 150_000, intervals: [5_000] },
      )
      .toBe(true);
    const group = digest!.meta!.groups!.find((g) => g.storage === store)!;
    expect(group.path).toBe('Rapor');
    expect(group.counts?.['file.uploaded']).toBe(30);
    const digests = (await rows('limit=200')).filter(
      (n) => n.event === 'notification.digest' && (n.meta?.groups ?? []).some((g) => g.storage === store),
    );
    expect(digests.length, 'one window was told more than once').toBe(1);

    // The bell says it in the reader's words.
    await page.addInitScript(() => {
      try {
        localStorage.setItem('filex.tourDone', '1');
      } catch {
        /* storage blocked */
      }
    });
    await loginAs(page, email, PASSWORD);
    await page.goto('/admin/explore');
    await page.getByTestId('notification-bell').click();
    await expect(page.getByTestId('notification-panel')).toBeVisible();
    await expect(
      page.getByTestId('notification-row').filter({ hasText: 'Rapor: 30 files added' }).first(),
    ).toBeVisible();
  });
});
