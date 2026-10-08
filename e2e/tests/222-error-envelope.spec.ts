/**
 * 222 - one shape for every refusal, and the server writes its sentence
 * (0.54 #209, docs/API-ERRORS.md).
 *
 * A refusal answers {"error": <code>, "message": <the server's sentence in
 * the reader's language>}; every client shows `message`. And a form asks the
 * server, while it is typed, whether an address or a username would be
 * accepted (POST /api/auth/account/check) instead of judging with a copy of
 * the rules.
 *
 * RED on int/054-wave d4107407: a rename on a read-only storage answered
 * 403 {"error":"storage is read-only"} - an English sentence where the code
 * goes, no `message` - and /api/auth/account/check did not exist (404).
 *
 * Measured on the wire, not on a screen: the words are the server's
 * catalogue (backend/internal/srvtext/locales), pinned by the Go tests; the
 * explorer's half (it shows `message`) is web/tests/lib/serverMessage.test.ts.
 */
import { test, expect } from '@playwright/test';

import { apiLogin } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';

const RO = 'e2e-222-ro';

// The admin account's language decides first, then Accept-Language: either
// shipped language's sentence is the server's.
const READ_ONLY_SAID = ['This storage is read-only.', 'Bu depo salt okunur.'];

test.describe('222 - the error envelope', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, RO);
    await seedLocalStorage(request, RO, '/tmp/filex-e2e-222-ro', { read_only: true });
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, RO);
  });

  test('a read-only storage answers a code and the server sentence', async ({ request }) => {
    await apiLogin(request);
    const res = await request.post('/api/files/manager?action=rename', {
      headers: { 'Accept-Language': 'tr' },
      data: { path: `${RO}://`, item: `${RO}://nothing-here.txt`, name: 'renamed.txt' },
    });
    expect(res.status()).toBe(403);
    const body = (await res.json()) as { error?: unknown; message?: unknown };
    expect(body.error, 'the code, not an English sentence').toBe('read_only');
    expect(READ_ONLY_SAID).toContain(body.message);
  });

  test('the account check says what the save would refuse, in its words', async ({ request }) => {
    await apiLogin(request);
    const res = await request.post('/api/auth/account/check', {
      data: { email: 'a,b@x', username: '9lives', for: 'new' },
    });
    expect(res.status()).toBe(200);
    const body = (await res.json()) as Record<string, { error?: string; message?: string } | undefined>;
    expect(body.email?.error).toBe('email_invalid');
    expect(body.email?.message ?? '').toContain('a,b@x');
    expect(body.username?.error).toBe('username_invalid');
    expect((body.username?.message ?? '').length).toBeGreaterThan(0);

    // An acceptable address and name: nothing to say.
    const ok = await request.post('/api/auth/account/check', {
      data: { email: 'someone.222@example.com', username: 'someone222', for: 'new' },
    });
    expect(ok.status()).toBe(200);
    expect(await ok.json()).toEqual({});
  });
});
