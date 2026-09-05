/**
 * Resumable uploads, end to end — the requirement, verbatim (translated from
 * Turkish): "if it is cut off halfway it has to be able to resume."
 *
 * Two halves, because two different things can be broken:
 *
 *   1. The PROTOCOL, against the running server: a large file, the link cut in
 *      the middle of a chunk, a resume from the offset the server reports, and a
 *      checksum on what actually came back down. No client code involved — if
 *      this fails, no client can be correct.
 *   2. The BROWSER, against the real explorer: the same interruption, then a
 *      full page reload, then the same file picked again. The upload must
 *      continue the SAME staged session instead of opening a new one — the
 *      reload is the case a browser cannot survive without a bookmark on disk.
 *
 * ⚠ The assertions are byte counts and digests, never "was the endpoint
 * called". An upload that silently restarts calls exactly the same endpoints as
 * one that resumes; the only thing that tells them apart is how much crossed
 * the wire.
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { dropStorageByName, seedLocalStorage, newAuthedRequest, waitForOp } from '../helpers/seed';
import { loginAs } from '../helpers/auth';

const STORAGE_NAME = `e2e-resume-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE_NAME}`;
const CHUNK = 256 * 1024;
/** Four chunks and a bit — enough to have a middle to be cut in. */
const TOTAL = CHUNK * 4 + 1234;

/**
 * The browser half uses its own, bigger file, and that is not an accident.
 *
 * The explorer stages a file only when it needs more than one chunk — the
 * threshold is `config.chunkSize`, 8 MiB by default on both ends (see
 * docs/UPLOADS.md, "Large means above the chunk size"). Anything smaller takes
 * the single-POST fast path, which never calls `begin`, never writes a bookmark
 * and has nothing to resume. This spec's first draft handed the explorer a 1 MB
 * file and then waited 30 s for a bookmark that, correctly, was never going to
 * be written.
 */
const UI_CHUNK = 8 * 1024 * 1024;
/** Two whole chunks and a tail, so there is a middle to cut in. */
const UI_TOTAL = UI_CHUNK * 2 + 1234;

const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL ?? '');
    await use(ctx);
    await ctx.dispose();
  },
});

/**
 * Deterministic bytes with no short period.
 *
 * ⚠ The obvious `(i * 31 + 7) & 0xff` repeats every 256 bytes, so any run of
 * the file hashes like any other run of the same length — which makes a digest
 * blind to precisely the failure this spec hunts. Measured on this suite: a
 * 16 MB upload with only its first 8 MiB chunk delivered came back down with
 * the expected sha256, because chunk 1 repeated IS the rest of the file. An
 * LCG keeps every offset different from every other.
 */
function payload(n: number): Buffer {
  const b = Buffer.alloc(n);
  let s = 0x9e3779b9;
  for (let i = 0; i < n; i++) {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    b[i] = (s >>> 24) & 0xff;
  }
  return b;
}

function sha256(b: Buffer | Uint8Array): string {
  return createHash('sha256').update(b).digest('hex');
}

