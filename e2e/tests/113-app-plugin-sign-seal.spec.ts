/**
 * 113-app-plugin-sign-seal — certification, the platform seal, the hash to
 * everyone and the lock, measured end to end in a browser.
 *
 * ⚠⚠ The owner, 2026-09-22: "after a document is fully signed and someone
 * changes it, does the signature say so — or do we lock the file? Both, plus
 * a seal." This spec walks ONE request — an inside signer and an outside one,
 * "lock the signed file" ticked — to completion, and then checks from the
 * outside what was promised:
 *
 *   1. every party is told the SHA-256 of the signed file: the outside signer
 *      by mail (captured by an SMTP sink this spec runs, with the delivery
 *      link), the requester and the inside signer in filex;
 *   2. the file the delivery link hands out hashes to exactly that value —
 *      `sha256sum` of the delivered bytes, not of some copy of them;
 *   3. the signed file is locked: a rename is refused (423, by `sign`);
 *   4. Verify, in the browser, says every signature is valid, certified,
 *      sealed by filex, and that this is the file whose hash was sent;
 *   5. a copy EDITED after completion — an incremental update that redraws
 *      page 1, the way any PDF tool appends one — is reported by Verify as a
 *      change the certification and filex's seal do NOT permit.
 *
 * With E2E_SHOTS_DIR set, the delivered and the edited PDF are written there
 * too, so an outside validator (pyHanko) can read the very same bytes.
 *
 * The module is the real `filex-sign` build; without it the spec skips,
 * unless FILEX_REQUIRE_WASM_FIXTURE=1.
 */
import { createHash } from 'node:crypto';
import { writeFileSync } from 'node:fs';
import net from 'node:net';
import { test, expect, request as pwRequest, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { guardFixture, installThroughWizard, minimalPDF, resolveApp } from '../helpers/appPlugin';
import { removeApp } from '../helpers/surface';
import { advance, drawSignature, next, press, SEND, SIGN } from '../helpers/wizard';

const APP = resolveApp('sign');
const STORAGE = `e2e-seal-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DOC = 'sealed.pdf';
const EDITED = 'sealed-edited.pdf';
const QUALIFIED = `${STORAGE}://${DOC}`;
const SIGNER = { email: 'seal-signer@example.test', password: 'seal-signer-pw-2026', name: 'Selin Mühür' };
const OUTSIDE = 'dis-muhur@example.test';
const SHOTS = process.env.E2E_SHOTS_DIR;

// ── an SMTP sink: every mail filex sends, captured whole ─────────────────
//
// ⚠ Plain SMTP, no TLS, no AUTH: filex's mailer with `smtp.tls = none` and
// no username says EHLO, NOOP (the verify), MAIL, RCPT, DATA, QUIT — and the
// body is plain UTF-8 text with no transfer encoding (mailer.buildMessage),
// so what the sink stores is what the recipient reads.

interface Mail {
  to: string[];
  data: string;
}
const mails: Mail[] = [];
let smtp: net.Server | undefined;

function startSmtp(): Promise<number> {
  const server = net.createServer((sock) => {
    sock.setEncoding('utf8');
    let rcpt: string[] = [];
    let inData = false;
    let buf = '';
    let data = '';
    sock.write('220 e2e-sink ESMTP\r\n');
    sock.on('data', (chunk: string) => {
      buf += chunk;
      let i: number;
      while ((i = buf.indexOf('\r\n')) >= 0) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 2);
        if (inData) {
          if (line === '.') {
            inData = false;
            mails.push({ to: rcpt, data });
            rcpt = [];
            data = '';
            sock.write('250 queued\r\n');
          } else {
            data += (line.startsWith('..') ? line.slice(1) : line) + '\n';
          }
          continue;
        }
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === 'EHLO' || verb === 'HELO') sock.write('250 e2e-sink\r\n');
        else if (verb === 'RCPT') {
          rcpt.push(line.replace(/^RCPT TO:\s*<?([^>]*)>?.*$/i, '$1'));
          sock.write('250 ok\r\n');
        } else if (verb === 'DATA') {
          inData = true;
          sock.write('354 go ahead\r\n');
        } else if (verb === 'QUIT') {
          sock.write('221 bye\r\n');
          sock.end();
        } else sock.write('250 ok\r\n');
      }
    });
    sock.on('error', () => {});
  });
  smtp = server;
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve((server.address() as net.AddressInfo).port)));
}

