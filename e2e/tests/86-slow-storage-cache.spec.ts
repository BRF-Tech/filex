/**
 * A big file on a storage the operator marked slow — end to end, against the
 * running server.
 *
 * "fs yavaş ve dosya büyükse kullanıcıya özel bir caching üreteceğimizi
 *  belirtip cache hazır olduğunda indirmeye başlat tarzı bişiler yapmamız
 *  gerekiyor."
 *
 * The sequence under test is the product promise, in order:
 *
 *   1. the first download is answered 202 with a percentage — filex says it is
 *      preparing rather than dribbling at the backend's speed;
 *   2. a poll reports progress and then readiness;
 *   3. the download that follows returns the file, byte for byte (digest), and
 *      a Range request returns exactly its window.
 *
 * ⚠ The assertions are digests and byte counts, never "the endpoint answered".
 * A cache that served plausible-looking bytes would pass a status-code test and
 * corrupt every download — which is the shape of the two green-but-broken tests
 * this project shipped last month.
 *
 * The threshold is normally 64 MiB (FILEX_CACHE_MIN_SIZE); run.mjs lowers it to
 * 1 MiB for the suite so this costs 3 MiB instead of 200.
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { createHash } from 'node:crypto';
import { newAuthedRequest, waitForOp, dropStorageByName } from '../helpers/seed';
import { apiLogin } from '../helpers/auth';

const STORAGE_NAME = `e2e-slow-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE_NAME}`;
const CHUNK = 1024 * 1024;
/** Three chunks — comfortably over the suite's 1 MiB cache threshold. */
const TOTAL = CHUNK * 3;

const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL ?? '');
    await use(ctx);
    await ctx.dispose();
  },
});

function payload(n: number): Buffer {
  const b = Buffer.alloc(n);
  for (let i = 0; i < n; i++) b[i] = (i * 17 + 3) & 0xff;
  return b;
}

function sha256(b: Buffer | Uint8Array): string {
  return createHash('sha256').update(b).digest('hex');
}

/** Uploads through the staged protocol and waits until the bytes are on the driver. */
async function upload(request: APIRequestContext, name: string, data: Buffer) {
  const begunRes = await request.post('/api/files/upload/begin', {
    data: {
      path: `${STORAGE_NAME}://`,
      name,
      size: data.length,
      chunk_size: CHUNK,
      hash: `sha256:${sha256(data)}`,
    },
  });
  expect(begunRes.status(), await begunRes.text()).toBe(200);
  const { id } = await begunRes.json();

  for (let start = 0; start < data.length; start += CHUNK) {
    const len = Math.min(CHUNK, data.length - start);
    const res = await request.put(`/api/files/upload/${id}`, {
      headers: {
        'Content-Range': `bytes ${start}-${start + len - 1}/${data.length}`,
        'Content-Type': 'application/octet-stream',
      },
      data: data.subarray(start, start + len),
    });
    expect(res.status(), await res.text()).toBe(200);
  }

  const commitRes = await request.post(`/api/files/upload/${id}/commit`);
  expect(commitRes.status(), await commitRes.text()).toBe(202);
  const commit = await commitRes.json();
  // The prepared copy is for files whose bytes are on the BACKEND. While a
  // node is staged its bytes are already on filex's own local disk, so it is
  // deliberately never cached — wait for the transfer before asserting.
  expect((await waitForOp(request, commit.op_id)).status).toBe('ok');
}

function downloadURL(name: string, extra = ''): string {
  const path = encodeURIComponent(`${STORAGE_NAME}://${name}`);
  return `/api/files/manager?action=download&path=${path}${extra}`;
}

const asJSON = { Accept: 'application/json' };