test.describe('resumable upload', () => {
  // This spec pushes ~25 MB through the server and then has the ops worker copy
  // 16 MB onto the driver. The suite-wide 10 s action timeout is enough for a
  // click; it is not enough for a login that queues behind that work, and the
  // teardown was failing on exactly that rather than on anything under test.
  test.use({ actionTimeout: 60_000 });

  test.beforeAll(async ({ request }) => {
    await seedLocalStorage(request, STORAGE_NAME, MOUNT);
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test('a cut connection costs one chunk, not the file', async ({ authedRequest: request }) => {
    const data = payload(TOTAL);
    const name = 'resume-protocol.bin';

    // ── begin ──
    const begunRes = await request.post('/api/files/upload/begin', {
      data: {
        path: `${STORAGE_NAME}://`,
        name,
        size: TOTAL,
        chunk_size: CHUNK,
        hash: `sha256:${sha256(data)}`,
      },
    });
    expect(begunRes.status(), await begunRes.text()).toBe(200);
    const begun = await begunRes.json();
    const id: string = begun.id;
    const chunk: number = begun.chunk_size;
    expect(chunk).toBe(CHUNK);
    expect(begun.offset).toBe(0);

    // How many bytes we push, total. This is the number under test.
    let wire = 0;

    async function put(start: number, claim: number, body: Buffer) {
      wire += body.length;
      return request.put(`/api/files/upload/${id}`, {
        headers: {
          'Content-Range': `bytes ${start}-${start + claim - 1}/${TOTAL}`,
          'Content-Type': 'application/octet-stream',
        },
        data: body,
      });
    }

    // ── two whole chunks ──
    for (const n of [0, 1]) {
      const res = await put(n * CHUNK, CHUNK, data.subarray(n * CHUNK, (n + 1) * CHUNK));
      expect(res.status(), await res.text()).toBe(200);
      expect((await res.json()).offset).toBe((n + 1) * CHUNK);
    }

    // ── the link is cut mid-chunk: the header promises a chunk, half arrives ──
    const cut = await put(2 * CHUNK, CHUNK, data.subarray(2 * CHUNK, 2 * CHUNK + 4096));
    expect(cut.status()).toBe(400);

    // ── the client lost its state and asks where to continue ──
    const statusRes = await request.get(`/api/files/upload/${id}`);
    expect(statusRes.ok()).toBeTruthy();
    const status = await statusRes.json();
    expect(status.offset, 'an interrupted chunk must not advance the offset').toBe(2 * CHUNK);
    expect(status.state).toBe('staging');

    // ── resume from exactly there ──
    for (let start = status.offset; start < TOTAL; start += CHUNK) {
      const len = Math.min(CHUNK, TOTAL - start);
      const res = await put(start, len, data.subarray(start, start + len));
      expect(res.status(), await res.text()).toBe(200);
    }

    // ── commit; the server verifies the digest declared before the first chunk ──
    const commitRes = await request.post(`/api/files/upload/${id}/commit`);
    expect(commitRes.status(), await commitRes.text()).toBe(202);
    const commit = await commitRes.json();
    expect(commit.transfer_state).toBe('staged');

    // The node is listed before its bytes have moved — that is the point of
    // staging, and it is a claim worth checking rather than assuming.
    const listing = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE_NAME}://`)}`,
    );
    const names = ((await listing.json()).files ?? []).map((f: { basename: string }) => f.basename);
    expect(names).toContain(name);

    const op = await waitForOp(request, commit.op_id, 60_000);
    expect(op.status).toBe('ok');

    // ── and the file is the file ──
    const dl = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE_NAME}://${name}`)}`,
    );
    expect(dl.ok()).toBeTruthy();
    expect(sha256(await dl.body())).toBe(sha256(data));

    // The measurement: everything once, plus the interrupted chunk's stub.
    expect(wire, 'a resume must not re-send the file').toBeLessThan(TOTAL * 2);
    expect(wire).toBe(TOTAL + 4096);
  });

  test('the browser continues the same session across a page reload', async ({
    page,
    authedRequest: request,
  }) => {
    // ~25 MB pushed through a request interceptor and 16 MB read back.
    test.setTimeout(240_000);

    const data = payload(UI_TOTAL);
    const name = 'resume-browser.bin';

    // ⚠ The file is handed over from DISK, not as a buffer.
    //
    // `setInputFiles({buffer})` builds the File in the page, and the platform
    // stamps `lastModified = Date.now()` on it — so the same bytes picked twice
    // are two DIFFERENT files as far as the bookmark is concerned, whose
    // fingerprint is (destination, name, size, lastModified). Measured on this
    // suite: two buffer picks 1.5 s apart came back 1524 ms apart. A file on
    // disk carries its mtime and picks identically every time, which is also
    // what a user re-picking a file actually does.
    //
    // That mtime is not incidental strictness: splicing a new tail onto an old
    // head is the one way a resumable upload corrupts data, so a file whose
    // identity moved must NOT inherit the session.
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-resume-'));
    const filePath = path.join(dir, name);
    fs.writeFileSync(filePath, data);

    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE_NAME)}`);
    await page.waitForLoadState('networkidle');

    // Watch the protocol rather than the DOM: what we care about is whether a
    // SECOND staged session gets opened and how many bytes are pushed, and both
    // are network facts.
    //
    // ⚠ `cutting` is a flag rather than a counter because the client retries a
    // failed chunk a few times before giving up. Every attempt at that offset
    // has to die for the run to fail; the flag is cleared before the reload so
    // the resume is allowed through.
    const begins: string[] = [];
    /** [start, end) of every chunk actually let through to the server. */
    const sent: Array<[number, number]> = [];
    let killed = 0;
    let cutting = true;
    /** localStorage as it stood when the first chunk was on the wire but not
     *  yet through — the window a flaky link dies in. */
    let bookmarkAtFirstChunk: string | null | undefined;

    await page.route('**/api/files/upload/**', async (route) => {
      const req = route.request();
      if (req.method() === 'POST' && req.url().endsWith('/begin')) {
        begins.push(req.url());
      }
      if (req.method() === 'PUT') {
        const m = /^bytes (\d+)-(\d+)\//.exec(req.headers()['content-range'] ?? '');
        const start = m ? Number(m[1]) : -1;
        const end = m ? Number(m[2]) + 1 : -1;
        if (start === 0 && bookmarkAtFirstChunk === undefined) {
          // The request is held here, so this reads the bookmark at the exact
          // moment the first chunk is in flight and nothing has landed yet.
          bookmarkAtFirstChunk = await page.evaluate(() =>
            localStorage.getItem('filex:uploads:v1'),
          );
        }
        if (cutting && start === UI_CHUNK) {
          killed += 1;
          await route.abort('connectionreset');
          return;
        }
        if (start >= 0) sent.push([start, end]);
      }
      await route.continue();
    });

    /** Bytes the browser has pushed at the staged endpoint, total. */
    const wire = () => sent.reduce((n, [a, b]) => n + (b - a), 0);

    /**
     * Record the explorer's toasts as they appear. They dismiss themselves
     * after 2.5 s, so polling for one is a race; this reads the text off the
     * screen the moment it is rendered and keeps it. Re-armed after a reload,
     * which wipes it along with everything else in the page.
     */
    const recordToasts = () =>
      page.evaluate(() => {
        const seen: string[] = [];
        (window as unknown as { __fxToasts: string[] }).__fxToasts = seen;
        const scrape = () => {
          for (const el of document.querySelectorAll('.fe-toast__msg')) {
            const txt = el.textContent?.trim();
            if (txt && !seen.includes(txt)) seen.push(txt);
          }
        };
        new MutationObserver(scrape).observe(document.body, {
          subtree: true,
          childList: true,
          characterData: true,
        });
        scrape();
      });
    const toasts = () =>
      page
        .evaluate(() => (window as unknown as { __fxToasts: string[] }).__fxToasts ?? [])
        .then((list) => list.join(' | '));

    await recordToasts();

    // Hand the explorer the file. The picker input is hidden behind the toolbar
    // button, so the input is driven directly — the same File object the button
    // would produce.
    const setFile = async () => {
      const input = page.locator('input[type="file"]').first();
      await input.setInputFiles(filePath);
    };

    await setFile();

    // The first attempt must fail (the link was cut) but leave the session
    // behind — the bookmark is in localStorage, the bytes are on the server.
    // It is followed until it reaches the first chunk boundary: the bookmark
    // appears at offset 0 before any byte moves (that is the point of it), so
    // "it exists" is not yet evidence that a chunk landed.
    const readBookmark = () =>
      page.evaluate(() => localStorage.getItem('filex:uploads:v1'));
    await expect
      .poll(
        async () => {
          const raw = await readBookmark();
          const rec = Object.values(JSON.parse(raw ?? '{}'))[0] as { offset?: number } | undefined;
          return rec?.offset ?? -1;
        },
        { timeout: 120_000 },
      )
      .toBe(UI_CHUNK);

    const bookmark = JSON.parse((await readBookmark()) ?? '{}');
    const record = Object.values(bookmark)[0] as { uploadId: string; offset: number };
    expect(record?.uploadId).toBeTruthy();

    // The client retries a cut chunk a few times before giving up, and it says
    // so on screen when it does — en: `Could not upload “…”` · tr: `“…”
    // yüklenemedi`. Waiting for that (rather than for a fixed number of
    // retries) means the reload cannot race an attempt still in flight, and it
    // does not encode the client's retry count into the test.
    await expect.poll(toasts, { timeout: 60_000 }).toMatch(/resume-browser\.bin/);
    expect(killed, 'the second chunk had to die for this to be a resume').toBeGreaterThan(0);

    // ⚠ The bookmark has to exist BEFORE the first chunk, not after it: a tab
    // that dies between `begin` and the first PUT would otherwise leave a
    // staging session nobody can name, and the next visit re-uploads the file.
    expect(
      bookmarkAtFirstChunk,
      'the bookmark must be written before the first chunk is sent',
    ).toBeTruthy();
    expect(JSON.parse(bookmarkAtFirstChunk ?? '{}')).toEqual({
      [Object.keys(bookmark)[0]]: expect.objectContaining({
        uploadId: record.uploadId,
        offset: 0,
      }),
    });

    // ── reload: everything in memory is gone; only the bookmark survives ──
    cutting = false;
    await page.reload();
    await page.waitForLoadState('networkidle');

    await recordToasts();

    const beginsBeforeResume = begins.length;
    const sentBeforeResume = sent.length;
    expect(wire(), 'one whole chunk landed before the cut').toBe(UI_CHUNK);

    await setFile();

    // The explorer says on screen that it is continuing, rather than resuming
    // in silence — an upload that quietly starts over looks identical to one
    // that never happened, which is the complaint this feature answers.
    // en: `Resuming “resume-browser.bin” from 50%` · tr: `“resume-browser.bin”
    // %50 noktasından devam ediyor`. Both name the file and the offset.
    await expect.poll(toasts, { timeout: 30_000 }).toMatch(/resume-browser\.bin[^|]*50/);

    // ── the measurement: one session, and only the missing tail on the wire ──
    //
    // An upload that silently restarts calls exactly these endpoints, in
    // exactly this order, and ends with exactly the same file on the server —
    // it even shows the same "resuming" toast, because the bookmark it ignores
    // is still on disk. What tells the two apart is how many bytes moved.
    //
    // ⚠ These are polled TOGETHER rather than asserted one after another. The
    // end of the upload has to be waited for (the bookmark is cleared at
    // commit and nowhere else, so its disappearance is the signal — the name
    // appears in the listing at `begin`, and the digest can match while the
    // last chunk is still in flight), and reading the byte counts before that
    // moment measures an upload in progress. Polling the whole tuple waits for
    // the end state and reports what was wrong with it.
    await expect
      .poll(
        async () => ({
          bookmark: await readBookmark(),
          begins: begins.length,
          wire: wire(),
        }),
        { timeout: 120_000 },
      )
      .toEqual({ bookmark: '{}', begins: beginsBeforeResume, wire: UI_TOTAL });

    const afterReload = sent.slice(sentBeforeResume);
    expect(afterReload, 'the resume must send the missing tail and nothing else').toEqual([
      [UI_CHUNK, UI_CHUNK * 2],
      [UI_CHUNK * 2, UI_TOTAL],
    ]);
    expect(wire(), 'a resume must not re-send the file').toBeLessThan(UI_TOTAL * 2);

    // ── and the file is the file ──
    await expect
      .poll(
        async () => {
          const dl = await request.get(
            `/api/files/manager?action=download&path=${encodeURIComponent(
              `${STORAGE_NAME}://${name}`,
            )}`,
          );
          if (!dl.ok()) return `HTTP ${dl.status()}`;
          return sha256(await dl.body());
        },
        { timeout: 120_000 },
      )
      .toBe(sha256(data));

    fs.rmSync(dir, { recursive: true, force: true });
  });
});