/** The completion mail to the outside signer — the one that carries the hash. */
const completionMails = () =>
  mails.filter((m) => m.to.includes(OUTSIDE) && /SHA-256 of the signed file|İmzalı dosyanın SHA-256/.test(m.data));

// The wizard walk lives in ONE place, e2e/helpers/wizard.ts. ⚠ This spec's
// own `next()` was `.last().click()` with no wait at all, and it was used for
// Send and Sign as well as for moving on — so every press was one late
// round trip away from acting on the wrong step. 130 failed that way on the
// v0.43.0 release run. Every press below is classified: an ADVANCE goes
// through next/advance (which never act); an ACT goes through press(), by
// name. Back-to-back advances use advance(), which proves each step changed.

const sha256 = (b: Buffer) => createHash('sha256').update(b).digest('hex');

/** How many signatures the document carries now, read as the requester. */
async function signatures(request: APIRequestContext): Promise<number> {
  const res = await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(QUALIFIED)}`);
  if (!res.ok()) return -1;
  return (Buffer.from(await res.body()).toString('latin1').match(/\/Type \/Sig \/Filter/g) ?? []).length;
}


/**
 * An edit after completion, the way any PDF tool appends one: the page's
 * content stream is written again (it now also draws "PAID IN FULL") in a
 * new incremental update. The signed bytes are untouched — every signature
 * still checks out — what changed is what the page shows, which none of
 * this document's signatures permits.
 */
function editAfterCompletion(pdf: Buffer): Buffer {
  const s = pdf.toString('latin1');
  const stream = /(\d+) 0 obj\s*<<\s*\/Length (\d+)\s*>>\s*stream\r?\n([\s\S]*?)\r?\nendstream/.exec(s);
  if (!stream) throw new Error('no content stream to edit');
  const id = Number(stream[1]);
  const content = `${stream[3]}\nBT /F1 28 Tf 72 400 Td (PAID IN FULL) Tj ET`;
  const root = [...s.matchAll(/\/Root (\d+ \d+ R)/g)].pop()![1];
  const size = Math.max(...[...s.matchAll(/\/Size (\d+)/g)].map((m) => Number(m[1])));
  const prev = Number([...s.matchAll(/startxref\s+(\d+)/g)].pop()![1]);
  let out = s.endsWith('\n') ? s : `${s}\n`;
  const at = Buffer.byteLength(out, 'latin1');
  out += `${id} 0 obj\n<< /Length ${Buffer.byteLength(content, 'latin1')} >>\nstream\n${content}\nendstream\nendobj\n`;
  const xref = Buffer.byteLength(out, 'latin1');
  out += `xref\n${id} 1\n${String(at).padStart(10, '0')} 00000 n \ntrailer\n<< /Size ${size} /Root ${root} /Prev ${prev} >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, 'latin1');
}