test.describe('slow storage: a prepared copy for big files', () => {
  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    // `slow: true` is the operator's own flag, set on the storage config —
    // seedLocalStorage cannot carry it, so the storage is created here.
    const res = await request.post('/api/admin/storages', {
      data: {
        name: STORAGE_NAME,
        driver: 'local',
        mount_path: MOUNT,
        config: { root: MOUNT, path: 'fileman', slow: true },
        sync_mode: 'fsnotify',
        sync_interval_s: 0,
        enabled: true,
        read_only: false,
      },
    });
    expect(res.ok(), await res.text()).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test('202 with a percentage, then the file byte for byte', async ({ authedRequest: request }) => {
    const data = payload(TOTAL);
    const name = 'prepared.bin';
    await upload(request, name, data);

    // 1 — the first download is announced, not served.
    const first = await request.get(downloadURL(name), { headers: asJSON });
    expect(first.status(), await first.text()).toBe(202);
    const announced = await first.json();
    expect(announced.state).toBe('preparing');
    expect(announced.ready).toBe(false);
    expect(typeof announced.percent).toBe('number');
    expect(announced.percent).toBeGreaterThanOrEqual(0);
    // 100 belongs to a file that is actually on disk, so a poller never sees
    // "done" before it can be downloaded.
    expect(announced.percent).toBeLessThanOrEqual(99);
    expect(announced.size).toBe(TOTAL);

    // 2 — the poll reports progress and then readiness. It never serves bytes,
    // so the wait page's fetch().json() cannot choke on a file.
    let ready = false;
    for (let i = 0; i < 200 && !ready; i++) {
      const poll = await request.get(downloadURL(name, '&cache=status'), { headers: asJSON });
      expect(poll.status()).toBe(200);
      const j = await poll.json();
      expect(typeof j.percent).toBe('number');
      ready = j.ready === true;
      if (!ready) await new Promise((r) => setTimeout(r, 50));
    }
    expect(ready, 'the prepared copy never became ready').toBeTruthy();

    // 3 — and now the actual file.
    const served = await request.get(downloadURL(name), { headers: asJSON });
    expect(served.status()).toBe(200);
    const body = await served.body();
    expect(body.length).toBe(TOTAL);
    expect(sha256(body)).toBe(sha256(data));
  });

  test('the prepared copy answers ranges, exactly', async ({ authedRequest: request }) => {
    const data = payload(TOTAL);
    const name = 'ranged.bin';
    await upload(request, name, data);

    // Warm it, then wait.
    await request.get(downloadURL(name), { headers: asJSON });
    let ready = false;
    for (let i = 0; i < 200 && !ready; i++) {
      const poll = await request.get(downloadURL(name, '&cache=status'), { headers: asJSON });
      ready = (await poll.json()).ready === true;
      if (!ready) await new Promise((r) => setTimeout(r, 50));
    }
    expect(ready).toBeTruthy();

    const start = 1_000_000;
    const end = 1_000_999;
    const res = await request.get(downloadURL(name), {
      headers: { ...asJSON, Range: `bytes=${start}-${end}` },
    });
    expect(res.status()).toBe(206);
    expect(res.headers()['content-range']).toBe(`bytes ${start}-${end}/${TOTAL}`);
    const window = await res.body();
    expect(window.length).toBe(1000);
    expect(sha256(window)).toBe(sha256(data.subarray(start, end + 1)));
  });

  test('a browser is shown a preparing page, not JSON', async ({ authedRequest: request }) => {
    const data = payload(TOTAL);
    const name = 'browser.bin';
    await upload(request, name, data);

    const res = await request.get(downloadURL(name), {
      headers: { Accept: 'text/html,application/xhtml+xml', 'Accept-Language': 'en' },
    });
    expect(res.status()).toBe(202);
    expect(res.headers()['content-type']).toContain('text/html');
    const page = await res.text();
    expect(page).toContain('Preparing your download');
    expect(page).toContain('cache=status');
  });

  test('a small file on the same slow storage is served straight away', async ({
    authedRequest: request,
  }) => {
    const data = payload(4096);
    const name = 'small.bin';
    await upload(request, name, data);

    const res = await request.get(downloadURL(name), { headers: asJSON });
    expect(res.status(), 'a small file must never be answered "preparing"').toBe(200);
    expect(sha256(await res.body())).toBe(sha256(data));
  });
});
