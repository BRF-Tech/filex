/**
 * 112-language-pack-server-text — what the SERVER writes, in a pack's language.
 *
 * 114 walks a pack through every screen. This walks it through the text the
 * server composes, which knew English and Turkish only until v0.43.0: with a
 * Spanish pack installed and a Spanish account, a share-link e-mail arrived
 * as "informe.txt dosyası sizinle paylaşıldı" — TURKISH, the `else` of an
 * en/tr pair — the drop notice to the owner too, the bell said "1 file
 * received", and a Spanish browser's drop page said "Send files" (measured
 * 2026-09-22 before the server catalogue).
 *
 * The mails are read off a real SMTP conversation: this file runs a tiny SMTP
 * server, points filex at it, and restores the settings afterwards.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { mailToSink, startSink, waitMail, type Sink } from '../helpers/smtpSink';

const STORAGE = `e2e-srvtext-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PACK_NAME = 'lang-es-srvtext-e2e';

/** A PARTIAL pack on purpose: the keys it lacks must arrive in English. */
const ES: Record<string, string> = {
  'nav.notifications': 'Notificaciones',
  'server.mail.greeting': 'Hola:',
  'server.mail.share.subject_file': '{name} se ha compartido contigo',
  'server.mail.label.file': 'Archivo: {name}',
  'server.mail.share.download': 'Descárgalo aquí:',
  'server.mail.valid_days': 'Este enlace es válido durante {count} días.',
  'server.notify.word.someone': 'Alguien',
  'server.notify.drop.received.title': '{count} archivos recibidos',
  'server.notify.drop.received.title_one': 'Un archivo recibido',
  'server.public.drop_title': 'Enviar archivos',
  'server.public.drop_heading': 'Enviar archivos',
  'server.public.footer': 'Compartido con {filex}',
};

const PREFS = '/api/me/prefs?surface=web';

test.describe.serial('Language pack — the text the server writes', () => {
  let api: APIRequestContext;
  let sink: Sink;
  let restoreMail: () => Promise<void> = async () => undefined;
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    sink = await startSink();
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const prefs = await api.get(PREFS);
    prefsBefore = prefs.ok() ? ((await prefs.json()).prefs ?? {}) : {};

    restoreMail = await mailToSink(api, sink);

    const list = await (await api.get('/api/admin/app-plugins')).json();
    for (const p of list.plugins ?? []) if (p.name === PACK_NAME) await api.delete(`/api/admin/app-plugins/${p.id}`);
    const manifest = {
      manifest_version: 1, name: PACK_NAME, version: '1.0.0', label: { en: 'Spanish (server text e2e)' },
      permissions: [], ui_locales: { es: ES },
    };
    const inst = await api.post('/api/admin/app-plugins', {
      multipart: { manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(manifest)) } },
    });
    expect(inst.ok(), await inst.text()).toBeTruthy();

    expect((await api.patch('/api/auth/profile', { data: { locale: 'es' } })).ok()).toBeTruthy();
    await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'es' } } });

    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: 'informe.txt', mimeType: 'text/plain', buffer: Buffer.from('hola') } },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: 'buzon' } });
    expect(mk.ok(), await mk.text()).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    const list = await (await api.get('/api/admin/app-plugins')).json();
    for (const p of list.plugins ?? []) if (p.name === PACK_NAME) await api.delete(`/api/admin/app-plugins/${p.id}`);
    await restoreMail();
    await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api.patch('/api/auth/profile', { data: { locale: 'en' } }).catch(() => undefined);
    await dropStorageByName(request, STORAGE);
    await api.dispose();
    await sink.close();
  });

  test('a share-link mail arrives in the pack language, English where the pack is silent', async () => {
    // Seven days, so the mail has a validity line to say in Spanish: the
    // server writes it from the link (share_mail.go), not from the request.
    const made = await api.post('/api/files/share', {
      data: { path: `${STORAGE}://informe.txt`, expires_at: new Date(Date.now() + 7 * 86_400_000).toISOString() },
    });
    expect(made.ok()).toBeTruthy();
    const { url, token } = (await made.json()).share;
    const before = sink.mails.length;
    const sent = await api.post('/api/files/permissions/share-mail', {
      data: { share: token, email: 'amigo@e2e.test', locale: 'es' },
    });
    expect(sent.ok(), await sent.text()).toBeTruthy();
    const { h, body } = await waitMail(sink, before + 1);
    expect(h.Subject).toBe('informe.txt se ha compartido contigo');
    expect(h['Content-Language']).toBe('es');
    expect(body).toContain('Hola:');
    expect(body).toContain('Archivo: informe.txt');
    expect(body).toContain(`Descárgalo aquí:\n${url}`);
    expect(body).toContain('Este enlace es válido durante 7 días.');
    expect(body).toContain('A file has been shared with you:'); // not in the pack
    expect(body).not.toMatch(/paylaşıldı|Merhaba/);
  });

  test('a drop tells its Spanish owner in Spanish — by mail and in the bell', async ({ page, request }) => {
    const drop = await api.post('/api/files/share', { data: { path: `${STORAGE}://buzon`, kind: 'drop' } });
    expect(drop.ok(), await drop.text()).toBeTruthy();
    const token = (await drop.json()).share.token;
    const before = sink.mails.length;
    const up = await request.post(`/d/${token}`, {
      multipart: { 'file[]': { name: 'factura.txt', mimeType: 'text/plain', buffer: Buffer.from('x') } },
    });
    expect(up.ok(), await up.text()).toBeTruthy();
    // 0.54 (#191): the owner's drop mail says what their bell says - the
    // notification's title as the subject, its body as the text - in the
    // owner's language, said at the last stop (notify mailNow + say.go). The
    // separate "New file upload" mail sentence is gone. The body's words are
    // the bell's too: `{uploader} → {folder}`, the uploader being the
    // pack's "somebody", since nobody gave a name.
    const { h, body } = await waitMail(sink, before + 1);
    expect(h.Subject).toBe('Un archivo recibido');
    expect(h['Content-Language']).toBe('es');
    expect(body).toContain('Alguien → buzon');
    expect(body).not.toMatch(/file received|New file upload/);

    // The bell's words are the READER's: the Spanish phrase, its singular.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/notifications');
    await expect(page.getByText('Un archivo recibido').first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('1 file received')).toHaveCount(0);

    // The no-JavaScript drop page a Spanish browser gets.
    const html = await (await request.get(`/d/${token}`, { headers: { Accept: '*/*', 'Accept-Language': 'es-ES,es;q=0.9' } })).text();
    // `dir` follows `lang` on every server-rendered page (feat/043-rtl).
    expect(html).toContain('<html lang="es" dir="ltr">');
    expect(html).toContain('Enviar archivos');
    expect(html).toContain('Drop your files here'); // not in the pack: English
    expect(html).toContain('Compartido con <a href="https://filex.sh"');
  });
});