test.describe('App plugin: sign — certified, sealed, hashed to everyone, locked', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  let sentHash = '';
  let signed: Buffer = Buffer.alloc(0);

  test.beforeAll(async ({ request }) => {
    const port = await startSmtp();
    await apiLogin(request);
    const set = await request.patch('/api/admin/settings', {
      data: { 'smtp.host': '127.0.0.1', 'smtp.port': String(port), 'smtp.tls': 'none', 'smtp.from': 'filex@e2e.test', 'smtp.username': '' },
    });
    expect(set.ok(), `smtp settings: ${set.status()} ${await set.text()}`).toBe(true);
    const verified = (await (await request.post('/api/admin/settings/smtp-test', { data: {} })).json()) as { ok: boolean; error?: string };
    expect(verified.ok, `the SMTP sink is verified: ${verified.error}`).toBe(true);

    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT, { rbac_enabled: true });
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: DOC, mimeType: 'application/pdf', buffer: minimalPDF('sealed contract') } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBe(true);
    await request.post('/api/admin/users', {
      data: { email: SIGNER.email, password: SIGNER.password, display_name: SIGNER.name, role: 'user' },
    });
    const raw = (await (await request.get('/api/admin/users')).json()) as
      | { id: number; email: string }[]
      | { users?: { id: number; email: string }[]; items?: { id: number; email: string }[] };
    const list = Array.isArray(raw) ? raw : (raw.users ?? raw.items ?? []);
    const me = list.find((u) => u.email === SIGNER.email);
    expect(me, 'the inside signer exists').toBeTruthy();
    const g = await request.post('/api/files/permissions', {
      data: { path: `${STORAGE}://`, user_id: me!.id, level: 'editor', is_dir: true },
    });
    expect(g.ok(), `grant: ${g.status()} ${await g.text()}`).toBe(true);
    await removeApp(request, 'sign');
  });

  test.afterAll(async ({ request }) => {
    smtp?.close();
    await apiLogin(request);
    await removeApp(request, 'sign');
    // ⚠ Leave no mail server behind: the next spec must meet the instance
    // the way it was, SMTP unconfigured and unverified.
    await request.patch('/api/admin/settings', { data: { 'smtp.host': '', 'smtp.port': '', 'smtp.from': '' } });
    await request.post('/api/admin/settings/smtp-test', { data: {} });
    await dropStorageByName(request, STORAGE);
  });

  test('install', async ({ page }) => {
    test.setTimeout(120_000);
    await loginAs(page);
    await installThroughWizard(page, APP);
  });

  test('the request: an inside and an outside signer, the signed file locked when done', async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1280, height: 900 });
    await loginAs(page);
    await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
    await page.getByTestId('surface-people-input').fill('seal-signer');
    await page.getByTestId('surface-people-suggest').locator('li,button').first().click();
    await page.locator('textarea').first().fill(`Dış Kişi <${OUTSIDE}>`);
    await next(page);
    await expect(page.getByText(/In what order|Hangi sırayla/)).toBeVisible();
    await next(page);
    await page.getByTestId('surface-pdf-add-signature').waitFor();
    for (let i = 0; i < 2; i++) await page.getByTestId('surface-pdf-add-signature').click();
    const cards = page.locator('[data-testid^="surface-pdf-card-"][data-testid$="-editor"]');
    await expect(cards).toHaveCount(2);
    await cards.nth(0).locator('[data-testid$="-assignee-s1"]').click();
    await cards.nth(1).locator('[data-testid$="-assignee-s2"]').click();
    await next(page);
    await expect(page.locator('.fe-spdf__page canvas').first()).toBeVisible({ timeout: 30_000 });
    for (const [fx, fy] of [[0.3, 0.75], [0.7, 0.75]]) {
      await page.locator('[data-testid^="surface-pdf-pending-"]').first().click();
      const b = (await page.locator('.fe-spdf__page').first().boundingBox())!;
      await page.mouse.click(b.x + b.width * fx, b.y + b.height * fy);
    }
    await expect(page.getByTestId('surface-pdf-all-placed')).toBeVisible();
    await page.waitForTimeout(1000);
    await advance(page); // → time
    await advance(page); // → while it is open
    // The option, and its words: what "yes" and what "no" each mean.
    const option = page.getByText(/^(Lock the signed file when every signature is in|Her imza gelince imzalı dosyayı kilitle)$/);
    await expect(option).toBeVisible();
    await expect(page.getByText(/Yes: once the last signature|Evet: son imza/)).toBeVisible();
    await expect(page.getByText(/No: afterwards the signed file is an ordinary file|Hayır: bundan sonra imzalı dosya sıradan/)).toBeVisible();
    await option.click();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/seal-1-option.png`, fullPage: true });
    await page.waitForTimeout(500);
    await advance(page); // → when it is done
    await advance(page); // → review
    await expect(page.getByText(/locked for good when every signature is in|her imza gelince imzalı dosya kalıcı olarak kilitlenir/)).toBeVisible();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/seal-2-review.png`, fullPage: true });
    await press(page, SEND);
    await expect(page.getByTestId('plugin-page-queued')).toBeVisible({ timeout: 20_000 });
  });

  test('the inside signer signs — the first signature, which certifies', async ({ page, request }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1366, height: 900 });
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(SIGNER.email);
    await page.getByLabel(/password|parola/i).fill(SIGNER.password);
    await page
      .getByRole('button', { name: 'Sign in', exact: true })
      .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
      .first()
      .click();
    await page.waitForURL(/\/drive\//);
    await page.goto(`/drive/apps/sign/sign-fill?path=${encodeURIComponent(QUALIFIED)}`);
    await next(page);
    await drawSignature(page);
    await next(page);
    await expect(page.getByText(/Printed under “|altına yazılacaklar:/).first()).toBeVisible({ timeout: 20_000 });
    await press(page, SIGN);
    await expect(page.getByTestId('plugin-page-queued')).toBeVisible({ timeout: 20_000 });
    await apiLogin(request);
    await expect.poll(() => signatures(request), { timeout: 60_000, message: 'the first signature lands' }).toBe(1);
  });

  test('the outside signer signs on their link — the request completes and filex seals', async ({ page, request }) => {
    test.setTimeout(180_000);
    await apiLogin(request);
    const mine = (await (await request.get('/api/shares')).json()) as {
      items?: { share?: { id: number; token: string; page_id?: string }; node_path?: string }[];
    };
    const link = (mine.items ?? []).find((r) => r.share?.page_id === 'signer' && (r.node_path ?? '').endsWith(DOC));
    expect(link?.share, 'the request opened a signing link').toBeTruthy();
    const { pin } = (await (await request.get(`/api/shares/${link!.share!.id}/pin`)).json()) as { pin: string };
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/s/${link!.share!.token}`);
    await page.getByTestId('public-page-pin-input').fill(pin);
    await page.getByTestId('public-page-pin-submit').click();
    await page.getByTestId('public-page-action-next').click();
    await drawSignature(page);
    await page.getByTestId('public-page-action-next').click();
    await expect(page.locator('.fe-spdf__page canvas').first()).toBeVisible({ timeout: 30_000 });
    await page.getByTestId('public-page-action-sign').click();
    // Two signatures and filex's seal.
    await expect
      .poll(() => signatures(request), { timeout: 90_000, message: 'the second signature and the seal land' })
      .toBe(3);
    await expect
      .poll(() => completionMails().length, { timeout: 60_000, message: 'the outside signer is mailed the hash' })
      .toBeGreaterThan(0);
  });

  test('the hash in the mail is the SHA-256 of the file the delivery link hands out', async ({ request, baseURL }) => {
    const mail = completionMails().pop()!;
    const hash = /(?:SHA-256 of the signed file|İmzalı dosyanın SHA-256 özeti)[^\n]*:\n([0-9a-f]{64})\n/.exec(mail.data);
    expect(hash, `the mail carries the file's SHA-256:\n${mail.data}`).toBeTruthy();
    sentHash = hash![1];
    // The seal's fingerprint is written the way filex writes every
    // certificate fingerprint: upper-case, in groups of four.
    expect(mail.data, 'the seal is named').toMatch(
      /(?:Fingerprint of the seal's certificate|Mühür sertifikasının parmak izi)[^\n]*:\n[0-9A-F]{4}(?: [0-9A-F]{4}){15}\n/,
    );
    expect(mail.data, 'and how to check it without filex').toMatch(/sha256sum/);
    expect(mail.data).toMatch(/Get-FileHash/);
    expect(mail.data, 'and that it is locked').toMatch(/locked for good in filex|kalıcı olarak kilitlendi/);
    const url = /\/s\/([A-Za-z0-9_-]+)/.exec(mail.data);
    expect(url, 'the mail carries the delivery link').toBeTruthy();

    // The delivery link's PIN is the requester's to pass on.
    await apiLogin(request);
    const mine = (await (await request.get('/api/shares')).json()) as { items?: { share?: { id: number; token: string } }[] };
    const delivery = (mine.items ?? []).find((r) => r.share?.token === url![1]);
    expect(delivery, 'the delivery link is one of the requester’s shares').toBeTruthy();
    const { pin } = (await (await request.get(`/api/shares/${delivery!.share!.id}/pin`)).json()) as { pin: string };
    // As a stranger fetches it: no session, the PIN, the confirmed download.
    const anon = await pwRequest.newContext({ baseURL });
    const res = await anon.post(`/s/${url![1]}?confirmed=1`, { form: { pin }, maxRedirects: 0 });
    expect(res.status(), 'the delivery link hands out the file').toBe(200);
    signed = Buffer.from(await res.body());
    await anon.dispose();
    expect(signed.subarray(0, 5).toString('latin1')).toBe('%PDF-');
    if (SHOTS) writeFileSync(`${SHOTS}/seal-delivered.pdf`, signed);
    expect(sha256(signed), 'sha256sum of the delivered file is the hash that was mailed').toBe(sentHash);

    // The same bytes are the file in filex (a new version of the document).
    const raw = await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(QUALIFIED)}`);
    expect(sha256(Buffer.from(await raw.body())), 'the file in filex is the delivered file').toBe(sentHash);
  });

  test('the requester and the inside signer are told the same hash in filex', async ({ request, baseURL }) => {
    await apiLogin(request);
    const requester = JSON.stringify(await (await request.get('/api/notifications')).json());
    expect(requester, 'the requester’s notice carries the hash').toContain(sentHash);
    const inside = await pwRequest.newContext({ baseURL });
    await apiLogin(inside, SIGNER.email, SIGNER.password);
    const theirs = JSON.stringify(await (await inside.get('/api/notifications')).json());
    await inside.dispose();
    expect(theirs, 'the inside signer’s notice carries the hash').toContain(sentHash);
  });

  test('the signed file is locked for good', async ({ request }) => {
    await apiLogin(request);
    const res = await request.post('/api/files/manager?action=rename', {
      data: { path: `${STORAGE}://`, item: QUALIFIED, name: 'renamed.pdf' },
    });
    expect(res.status(), `a rename of the locked signed file: ${await res.text()}`).toBe(423);
    const refusal = (await res.json()) as { error?: string; plugin?: string };
    expect(refusal.error).toBe('locked');
    expect(refusal.plugin).toBe('sign');
  });

  test('Verify: every signature valid, certified, sealed, and the hash the one sent', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 1000 });
    await loginAs(page);
    await page.goto(`/admin/apps/sign/verify?path=${encodeURIComponent(QUALIFIED)}`);
    const body = page.locator('body');
    await expect(body).toContainText(/Every signature is valid|Her imza geçerli/, { timeout: 30_000 });
    await expect(body).toContainText(
      /Certified by .*filling in the form and signing are permitted|tarafından onaylandı: .*formu doldurmaya ve imzalamaya izin veriliyor/,
    );
    await expect(body).toContainText(/Sealed by filex|filex tarafından mühürlendi/);
    await expect(body).toContainText(sentHash);
    await expect(body).toContainText(
      /This is exactly the file whose SHA-256 every party was sent|SHA-256 özeti her tarafa gönderilen dosyanın ta kendisi/,
    );
    await expect(body).not.toContainText(/do NOT permit|İZİN VERMEDİĞİ/);
    // ⚠ The seal is named what its certificate says — on the first run it
    // typed "filex", and Verify warned about our own seal.
    await expect(body).not.toContainText(/the certificate says otherwise|sertifika başka söylüyor/);
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/seal-3-verify.png`, fullPage: true });
  });

  test('an edit after completion is reported as NOT permitted', async ({ page, request }) => {
    const edited = editAfterCompletion(signed);
    expect(sha256(edited)).not.toBe(sentHash);
    if (SHOTS) writeFileSync(`${SHOTS}/seal-edited.pdf`, edited);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: EDITED, mimeType: 'application/pdf', buffer: edited } },
    });
    expect(up.ok(), `upload the edited copy: ${up.status()}`).toBe(true);
    await page.setViewportSize({ width: 1280, height: 1000 });
    await loginAs(page);
    await page.goto(`/admin/apps/sign/verify?path=${encodeURIComponent(`${STORAGE}://${EDITED}`)}`);
    const body = page.locator('body');
    await expect(body).toContainText(/do NOT permit|İZİN VERMEDİĞİ/, { timeout: 30_000 });
    await expect(body).toContainText(
      /Not permitted by filex's seal: page 1 draws something different|filex'in mührü buna izin vermiyor: 1\. sayfa başka bir şey çiziyor/,
    );
    await expect(body).toContainText(
      /Not permitted by the certification: page 1 draws something different|onay imzası buna izin vermiyor: 1\. sayfa başka bir şey çiziyor/,
    );
    // ⚠⚠ The signatures themselves are intact — the edit touched no signed
    // byte — and the report says so, instead of "does not match the bytes it
    // covers" (what it said on the first run of this spec, 2026-09-22:
    // pdfsign's own DocMDP check gave up before checking the signature).
    await expect(body).toContainText(/Every signature itself is intact|Her imzanın kendisi bozulmamış/);
    await expect(body).not.toContainText(/does not match the bytes it covers|Every signature is valid|DocMDP validation failed/);
    // A copy that is not the sent file does not get the sent file's blessing.
    await expect(body).not.toContainText(/This is exactly the file whose SHA-256|SHA-256 özeti her tarafa gönderilen dosyanın ta kendisi/);
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/seal-4-edited.png`, fullPage: true });
  });
});
